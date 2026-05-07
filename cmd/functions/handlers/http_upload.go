package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/google/uuid"
)

// HTTPUploadHandler handles POST /upload
// Validates auth, stores file to Blob, writes job to Cosmos, enqueues for processing
func HTTPUploadHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received upload request")

	// Validate request method
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Validate content type (multipart/form-data)
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "Content-Type must be multipart/form-data")
		return
	}

	// Extract tenant ID from header (simplified auth - in production use Azure AD)
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Parse multipart form
	maxMemory := int64(10 << 20) // 10 MB
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	// Get the file
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "No file provided in 'file' field")
		return
	}
	defer file.Close()

	// Validate file type
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

	// Read file content
	fileContent, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	// Get adapters
	blobAdapter, err := getBlobAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Storage service unavailable")
		return
	}

	queueAdapter, err := getQueueAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Queue service unavailable")
		return
	}

	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	// Generate job ID and blob path
	jobID := uuid.New().String()
	now := time.Now()
	blobPath := fmt.Sprintf("%s/%d/%s%s", tenantID, now.Unix(), jobID, ext)

	// Determine content type
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

	// Upload to Blob Storage
	blobURL, err := blobAdapter.Upload(ctx, ContainerDocuments, blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		log.Printf("Blob upload failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	// Create job in Cosmos DB
	job := &domain.Job{
		ID:             jobID,
		TenantID:       tenantID,
		WorkflowType:   domain.WorkflowDocumentIngestion,
		Status:         domain.JobStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
		TraceID:        r.Header.Get("X-Trace-ID"),
		CorrelationID:  r.Header.Get("X-Correlation-ID"),
		Input:          map[string]string{"filename": header.Filename, "blob_url": blobURL},
	}

	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		log.Printf("Failed to create job: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to create job")
		return
	}

	// Enqueue document processing job
	docJob := &agents.DocumentJob{
		TenantID:  tenantID,
		JobID:     jobID,
		BlobURL:   blobURL,
		FileName:  header.Filename,
		Type:      "DOCUMENT",
		JobType:   "ocr",
	}

	if _, err := queueAdapter.Enqueue(ctx, QueueDocument, docJob); err != nil {
		log.Printf("Failed to enqueue job: %v", err)
		// Job is created, but queueing failed - mark as failed
		job.Status = domain.JobStatusFailed
		job.Error = "Failed to enqueue for processing"
		cosmosAdapter.UpsertJob(ctx, job)
		writeError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	// Log audit event
	auditEvent := &domain.AuditEvent{
		Actor:        "system",
		Action:       "UPLOAD",
		TargetType:   "job",
		TargetID:     jobID,
		NewState:     "PENDING",
		Timestamp:    now,
		TraceID:      job.TraceID,
		CorrelationID: job.CorrelationID,
	}
	cosmosAdapter.AppendAuditEvent(ctx, auditEvent)

	// Return response
	log.Printf("Upload successful: job=%s, blob=%s", jobID, blobPath)
	writeJSON(w, http.StatusAccepted, UploadResponse{
		JobID:   jobID,
		Status:  "PENDING",
		BlobURL: blobURL,
		Message: "Document uploaded successfully and queued for processing",
	})
}

// UploadWithFormHandler handles file upload with explicit form field extraction
func UploadWithFormHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received form-based upload request")

	// Parse content type for multipart
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid multipart form: %v", err))
		return
	}

	// Extract tenant ID
	tenantID := r.FormValue("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}

	// Validate file exists
	file, header, err := r.FormFile("document")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing 'document' file field")
		return
	}
	defer file.Close()

	// Get adapters (same as above)
	blobAdapter, _ := getBlobAdapter(ctx)
	queueAdapter, _ := getQueueAdapter(ctx)
	cosmosAdapter, _ := getCosmosAdapter(ctx)

	if blobAdapter == nil || queueAdapter == nil || cosmosAdapter == nil {
		writeError(w, http.StatusServiceUnavailable, "Service unavailable")
		return
	}

	// Generate IDs and paths
	jobID := uuid.New().String()
	blobPath := fmt.Sprintf("uploads/%s/%s", tenantID, header.Filename)

	// Upload to blob
	content, _ := io.ReadAll(file)
	blobURL, err := blobAdapter.Upload(ctx, ContainerDocuments, blobPath, bytes.NewReader(content), "application/pdf")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Upload failed")
		return
	}

	// Create job
	job := &domain.Job{
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusPending,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Input:        map[string]string{"blob_url": blobURL},
	}

	cosmosAdapter.UpsertJob(ctx, job)

	// Queue job
	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: header.Filename,
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}
	queueAdapter.Enqueue(ctx, QueueDocument, docJob)

	writeJSON(w, http.StatusAccepted, UploadResponse{
		JobID:  jobID,
		Status: "PENDING",
	})
}