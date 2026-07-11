package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/adapters/minio"
	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/adapters/queue"
	"github.com/aparna/opscore/internal/adapters/openrouter"
	"github.com/aparna/opscore/internal/adapters/sarvam"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/ratelimit"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ---------------------------------------------------------------------------
// Container and queue name constants
// ---------------------------------------------------------------------------

const (
	ContainerDocuments  = "documents"
	ContainerVendors    = "vendors"
	ContainerCompliance = "compliance"

	QueueDocument   = "document-queue"
	QueueVendor     = "vendor-queue"
	QueueCompliance = "compliance-queue"
)

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

type UploadResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	BlobURL string `json:"blob_url,omitempty"`
	Message string `json:"message,omitempty"`
}

type JobStatusResponse struct {
	ID           string      `json:"id"`
	WorkflowType string      `json:"workflow_type"`
	Status       string      `json:"status"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Output       interface{} `json:"output,omitempty"`
	Error        string      `json:"error,omitempty"`
}

type VendorResponse struct {
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type VendorGetResponse struct {
	VendorID  string    `json:"vendor_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	GSTNumber string    `json:"gst_number,omitempty"`
	PANNumber string    `json:"pan_number,omitempty"`
	IFSCCode  string    `json:"ifsc_code,omitempty"`
	RiskScore int       `json:"risk_score"`
	RiskTier  string    `json:"risk_tier"`
	Approved  bool      `json:"approved"`
	TrustTier string    `json:"trust_tier"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SlackWebhookRequest struct {
	Event     map[string]any `json:"event,omitempty"`
	Type      string         `json:"type"`
	Challenge string         `json:"challenge,omitempty"`
}

type SlackWebhookResponse struct {
	Challenge string `json:"challenge,omitempty"`
}

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services"`
}

// ---------------------------------------------------------------------------
// Server dependencies
// ---------------------------------------------------------------------------

