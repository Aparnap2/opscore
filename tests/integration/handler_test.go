package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// Mock types for handler testing
// ---------------------------------------------------------------------------

// mockDB implements DBProvider + Ping for handler testing.
type mockHandlerDB struct {
	mu           sync.Mutex
	jobs         map[string]*domain.Job
	vendors      map[string]*domain.Vendor
	documents    map[string]*domain.Document
	hitlRequests map[string]*domain.HITLRequest
	exceptions   map[string]*domain.ExceptionCase
	auditEvents  []*domain.AuditEvent
	pingErr      error
}

func newMockHandlerDB() *mockHandlerDB {
	return &mockHandlerDB{
		jobs:         make(map[string]*domain.Job),
		vendors:      make(map[string]*domain.Vendor),
		documents:    make(map[string]*domain.Document),
		hitlRequests: make(map[string]*domain.HITLRequest),
		exceptions:   make(map[string]*domain.ExceptionCase),
	}
}

func (m *mockHandlerDB) Ping(_ context.Context) error { return m.pingErr }

func (m *mockHandlerDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return nil
}

func (m *mockHandlerDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	return job, nil
}

func (m *mockHandlerDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Job
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			result = append(result, j)
		}
	}
	return result, nil
}

func (m *mockHandlerDB) UpsertVendor(_ context.Context, vendor *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[vendor.ID] = vendor
	return nil
}

func (m *mockHandlerDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor not found")
	}
	return v, nil
}

