package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// uploadHandler handles POST /upload.
func (s *ServerDeps) uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "Content-Type must be multipart/form-data")
		return
	}

	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	maxMemory := int64(10 << 20)
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "No file provided in 'file' field")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

	// Compute SHA256 hash.
	contentHash, err := domain.ComputeSHA256Streaming(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to compute file hash")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}
	fileContent, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	ctx := r.Context()

	// Check for duplicate.
	existingDoc, err := s.db.FindBySHA256(ctx, tenantID, contentHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error checking for duplicate document")
		return
	}
	if existingDoc != nil {
		writeJSON(w, http.StatusConflict, UploadResponse{
			JobID:   existingDoc.JobID,
			Status:  "duplicate",
			Message: "Document with identical content already exists",
		})
		return
	}

	jobID := uuid.New().String()
	now := time.Now()
	blobPath := fmt.Sprintf("%s/%d/%s%s", tenantID, now.Unix(), jobID, ext)

	contentTypeMap := map[string]string{
		".pdf":  "application/pdf",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
	}
	ct := contentTypeMap[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}

	blobURL, err := s.storage.Upload(ctx, ContainerDocuments, blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		slog.Error("Blob upload failed", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "UPLOAD_FAILED",
			TargetType: "job",
			TargetID:   jobID,
			NewState:   "FAILED",
			Error:      err.Error(),
			Timestamp:  now,
		})
		writeError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	idempotencyKey := domain.GenerateIdempotencyKey(contentHash, tenantID)

	job := &domain.Job{
		ID:            jobID,
		TenantID:      tenantID,
		WorkflowType:  domain.WorkflowDocumentIngestion,
		Status:        domain.JobStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
		TraceID:       r.Header.Get("X-Trace-ID"),
		CorrelationID: r.Header.Get("X-Correlation-ID"),
		Input: map[string]string{
			"filename":        header.Filename,
			"blob_url":        blobURL,
			"content_hash":    contentHash,
			"idempotency_key": idempotencyKey,
		},
	}

	if err := s.db.UpsertJob(ctx, job); err != nil {
		slog.Error("Failed to create job", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to create job")
		return
	}

	doc := &domain.Document{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		JobID:       jobID,
		FileName:    header.Filename,
		StoragePath: blobURL,
		Type:        "DOCUMENT",
		Status:      "pending",
		ContentHash: contentHash,
		CreatedAt:   now,
	}
	if err := s.db.UpsertDocument(ctx, doc); err != nil {
		slog.Error("Failed to create document record", "err", err)
	}

	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: header.Filename,
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	if _, err := s.rq.Enqueue(ctx, QueueDocument, docJob); err != nil {
		slog.Error("Failed to enqueue job", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "ENQUEUE_FAILED",
			TargetType: "job",
			TargetID:   jobID,
			NewState:   "FAILED",
			Error:      "Failed to enqueue for processing",
			Timestamp:  now,
		})
		job.Status = domain.JobStatusFailed
		job.Error = "Failed to enqueue for processing"
		_ = s.db.UpsertJob(ctx, job)
		writeError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:         "system",
		Action:        "UPLOAD",
		TargetType:    "job",
		TargetID:      jobID,
		NewState:      "PENDING",
		Timestamp:     now,
		TraceID:       job.TraceID,
		CorrelationID: job.CorrelationID,
	})

	slog.Info("Upload successful", "job", jobID, "blob", blobPath)
	writeJSON(w, http.StatusAccepted, UploadResponse{
		JobID:   jobID,
		Status:  "queued",
		BlobURL: blobURL,
		Message: "Document uploaded successfully and queued for processing",
	})
}