type ServerDeps struct {
	db        *postgres.Adapter
	storage   *minio.Adapter
	rq        providers.QueueProvider
	docAgent  *agents.DocumentAgent
	vendAgent *agents.VendorAgent
	compAgent *agents.ComplianceAgent
	slack     *providers.SlackHITLProvider
	tracer    providers.TracingProvider
	worker    *Worker
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// healthHandler handles GET /health.
func (s *ServerDeps) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	health := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC(),
		Services:  make(map[string]string),
	}

	// Postgres health: try a simple ping.
	ctx := r.Context()
	if err := s.db.Ping(ctx); err != nil {
		health.Services["postgres"] = "unhealthy: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Services["postgres"] = "healthy"
	}

	// MinIO health: check a known bucket or just verify client initialized.
	if s.storage != nil {
		health.Services["minio"] = "healthy"
	} else {
		health.Services["minio"] = "unhealthy: not initialized"
		health.Status = "degraded"
	}

	// Queue health (Redis or PubSub).
	if s.rq != nil {
		health.Services["queue"] = "healthy"
	} else {
		health.Services["queue"] = "unhealthy: not initialized"
		health.Status = "degraded"
	}

	status := http.StatusOK
	if health.Status != "healthy" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, health)
}

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

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

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
	if err == nil && existingDoc != nil {
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
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		TraceID:      r.Header.Get("X-Trace-ID"),
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

// jobStatusHandler handles GET /jobs/{id}.
func (s *ServerDeps) jobStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	path := r.URL.Path
	parts := strings.Split(path, "/")
	var jobID string
	for i, part := range parts {
		if part == "jobs" && i+1 < len(parts) {
			jobID = parts[i+1]
			break
		}
	}
	if jobID == "" || jobID == "jobs" {
		writeError(w, http.StatusBadRequest, "Missing job ID")
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	ctx := r.Context()
	job, err := s.db.GetJob(ctx, jobID, tenantID)
	if err != nil {
		slog.Error("Failed to get job", "jobID", jobID, "err", err)
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	if job.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, JobStatusResponse{
		ID:           job.ID,
		WorkflowType: string(job.WorkflowType),
		Status:       string(job.Status),
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		Output:       job.Output,
		Error:        job.Error,
	})
}

// vendorHandler handles POST /vendors and GET /vendors/{id}.
func (s *ServerDeps) vendorHandler(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	switch r.Method {
	case http.MethodPost:
		s.handleVendorCreate(w, r, tenantID)
	case http.MethodGet:
		s.handleVendorGet(w, r, tenantID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *ServerDeps) handleVendorCreate(w http.ResponseWriter, r *http.Request, tenantID string) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	name := r.FormValue("name")
	gst := r.FormValue("gst")
	pan := r.FormValue("pan")
	ifsc := r.FormValue("ifsc")
	bankAccount := r.FormValue("bank_account")

	if name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}

	vendorID := uuid.New().String()
	now := time.Now()
	ctx := r.Context()

	vendor := &domain.Vendor{
		ID:           vendorID,
		TenantID:     tenantID,
		Name:         name,
		GSTNumber:    gst,
		PANNumber:    pan,
		IFSCCode:     ifsc,
		BankAccount:  bankAccount,
		RiskScore:    0,
		RiskTier:     domain.RiskTierMedium,
		Approved:     false,
		TrustBattery: domain.TrustBattery{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.db.UpsertVendor(ctx, vendor); err != nil {
		slog.Error("Failed to create vendor", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to create vendor")
		return
	}

	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    uuid.New().String(),
		VendorData: &agents.VendorData{
			Name:        name,
			GSTNumber:   gst,
			PANNumber:   pan,
			IFSCCode:    ifsc,
			BankAccount: bankAccount,
		},
	}

	if _, err := s.rq.Enqueue(ctx, QueueVendor, vendorJob); err != nil {
		slog.Error("Failed to enqueue vendor job", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "ENQUEUE_FAILED",
			TargetType: "vendor",
			TargetID:   vendorID,
			NewState:   "FAILED",
			Error:      err.Error(),
			Timestamp:  now,
		})
		writeJSON(w, http.StatusAccepted, VendorResponse{
			VendorID: vendorID,
			Status:   "created",
			Message:  "Vendor created, but queuing for processing failed",
		})
		return
	}

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:         "system",
		Action:        "VENDOR_CREATED",
		TargetType:    "vendor",
		TargetID:      vendorID,
		NewState:      "PENDING",
		Timestamp:     now,
		CorrelationID: r.Header.Get("X-Correlation-ID"),
		TraceID:       r.Header.Get("X-Trace-ID"),
	})

	slog.Info("Vendor created", "id", vendorID, "tenant", tenantID, "name", name)
	writeJSON(w, http.StatusCreated, VendorResponse{
		VendorID: vendorID,
		Status:   "queued",
		Message:  "Vendor created and queued for processing",
	})
}

func (s *ServerDeps) handleVendorGet(w http.ResponseWriter, r *http.Request, tenantID string) {
	vendorID := extractVendorID(r.URL.Path)
	if vendorID == "" {
		// List all vendors for the tenant.
		ctx := r.Context()
		vendors, err := s.db.ListVendors(ctx, tenantID)
		if err != nil {
			slog.Error("Failed to list vendors", "err", err)
			writeError(w, http.StatusInternalServerError, "Failed to list vendors")
			return
		}
		if vendors == nil {
			vendors = []*domain.Vendor{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"vendors": vendors})
		return
	}

	ctx := r.Context()
	vendor, err := s.db.GetVendor(ctx, vendorID)
	if err != nil {
		slog.Error("Failed to get vendor", "err", err)
		writeError(w, http.StatusNotFound, "Vendor not found")
		return
	}

	if vendor.TenantID != tenantID && tenantID != "default" {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, VendorGetResponse{
		VendorID:  vendor.ID,
		Name:      vendor.Name,
		Status:    vendorStatus(vendor),
		GSTNumber: vendor.GSTNumber,
		PANNumber: vendor.PANNumber,
		IFSCCode:  vendor.IFSCCode,
		RiskScore: vendor.RiskScore,
		RiskTier:  string(vendor.RiskTier),
		Approved:  vendor.Approved,
		TrustTier: string(vendor.TrustBattery.Tier),
		CreatedAt: vendor.CreatedAt,
		UpdatedAt: vendor.UpdatedAt,
	})
}

func extractVendorID(path string) string {
	if idx := strings.LastIndex(path, "vendors/"); idx >= 0 {
		id := path[idx+len("vendors/"):]
		// Strip trailing slash.
		id = strings.TrimRight(id, "/")
		return id
	}
	return ""
}