func (m *mockHandlerDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Vendor
	for _, v := range m.vendors {
		if v.TenantID == tenantID {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *mockHandlerDB) UpsertDocument(_ context.Context, doc *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[doc.ID] = doc
	return nil
}

func (m *mockHandlerDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("document not found")
	}
	return d, nil
}

func (m *mockHandlerDB) FindBySHA256(_ context.Context, tenantID, contentHash string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.documents {
		if d.TenantID == tenantID && d.ContentHash == contentHash {
			return d, nil
		}
	}
	return nil, nil
}

func (m *mockHandlerDB) UpsertHITLRequest(_ context.Context, req *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlRequests[req.ID] = req
	return nil
}

func (m *mockHandlerDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlRequests[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("HITL request not found")
	}
	return r, nil
}

func (m *mockHandlerDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.HITLRequest
	for _, r := range m.hitlRequests {
		if r.TenantID == tenantID && r.Status == domain.HITLStatusPending {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockHandlerDB) ListHITLRequests(_ context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.HITLRequest
	for _, r := range m.hitlRequests {
		if r.TenantID != tenantID {
			continue
		}
		if status != "" && string(r.Status) != status {
			continue
		}
		result = append(result, r)
	}
	// Sort by sent_at descending (simple bubble sort for mock).
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].SentAt.Before(result[j].SentAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	// Apply pagination.
	if offset >= len(result) {
		return []*domain.HITLRequest{}, nil
	}
	result = result[offset:]
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockHandlerDB) UpsertExceptionCase(_ context.Context, ec *domain.ExceptionCase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exceptions[ec.ID] = ec
	return nil
}

func (m *mockHandlerDB) GetExceptionCaseByID(_ context.Context, id, tenantID string) (*domain.ExceptionCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ec, ok := m.exceptions[id]
	if !ok || ec.TenantID != tenantID {
		return nil, fmt.Errorf("exception case not found")
	}
	return ec, nil
}

func (m *mockHandlerDB) ListExceptionCases(_ context.Context, tenantID, status, mismatchType string, limit, offset int) ([]*domain.ExceptionCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.ExceptionCase
	for _, ec := range m.exceptions {
		if ec.TenantID != tenantID {
			continue
		}
		if status != "" && ec.Status != status {
			continue
		}
		if mismatchType != "" && string(ec.Type) != mismatchType {
			continue
		}
		result = append(result, ec)
	}
	if offset >= len(result) {
		return []*domain.ExceptionCase{}, nil
	}
	result = result[offset:]
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockHandlerDB) UpdateExceptionCaseStatus(_ context.Context, id, tenantID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ec, ok := m.exceptions[id]
	if !ok || ec.TenantID != tenantID {
		return fmt.Errorf("exception case not found")
	}
	ec.Status = status
	ec.UpdatedAt = time.Now()
	return nil
}

func (m *mockHandlerDB) AppendAuditEvent(_ context.Context, event *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, event)
	return nil
}

func (m *mockHandlerDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.AuditEvent
	for _, e := range m.auditEvents {
		if e.TenantID == tenantID {
			if targetType != "" && e.TargetType != targetType {
				continue
			}
			if targetID != "" && e.TargetID != targetID {
				continue
			}
			result = append(result, e)
		}
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockHandlerDB) IsVersionConflict(_ error) bool {
	return false
}

func (m *mockHandlerDB) WithTx(_ context.Context, _ string, fn func(context.Context) error) error {
	return fn(context.Background())
}

// mockHandlerStorage implements StorageProvider for handler tests.
type mockHandlerStorage struct {
	mu    sync.Mutex
	urls  map[string]string
	upErr error
}

func (m *mockHandlerStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	if m.upErr != nil {
		return "", m.upErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	url := fmt.Sprintf("https://storage.local/%s/%s", container, key)
	m.urls[key] = url
	return url, nil
}

func (m *mockHandlerStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHandlerStorage) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockHandlerStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

// mockHandlerQueue implements QueueProvider + Ping.
type mockHandlerQueue struct {
	mu         sync.Mutex
	messages   []string
	pingErr    error
	enqueueErr error
}

func (m *mockHandlerQueue) Ping(_ context.Context) error { return m.pingErr }

func (m *mockHandlerQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	if m.enqueueErr != nil {
		return "", m.enqueueErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("msg-%d", len(m.messages))
	m.messages = append(m.messages, id)
	return id, nil
}

func (m *mockHandlerQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}

func (m *mockHandlerQueue) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockHandlerQueue) Poison(_ context.Context, _, _ string) error {
	return nil
}

// mockSlackParser implements ParseSlackPayload for handler tests.
type mockSlackParser struct {
	parseFunc func(payload string) (*providers.SlackCallbackResult, error)
}

func (m *mockSlackParser) ParseSlackPayload(payload string) (*providers.SlackCallbackResult, error) {
	if m.parseFunc != nil {
		return m.parseFunc(payload)
	}
	return &providers.SlackCallbackResult{
		ActionID: "approve:test-job-id",
		JobID:    "test-job-id",
		UserID:   "U12345",
		Type:     "block_actions",
	}, nil
}

// ---------------------------------------------------------------------------
// Test server — replicates main.go handler logic using our mocks
// ---------------------------------------------------------------------------

const (
	containerDocuments = "documents"
	queueDocument      = "document-queue"
	queueVendor        = "vendor-queue"
)

type testServerDeps struct {
	db    *mockHandlerDB
	stor  *mockHandlerStorage
	q     *mockHandlerQueue
	slack *mockSlackParser
}

func newTestServer() *testServerDeps {
	return &testServerDeps{
		db:    newMockHandlerDB(),
		stor:  &mockHandlerStorage{urls: make(map[string]string)},
		q:     &mockHandlerQueue{},
		slack: &mockSlackParser{},
	}
}

// registerRoutes registers handler routes on the given mux, mirroring main.go.
func (s *testServerDeps) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/upload", s.uploadHandler)
	mux.HandleFunc("/jobs/", s.jobStatusHandler)
	mux.HandleFunc("/vendors", s.vendorHandler)
	mux.HandleFunc("/vendors/", s.vendorHandler)
	mux.HandleFunc("/slack/webhook", s.slackWebhookHandler)
}

// --- JSON helpers ---

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

// --- Handler: /health ---

func (s *testServerDeps) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	health := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"services": map[string]string{
			"postgres": "healthy",
			"minio":    "healthy",
			"redis":    "healthy",
		},
	}

	status := http.StatusOK
	_ = s.db.Ping(r.Context())
	_ = s.q.Ping(r.Context())

	writeJSON(w, status, health)
}

// --- Handler: /upload ---

func (s *testServerDeps) uploadHandler(w http.ResponseWriter, r *http.Request) {
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

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "No file provided in 'file' field")
		return
	}
	defer file.Close()

	ext := ""
	if idx := strings.LastIndex(header.Filename, "."); idx >= 0 {
		ext = strings.ToLower(header.Filename[idx:])
	}
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

	// Compute SHA256
	contentHash, err := computeSHA256(file)
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

	// Duplicate check
	existingDoc, err := s.db.FindBySHA256(ctx, tenantID, contentHash)
	if err == nil && existingDoc != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"job_id":  existingDoc.JobID,
			"status":  "duplicate",
			"message": "Document with identical content already exists",
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

	blobURL, err := s.stor.Upload(ctx, containerDocuments, blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	job := &domain.Job{
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		Input: map[string]string{
			"filename":     header.Filename,
			"blob_url":     blobURL,
			"content_hash": contentHash,
		},
	}

	if err := s.db.UpsertJob(ctx, job); err != nil {
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
	_ = s.db.UpsertDocument(ctx, doc)

	if _, err := s.q.Enqueue(ctx, queueDocument, map[string]string{"job_id": jobID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:      "system",
		Action:     "UPLOAD",
		TargetType: "job",
		TargetID:   jobID,
		NewState:   "PENDING",
		Timestamp:  now,
	})

	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id":   jobID,
		"status":   "queued",
		"blob_url": blobURL,
		"message":  "Document uploaded successfully and queued for processing",
	})
}