// signalsUploadHandler handles POST /signals/upload.
//
// It mirrors uploadHandler but targets the manufacturing signal ingestion
// pipeline: it uploads the blob to the documents container, creates a
// domain.Job (WorkflowSignalIngestion), records a domain.Document, and
// enqueues an agents.SignalJob to QueueSignal so the worker's
// processSignalJob picks it up and persists the manufacturing data.
//
// Handlers stay thin: no business logic beyond file I/O, job creation, and
// enqueue. The actual persistence happens in the worker (PHASE 2).
func (s *ServerDeps) signalsUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "Content-Type must be multipart/form-data")
		return
	}

	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	maxMemory := int64(10 << 20)
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "No file provided in 'file' field")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

	// Compute SHA256 hash.
	contentHash, err := domain.ComputeSHA256Streaming(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to compute file hash")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}
	fileContent, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	ctx := r.Context()

	// Check for duplicate (tenant-scoped).
	existingDoc, err := s.db.FindBySHA256(ctx, tenantID, contentHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error checking for duplicate document")
		return
	}
	if existingDoc != nil {
		writeJSON(w, http.StatusConflict, UploadResponse{
			JobID:   existingDoc.JobID,
			Status:  "duplicate",
			Message: "Document with identical content already exists",
		})
		return
	}

	jobID := uuid.New().String()
	now := time.Now()
	blobPath := fmt.Sprintf("%s/%d/%s%s", tenantID, now.Unix(), jobID, ext)

	contentTypeMap := map[string]string{
		".pdf":  "application/pdf",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
	}
	ct := contentTypeMap[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}

	blobURL, err := s.storage.Upload(ctx, ContainerDocuments, blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		slog.Error("Signal blob upload failed", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "SIGNAL_UPLOAD_FAILED",
			TargetType: "job",
			TargetID:   jobID,
			NewState:   "FAILED",
			Error:      err.Error(),
			Timestamp:  now,
		})
		writeError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	idempotencyKey := domain.GenerateIdempotencyKey(contentHash, tenantID)

	job := &domain.Job{
		ID:            jobID,
		TenantID:      tenantID,
		WorkflowType:  domain.WorkflowSignalIngestion,
		Status:        domain.JobStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
		TraceID:       r.Header.Get("X-Trace-ID"),
		CorrelationID: r.Header.Get("X-Correlation-ID"),
		Input: map[string]string{
			"filename":        header.Filename,
			"blob_url":        blobURL,
			"content_hash":    contentHash,
			"idempotency_key": idempotencyKey,
		},
	}

	if err := s.db.UpsertJob(ctx, job); err != nil {
		slog.Error("Failed to create signal job", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to create job")
		return
	}

	doc := &domain.Document{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		JobID:       jobID,
		FileName:    header.Filename,
		StoragePath: blobURL,
		Type:        "SIGNAL",
		Status:      "pending",
		ContentHash: contentHash,
		CreatedAt:   now,
	}
	if err := s.db.UpsertDocument(ctx, doc); err != nil {
		slog.Error("Failed to create signal document record", "err", err)
	}

	signalJob := &agents.SignalJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: header.Filename,
		Type:     "SIGNAL",
		JobType:  "ocr",
	}

	if _, err := s.rq.Enqueue(ctx, QueueSignal, signalJob); err != nil {
		slog.Error("Failed to enqueue signal job", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "SIGNAL_ENQUEUE_FAILED",
			TargetType: "job",
			TargetID:   jobID,
			NewState:   "FAILED",
			Error:      "Failed to enqueue for processing",
			Timestamp:  now,
		})
		job.Status = domain.JobStatusFailed
		job.Error = "Failed to enqueue for processing"
		_ = s.db.UpsertJob(ctx, job)
		writeError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:         "system",
		Action:        "SIGNAL_UPLOAD",
		TargetType:    "job",
		TargetID:      jobID,
		NewState:      "PENDING",
		Timestamp:     now,
		TraceID:       job.TraceID,
		CorrelationID: job.CorrelationID,
	})

	slog.Info("Signal upload successful", "job", jobID, "blob", blobPath)
	writeJSON(w, http.StatusAccepted, UploadResponse{
		JobID:   jobID,
		Status:  "queued",
		BlobURL: blobURL,
		Message: "Signal uploaded successfully and queued for processing",
	})
}