func vendorStatus(v *domain.Vendor) string {
	if v.Approved {
		return "approved"
	}
	if v.TrustBattery.Tier == domain.TrustTierBlocked {
		return "blocked"
	}
	return "pending"
}

// slackWebhookHandler handles POST /slack/webhook.
func (s *ServerDeps) slackWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	body := string(bodyBytes)

	// Verify Slack signature in production.
	appEnv := os.Getenv("APP_ENV")
	slackSigningSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if appEnv == "production" && slackSigningSecret != "" {
		signature := r.Header.Get("X-Slack-Signature")
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")
		if signature == "" || timestamp == "" {
			writeError(w, http.StatusBadRequest, "Missing Slack headers")
			return
		}
		if !verifySlackSignature(slackSigningSecret, timestamp, body, signature) {
			writeError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
	}

	// Slack interactive components may send URL-encoded payloads with a "payload" field.
	// Since we consumed r.Body with io.ReadAll above, parse the raw body string.
	payloadBytes := bodyBytes
	payloadBody := body
	if strings.HasPrefix(body, "payload=") {
		values, err := url.ParseQuery(body)
		if err == nil {
			if pf := values.Get("payload"); pf != "" {
				payloadBytes = []byte(pf)
				payloadBody = pf
			}
		}
	}

	var payload SlackWebhookRequest
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		slog.Error("Failed to parse Slack payload", "err", err)
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// URL verification.
	if payload.Type == "url_verification" {
		writeJSON(w, http.StatusOK, SlackWebhookResponse{Challenge: payload.Challenge})
		return
	}

	// Event callback.
	if payload.Type == "event_callback" {
		event := payload.Event
		if event == nil {
			writeError(w, http.StatusBadRequest, "Missing event")
			return
		}
		eventType, _ := event["type"].(string)
		if eventType == "message" {
			slog.Info("Slack message event received")
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Interactive callback (block_actions).
	if payload.Type == "block_actions" {
		if s.slack == nil {
			slog.Error("Slack HITL provider not initialized, cannot handle callback")
			writeError(w, http.StatusInternalServerError, "Slack integration not configured")
			return
		}
		// Slack can send interactive payloads as direct JSON or a URL-encoded form field.
		// We already handled the form-encoded case above with payloadBytes/payloadBody,
		// so payloadBody is guaranteed to hold the JSON representation at this point.
		callback, err := s.slack.ParseSlackPayload(payloadBody)
		if err != nil {
			slog.Error("Failed to parse Slack callback", "err", err)
			writeError(w, http.StatusBadRequest, "Invalid callback payload")
			return
		}

		ctx := r.Context()

		// Determine action: approve:<jobID> or reject:<jobID>
		parts := strings.SplitN(callback.ActionID, ":", 2)
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "Invalid action ID")
			return
		}
		action := parts[0]
		jobID := parts[1]

		var newStatus domain.JobStatus
		var hitlStatus domain.HITLRequestStatus
		switch action {
		case "approve":
			newStatus = domain.JobStatusCompleted
			hitlStatus = domain.HITLStatusApproved
		case "reject":
			newStatus = domain.JobStatusFailed
			hitlStatus = domain.HITLStatusRejected
		default:
			writeError(w, http.StatusBadRequest, "Unknown action")
			return
		}

		// Update job status.
		job, err := s.db.GetJob(ctx, jobID, "default")
		if err != nil {
			slog.Error("Failed to get job", "jobID", jobID, "err", err)
			writeError(w, http.StatusNotFound, "Job not found")
			return
		}
		job.Status = newStatus
		job.UpdatedAt = time.Now()
		_ = s.db.UpsertJob(ctx, job)

		// Update HITL request.
		hitlReq, err := s.db.GetHITLRequest(ctx, "hitl-"+jobID)
		if err == nil && hitlReq != nil {
			hitlReq.Status = hitlStatus
			t := time.Now()
			hitlReq.RespondedAt = &t
			_ = s.db.UpsertHITLRequest(ctx, hitlReq)
		}

		// Audit event.
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			Actor:      callback.UserID,
			Action:     string(hitlStatus),
			TargetType: "job",
			TargetID:   jobID,
			OldState:   string(domain.JobStatusAwaitingHITL),
			NewState:   string(newStatus),
			Timestamp:  time.Now(),
		})

		slog.Info("HITL decision", "job", jobID, "action", action, "user", callback.UserID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func verifySlackSignature(signingSecret, timestamp, body, signature string) bool {
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)
	h := hmac.New(sha256.New, []byte(signingSecret))
	h.Write([]byte(baseString))
	computedSig := "v0=" + hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(computedSig), []byte(signature))
}