// --- Handler: /jobs/{id} ---

func (s *testServerDeps) jobStatusHandler(w http.ResponseWriter, r *http.Request) {
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

	job, err := s.db.GetJob(r.Context(), jobID, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":            job.ID,
		"workflow_type": string(job.WorkflowType),
		"status":        string(job.Status),
		"created_at":    job.CreatedAt,
		"updated_at":    job.UpdatedAt,
		"output":        job.Output,
		"error":         job.Error,
	})
}

// --- Handler: /vendors ---

func (s *testServerDeps) vendorHandler(w http.ResponseWriter, r *http.Request) {
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

func (s *testServerDeps) handleVendorCreate(w http.ResponseWriter, r *http.Request, tenantID string) {
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
		ID:          vendorID,
		TenantID:    tenantID,
		Name:        name,
		GSTNumber:   gst,
		PANNumber:   pan,
		IFSCCode:    ifsc,
		BankAccount: bankAccount,
		RiskScore:   0,
		RiskTier:    domain.RiskTierMedium,
		Approved:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.db.UpsertVendor(ctx, vendor); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create vendor")
		return
	}

	_, _ = s.q.Enqueue(ctx, queueVendor, map[string]string{"vendor_id": vendorID})

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:      "system",
		Action:     "VENDOR_CREATED",
		TargetType: "vendor",
		TargetID:   vendorID,
		NewState:   "PENDING",
		Timestamp:  now,
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"vendor_id": vendorID,
		"status":    "queued",
		"message":   "Vendor created and queued for processing",
	})
}

func (s *testServerDeps) handleVendorGet(w http.ResponseWriter, r *http.Request, tenantID string) {
	vendorID := extractVendorID(r.URL.Path)
	if vendorID == "" {
		writeError(w, http.StatusBadRequest, "Vendor ID is required")
		return
	}

	vendor, err := s.db.GetVendor(r.Context(), vendorID, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Vendor not found")
		return
	}

	status := "pending"
	if vendor.Approved {
		status = "approved"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vendor_id":  vendor.ID,
		"name":       vendor.Name,
		"status":     status,
		"gst_number": vendor.GSTNumber,
		"pan_number": vendor.PANNumber,
		"risk_score": vendor.RiskScore,
		"risk_tier":  string(vendor.RiskTier),
		"approved":   vendor.Approved,
		"created_at": vendor.CreatedAt,
		"updated_at": vendor.UpdatedAt,
	})
}

func extractVendorID(path string) string {
	if idx := strings.LastIndex(path, "vendors/"); idx >= 0 {
		id := path[idx+len("vendors/"):]
		id = strings.TrimRight(id, "/")
		return id
	}
	return ""
}

// --- Handler: /slack/webhook ---

func (s *testServerDeps) slackWebhookHandler(w http.ResponseWriter, r *http.Request) {
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

	payloadBytes := bodyBytes
	if strings.HasPrefix(body, "payload=") {
		values, parseErr := parseQuery(body)
		if parseErr == nil {
			if pf, ok := values["payload"]; ok && len(pf) > 0 {
				payloadBytes = []byte(pf[0])
			}
		}
	}

	var payload struct {
		Type      string         `json:"type"`
		Challenge string         `json:"challenge,omitempty"`
		Event     map[string]any `json:"event,omitempty"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// URL verification
	if payload.Type == "url_verification" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": payload.Challenge})
		return
	}

	// Event callback
	if payload.Type == "event_callback" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Block actions
	if payload.Type == "block_actions" {
		if s.slack == nil {
			writeError(w, http.StatusInternalServerError, "Slack integration not configured")
			return
		}

		callback, err := s.slack.ParseSlackPayload(payloadBody(body))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid callback payload")
			return
		}

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

		ctx := r.Context()
		job, err := s.db.GetJob(ctx, jobID, "default")
		if err != nil {
			writeError(w, http.StatusNotFound, "Job not found")
			return
		}
		job.Status = newStatus
		job.UpdatedAt = time.Now()
		_ = s.db.UpsertJob(ctx, job)

		hitlReq, err := s.db.GetHITLRequest(ctx, "hitl-"+jobID, "default")
		if err == nil && hitlReq != nil {
			hitlReq.Status = hitlStatus
			t := time.Now()
			hitlReq.RespondedAt = &t
			_ = s.db.UpsertHITLRequest(ctx, hitlReq)
		}

		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   job.TenantID,
			Actor:      callback.UserID,
			Action:     string(hitlStatus),
			TargetType: "job",
			TargetID:   jobID,
			OldState:   string(domain.JobStatusAwaitingHITL),
			NewState:   string(newStatus),
			Timestamp:  time.Now(),
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func payloadBody(body string) string {
	if strings.HasPrefix(body, "payload=") {
		values, _ := parseQuery(body)
		if pf, ok := values["payload"]; ok && len(pf) > 0 {
			return pf[0]
		}
	}
	return body
}

func parseQuery(query string) (map[string][]string, error) {
	result := make(map[string][]string)
	for _, pair := range strings.Split(query, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			result[kv[0]] = append(result[kv[0]], kv[1])
		}
	}
	return result, nil
}

// --- Utility ---

func computeSHA256(r io.Reader) (string, error) {
	hash := sha256.New()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ---------------------------------------------------------------------------
// Actual handler tests
// ---------------------------------------------------------------------------

func TestUploadHandler(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	content := "%PDF-1.4 test document content for upload test"
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "invoice-2024.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte(content))
	w.Close()

	req, err := http.NewRequest("POST", ts.URL+"/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "test-tenant")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202; body=%s", resp.StatusCode, string(body))
	}

	var uploadResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		t.Fatalf("Decode response: %v", err)
	}

	if uploadResp["job_id"] == "" {
		t.Error("job_id is empty in response")
	}
	if uploadResp["status"] != "queued" {
		t.Errorf("status = %s, want queued", uploadResp["status"])
	}

	// Verify job stored in DB
	srv.db.mu.Lock()
	jobCount := len(srv.db.jobs)
	srv.db.mu.Unlock()
	if jobCount != 1 {
		t.Errorf("expected 1 job in DB, got %d", jobCount)
	}
}

func TestUploadHandler_InvalidFileType(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	content := "some executable content"
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "malware.exe")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte(content))
	w.Close()

	req, err := http.NewRequest("POST", ts.URL+"/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "test-tenant")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp["error"] == "" {
		t.Error("expected error message for invalid file type")
	}
}

func TestUploadHandler_Duplicate(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	content := "duplicate content for hash matching"
	var buf1 bytes.Buffer
	w1 := multipart.NewWriter(&buf1)
	fw1, _ := w1.CreateFormFile("file", "doc.pdf")
	_, _ = fw1.Write([]byte(content))
	w1.Close()

	// First upload
	req1, _ := http.NewRequest("POST", ts.URL+"/upload", &buf1)
	req1.Header.Set("Content-Type", w1.FormDataContentType())
	req1.Header.Set("X-Tenant-ID", "test-tenant")

	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("First upload: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first upload status = %d, want 202", resp1.StatusCode)
	}

	// Second upload (same content)
	var buf2 bytes.Buffer
	w2 := multipart.NewWriter(&buf2)
	fw2, _ := w2.CreateFormFile("file", "doc.pdf")
	_, _ = fw2.Write([]byte(content))
	w2.Close()

	req2, _ := http.NewRequest("POST", ts.URL+"/upload", &buf2)
	req2.Header.Set("Content-Type", w2.FormDataContentType())
	req2.Header.Set("X-Tenant-ID", "test-tenant")

	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("Second upload: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second upload status = %d, want 409", resp2.StatusCode)
	}

	var dupResp map[string]string
	json.NewDecoder(resp2.Body).Decode(&dupResp)
	if dupResp["status"] != "duplicate" {
		t.Errorf("status = %s, want duplicate", dupResp["status"])
	}
}

func TestJobStatusHandler(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a job
	jobID := uuid.New().String()
	now := time.Now()
	job := &domain.Job{
		ID:           jobID,
		TenantID:     "test-tenant",
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusCompleted,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = srv.db.UpsertJob(nil, job)

	resp, err := ts.Client().Get(ts.URL + "/jobs/" + jobID)
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var statusResp map[string]any
	json.NewDecoder(resp.Body).Decode(&statusResp)

	if statusResp["id"] != jobID {
		t.Errorf("id = %v, want %s", statusResp["id"], jobID)
	}
	if statusResp["status"] != "COMPLETED" {
		t.Errorf("status = %v, want COMPLETED", statusResp["status"])
	}
}

func TestJobStatusHandler_NotFound(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/jobs/nonexistent-id")
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestVendorHandler_Create(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := "name=Test+Vendor&gst=&pan=&ifsc="
	req, err := http.NewRequest("POST", ts.URL+"/vendors", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Tenant-ID", "test-tenant")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, string(bodyBytes))
	}

	var vendorResp map[string]string
	json.NewDecoder(resp.Body).Decode(&vendorResp)

	if vendorResp["vendor_id"] == "" {
		t.Error("vendor_id is empty in response")
	}
	if vendorResp["status"] != "queued" {
		t.Errorf("status = %s, want queued", vendorResp["status"])
	}

	// Verify vendor stored in DB
	srv.db.mu.Lock()
	vendorCount := len(srv.db.vendors)
	srv.db.mu.Unlock()
	if vendorCount != 1 {
		t.Errorf("expected 1 vendor in DB, got %d", vendorCount)
	}
}

func TestVendorHandler_Create_MissingName(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := "name=&gst=&pan=&ifsc="
	req, err := http.NewRequest("POST", ts.URL+"/vendors", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Tenant-ID", "test-tenant")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestVendorHandler_Get(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a vendor
	vendorID := uuid.New().String()
	now := time.Now()
	vendor := &domain.Vendor{
		ID:        vendorID,
		TenantID:  "test-tenant",
		Name:      "Test Vendor Inc",
		RiskScore: 50,
		RiskTier:  domain.RiskTierMedium,
		Approved:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = srv.db.UpsertVendor(nil, vendor)

	req, err := http.NewRequest("GET", ts.URL+"/vendors/"+vendorID, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Tenant-ID", "test-tenant")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Get vendor: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vendorResp map[string]any
	json.NewDecoder(resp.Body).Decode(&vendorResp)

	if vendorResp["vendor_id"] != vendorID {
		t.Errorf("vendor_id = %v, want %s", vendorResp["vendor_id"], vendorID)
	}
	if vendorResp["name"] != "Test Vendor Inc" {
		t.Errorf("name = %v, want Test Vendor Inc", vendorResp["name"])
	}
	if vendorResp["approved"] != true {
		t.Errorf("approved = %v, want true", vendorResp["approved"])
	}
}

func TestVendorHandler_Get_NotFound(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/vendors/nonexistent-vendor")
	if err != nil {
		t.Fatalf("Get vendor: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHealthHandler(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Get health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var healthResp map[string]any
	json.NewDecoder(resp.Body).Decode(&healthResp)

	if healthResp["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", healthResp["status"])
	}
}

func TestHealthHandler_WrongMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/health", "text/plain", nil)
	if err != nil {
		t.Fatalf("Post health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSlackWebhook_Challenge(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload := map[string]string{
		"type":      "url_verification",
		"challenge": "test-challenge-value-123",
	}
	bodyBytes, _ := json.Marshal(payload)

	resp, err := ts.Client().Post(ts.URL+"/slack/webhook", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Post slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var challengeResp map[string]string
	json.NewDecoder(resp.Body).Decode(&challengeResp)

	if challengeResp["challenge"] != "test-challenge-value-123" {
		t.Errorf("challenge = %v, want test-challenge-value-123", challengeResp["challenge"])
	}
}

func TestSlackWebhook_BlockActions_Approve(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a job with AWAITING_HITL status
	jobID := uuid.New().String()
	now := time.Now()
	job := &domain.Job{
		ID:           jobID,
		TenantID:     "default",
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusAwaitingHITL,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = srv.db.UpsertJob(nil, job)

	// Pre-populate a HITL request
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-" + jobID,
		TenantID: "default",
		JobID:    jobID,
		Reason:   "Document requires approval",
		Status:   domain.HITLStatusPending,
		SentAt:   now,
	}
	_ = srv.db.UpsertHITLRequest(nil, hitlReq)

	// Configure mock Slack parser
	srv.slack.parseFunc = func(payload string) (*providers.SlackCallbackResult, error) {
		return &providers.SlackCallbackResult{
			ActionID: "approve:" + jobID,
			JobID:    jobID,
			UserID:   "U12345",
			Type:     "block_actions",
		}, nil
	}

	// Send block_actions payload
	slackPayload := map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": "U12345", "name": "testuser"},
		"actions": []map[string]string{
			{"action_id": "approve:" + jobID, "block_id": "b1", "value": "approve"},
		},
	}
	bodyBytes, _ := json.Marshal(slackPayload)

	resp, err := ts.Client().Post(ts.URL+"/slack/webhook", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Post slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	// Verify job status updated to COMPLETED
	updatedJob, err := srv.db.GetJob(nil, jobID, "default")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if updatedJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", updatedJob.Status, domain.JobStatusCompleted)
	}

	// Verify HITL request updated
	updatedHITL, err := srv.db.GetHITLRequest(nil, "hitl-"+jobID, "default")
	if err != nil {
		t.Fatalf("GetHITLRequest: %v", err)
	}
	if updatedHITL.Status != domain.HITLStatusApproved {
		t.Errorf("HITL status = %s, want APPROVED", updatedHITL.Status)
	}
	if updatedHITL.RespondedAt == nil {
		t.Error("HITL RespondedAt should be set")
	}

	// Verify audit event written
	events, err := srv.db.ListAuditEvents(nil, "default", "job", jobID, 10)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) < 1 {
		t.Fatal("expected at least 1 audit event")
	}
	// The last event should be the HITL decision
	lastEvent := events[len(events)-1]
	if lastEvent.Action != string(domain.HITLStatusApproved) {
		t.Errorf("audit event action = %s, want APPROVED", lastEvent.Action)
	}
	if lastEvent.NewState != "COMPLETED" {
		t.Errorf("audit event new_state = %s, want COMPLETED", lastEvent.NewState)
	}
	if lastEvent.Actor != "U12345" {
		t.Errorf("audit event actor = %s, want U12345", lastEvent.Actor)
	}
}

func TestSlackWebhook_BlockActions_Reject(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a job with AWAITING_HITL status
	jobID := uuid.New().String()
	now := time.Now()
	job := &domain.Job{
		ID:           jobID,
		TenantID:     "default",
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusAwaitingHITL,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = srv.db.UpsertJob(nil, job)

	_ = srv.db.UpsertHITLRequest(nil, &domain.HITLRequest{
		ID:       "hitl-" + jobID,
		TenantID: "default",
		JobID:    jobID,
		Status:   domain.HITLStatusPending,
		SentAt:   now,
	})

	srv.slack.parseFunc = func(payload string) (*providers.SlackCallbackResult, error) {
		return &providers.SlackCallbackResult{
			ActionID: "reject:" + jobID,
			UserID:   "U67890",
			Type:     "block_actions",
		}, nil
	}

	slackPayload := map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": "U67890", "name": "rejecter"},
		"actions": []map[string]string{
			{"action_id": "reject:" + jobID, "block_id": "b1", "value": "reject"},
		},
	}
	bodyBytes, _ := json.Marshal(slackPayload)

	resp, err := ts.Client().Post(ts.URL+"/slack/webhook", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Post slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	updatedJob, _ := srv.db.GetJob(nil, jobID, "default")
	if updatedJob.Status != domain.JobStatusFailed {
		t.Errorf("job status = %s, want %s", updatedJob.Status, domain.JobStatusFailed)
	}

	updatedHITL, _ := srv.db.GetHITLRequest(nil, "hitl-"+jobID, "default")
	if updatedHITL.Status != domain.HITLStatusRejected {
		t.Errorf("HITL status = %s, want REJECTED", updatedHITL.Status)
	}
}

func TestSlackWebhook_EventCallback(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload := map[string]any{
		"type": "event_callback",
		"event": map[string]any{
			"type":    "message",
			"text":    "Hello from Slack",
			"channel": "C12345",
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	resp, err := ts.Client().Post(ts.URL+"/slack/webhook", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Post slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestUploadHandler_MissingFile(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// POST without a file
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "test-tenant")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUploadHandler_WrongMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/upload")
	if err != nil {
		t.Fatalf("Get upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestVendorHandler_WrongMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/vendors/some-id", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSlackWebhook_BadJSON(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/slack/webhook", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatalf("Post slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