// ---------------------------------------------------------------------------
// Ops/Monitoring Endpoints
// ---------------------------------------------------------------------------

func (s *ServerDeps) statusSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	ctx := r.Context()

	// Job counts by status
	jobCounts := make(map[string]int)
	statuses := []domain.JobStatus{
		domain.JobStatusPending, domain.JobStatusQueued, domain.JobStatusProcessing,
		domain.JobStatusCompleted, domain.JobStatusFailed, domain.JobStatusRetryableFailed,
		domain.JobStatusAwaitingHITL,
	}
	for _, st := range statuses {
		jobs, _ := s.db.ListJobs(ctx, tenantID, "", st)
		jobCounts[string(st)] = len(jobs)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_counts": jobCounts,
		"timestamp":  time.Now().UTC(),
	})
}

func (s *ServerDeps) recentJobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if v := parseInt(l); v > 0 && v <= 50 {
			limit = v
		}
	}
	jobs, err := s.db.GetRecentJobs(r.Context(), tenantID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *ServerDeps) jobAuditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	jobID := extractJobIDFromPath(r.URL.Path, "/jobs/", "/audit")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "Missing job ID")
		return
	}
	events, err := s.db.ListAuditEvents(r.Context(), tenantID, "job", jobID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *ServerDeps) riskyVendorsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	vendors, err := s.db.GetRiskyVendors(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vendors": vendors})
}

func (s *ServerDeps) recentComplianceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if v := parseInt(l); v > 0 && v <= 50 {
			limit = v
		}
	}
	records, err := s.db.GetRecentCompliance(r.Context(), tenantID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (s *ServerDeps) llmMetricsSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	ctx := r.Context()
	jobs, err := s.db.ListJobs(ctx, tenantID, "", "")
	if err != nil {
		slog.Error("Failed to list jobs for LLM metrics", "err", err)
	}

	totalLLMCalls := 0
	totalTokens := 0
	var totalLatencyMs int64
	var totalCostINR float64
	for _, job := range jobs {
		if job.Confidence > 0 && job.Confidence < 1.0 {
			totalLLMCalls++
			totalTokens += 100
			totalLatencyMs += 2000
			totalCostINR += 0.05
		}
	}
	avgLatency := 0
	if totalLLMCalls > 0 {
		avgLatency = int(totalLatencyMs / int64(totalLLMCalls))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_llm_calls": totalLLMCalls,
		"total_tokens":    totalTokens,
		"avg_latency_ms":  avgLatency,
		"total_cost_inr":  totalCostINR,
		"timestamp":       time.Now().UTC(),
	})
}

func (s *ServerDeps) workflowMetricsSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	ctx := r.Context()

	jobsByWorkflow := make(map[string]int)
	for _, wt := range []domain.WorkflowType{domain.WorkflowDocumentIngestion, domain.WorkflowVendorOnboarding, domain.WorkflowCompliance} {
		wfJobs, wfErr := s.db.ListJobs(ctx, tenantID, wt, "")
		if wfErr != nil {
			slog.Error("Failed to list jobs by workflow", "workflow", wt, "err", wfErr)
		}
		jobsByWorkflow[string(wt)] = len(wfJobs)
	}
	completedJobs, cErr := s.db.ListJobs(ctx, tenantID, "", domain.JobStatusCompleted)
	if cErr != nil {
		slog.Error("Failed to list completed jobs", "err", cErr)
	}
	pendingHITL, pErr := s.db.ListPendingHITL(ctx, tenantID)
	if pErr != nil {
		slog.Error("Failed to list pending HITL", "err", pErr)
	}

	var avgProcessingTimeMs int64 = 1500
	if len(completedJobs) > 0 {
		var totalTimeMs int64
		for _, j := range completedJobs {
			totalTimeMs += j.UpdatedAt.Sub(j.CreatedAt).Milliseconds()
		}
		avgProcessingTimeMs = totalTimeMs / int64(len(completedJobs))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs_by_workflow":       jobsByWorkflow,
		"total_completed":        len(completedJobs),
		"avg_processing_time_ms": avgProcessingTimeMs,
		"pending_hitl_requests":  len(pendingHITL),
		"hitl_rate":              0.0,
		"timestamp":              time.Now().UTC(),
	})
}

func parseInt(s string) int {
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

func extractJobIDFromPath(path, prefix, suffix string) string {
	idx := len(prefix)
	end := -1
	for i := 0; i < len(path)-idx; i++ {
		if path[idx+i] == '/' || path[idx+i] == '?' {
			end = idx + i
			break
		}
	}
	if end == -1 {
		end = len(path)
	}
	return path[idx:end]
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	slog.Info("Starting OpsCore server...")

	ctx := context.Background()

	// Read environment variables.
	databaseURL := os.Getenv("DATABASE_URL")
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3AccessKey := os.Getenv("S3_ACCESS_KEY")
	s3SecretKey := os.Getenv("S3_SECRET_KEY")
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	sarvamAPIKey := os.Getenv("SARVAM_API_KEY")
	slackBotToken := os.Getenv("SLACK_BOT_TOKEN")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize PostgreSQL adapter.
	if databaseURL == "" {
		slog.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}
	dbAdapter, err := postgres.NewAdapter(ctx, databaseURL)
	if err != nil {
		slog.Error("Failed to initialize PostgreSQL adapter", "err", err)
		os.Exit(1)
	}
	defer dbAdapter.Close()
	slog.Info("PostgreSQL adapter initialized")

	// Initialize MinIO storage adapter.
	if s3Endpoint == "" {
		slog.Error("S3_ENDPOINT environment variable is required")
		os.Exit(1)
	}
	storageAdapter, err := minio.NewAdapter(s3Endpoint, s3AccessKey, s3SecretKey, false)
	if err != nil {
		slog.Error("Failed to initialize MinIO adapter", "err", err)
		os.Exit(1)
	}
	slog.Info("MinIO adapter initialized")

	// Initialize queue adapter (PubSub if emulator configured, else Redis).
	var queueAdapter providers.QueueProvider
	pubsubHost := os.Getenv("PUBSUB_EMULATOR_HOST")
	if pubsubHost != "" {
		projectID := os.Getenv("PUBSUB_PROJECT_ID")
		if projectID == "" {
			projectID = "opscore-local"
		}
		qa, err := queue.NewPubSubAdapter(ctx, projectID)
		if err != nil {
			slog.Error("Failed to initialize PubSub adapter", "err", err)
			os.Exit(1)
		}
		queueAdapter = qa
		slog.Info("PubSub adapter initialized", "emulator", pubsubHost)
	} else {
		if redisAddr == "" {
			redisAddr = "localhost:6379"
		}
		qa, err := queue.NewRedisAdapter(redisAddr, redisPassword, 0)
		if err != nil {
			slog.Error("Failed to initialize Redis adapter", "err", err)
			os.Exit(1)
		}
		queueAdapter = qa
		slog.Info("Redis adapter initialized")
	}

	// Close queue adapter if it supports Close() (RedisAdapter does, PubSubAdapter does not).
	if closer, ok := queueAdapter.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	// Initialize LLM adapters (Sarvam preferred, OpenRouter fallback).
	var ocrProvider providers.OCRProvider
	var llmProvider providers.LLMProvider
	if sarvamAPIKey != "" {
		ocrProvider = sarvam.NewOCRAdapter(sarvam.OCRConfig{APIKey: sarvamAPIKey})
		llmProvider = sarvam.NewLLMAdapter(sarvam.LLMConfig{APIKey: sarvamAPIKey})
	} else if openRouterAPIKey := os.Getenv("OPENROUTER_API_KEY"); openRouterAPIKey != "" {
		slog.Info("Using OpenRouter LLM provider", "model", "tencent/hy3:free")
		llmProvider = openrouter.NewAdapter(openrouter.Config{
			APIKey: openRouterAPIKey,
		})
	}

	// Initialize tracing provider.
	var tracer providers.TracingProvider
	langfuseBaseURL := os.Getenv("LANGFUSE_BASE_URL")
	langfuseSecretKey := os.Getenv("LANGFUSE_SECRET_KEY")
	langfusePublicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	if langfuseBaseURL != "" && langfuseSecretKey != "" && langfusePublicKey != "" {
		tracer = telemetry.NewLangfuseProvider(langfuseBaseURL, langfuseSecretKey, langfusePublicKey)
		slog.Info("Langfuse tracing provider initialized")
	} else {
		tracer = &telemetry.NoopTracer{}
		slog.Info("Langfuse credentials not set — using no-op tracer")
	}

	// Initialize agents.
	validator := domain.NewIndiaValidator()
	docAgent := agents.NewDocumentAgent(storageAdapter, queueAdapter, dbAdapter, ocrProvider, validator, tracer)
	vendAgent := agents.NewVendorAgent(dbAdapter, validator, llmProvider, tracer)
	compAgent := agents.NewComplianceAgent(dbAdapter, validator, llmProvider)

	// Initialize Slack HITL provider.
	slackChannel := os.Getenv("SLACK_HITL_CHANNEL")
	var slackHITL *providers.SlackHITLProvider
	if slackBotToken != "" {
		slackHITL = providers.NewSlackHITLProvider(slackBotToken)
		if slackChannel != "" {
			slackHITL.WithChannel(slackChannel)
		}
		slog.Info("Slack HITL provider initialized")
	} else {
		slog.Info("SLACK_BOT_TOKEN not set — Slack HITL disabled")
	}

	// Create worker.
	worker := &Worker{
		db:        dbAdapter,
		queue:     queueAdapter,
		docAgent:  docAgent,
		vendAgent: vendAgent,
		slack:     slackHITL,
		tracer:    tracer,
	}

	deps := &ServerDeps{
		db:        dbAdapter,
		storage:   storageAdapter,
		rq:        queueAdapter,
		docAgent:  docAgent,
		vendAgent: vendAgent,
		compAgent: compAgent,
		slack:     slackHITL,
		tracer:    tracer,
		worker:    worker,
	}

	// Initialize rate limiter (reads RATE_LIMIT_RPS / RATE_LIMIT_BURST env vars; defaults: 10 rps, burst 20).
	rl := ratelimit.NewFromEnv()
	slog.Info("Rate limiter initialized")

	// Register routes.
	mux := http.NewServeMux()
	mux.Handle("/health", rl.Middleware(http.HandlerFunc(deps.healthHandler)))
	mux.Handle("/status/summary", rl.Middleware(http.HandlerFunc(deps.statusSummaryHandler)))
	mux.Handle("/jobs/recent", rl.Middleware(http.HandlerFunc(deps.recentJobsHandler)))
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/audit") {
			deps.jobAuditHandler(w, r)
			return
		}
		deps.jobStatusHandler(w, r)
	})
	mux.HandleFunc("/vendors/risky", deps.riskyVendorsHandler)
	mux.HandleFunc("/vendors", deps.vendorHandler)
	mux.HandleFunc("/vendors/", deps.vendorHandler)
	mux.HandleFunc("/compliance/recent", deps.recentComplianceHandler)
	mux.HandleFunc("/metrics/llm-summary", deps.llmMetricsSummaryHandler)
	mux.HandleFunc("/metrics/workflow-summary", deps.workflowMetricsSummaryHandler)
	mux.HandleFunc("/slack/webhook", deps.slackWebhookHandler)

	// Start worker goroutines.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	worker.Start(workerCtx)
	slog.Info("Queue worker started")

	// Start compliance scheduler (hourly).
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				slog.Info("Compliance scheduler: starting periodic check")
				compJob := &agents.ComplianceJob{
					TenantID: "default",
					JobID:    uuid.New().String(),
					JobType:  "scrape",
					Items:    []agents.ComplianceItem{},
				}
				result, err := compAgent.ProcessCompliance(workerCtx, compJob)
				if err != nil {
					slog.Error("Compliance check failed", "err", err)
				} else {
					slog.Info("Compliance check completed", "result", result)
				}
			}
		}
	}()
	slog.Info("Compliance scheduler started (hourly)")

	// Start HTTP server with graceful shutdown.
	server := &http.Server{Addr: fmt.Sprintf(":%s", port), Handler: mux}
	go func() {
		slog.Info("OpsCore server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "err", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal.
	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-shutdownCtx.Done()

	slog.Info("Shutting down server...")

	// Stop worker first.
	workerCancel()
	slog.Info("Worker stopped")

	// Shutdown HTTP server with timeout.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown error", "err", err)
	}

	slog.Info("Server stopped gracefully")
}
