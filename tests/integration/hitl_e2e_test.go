package integration

import (
	"bytes"
	"context"
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

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// Shared mock implementations for E2E test — shared between server and agent
// ---------------------------------------------------------------------------

// e2eMockDB is a shared in-memory DB used by both the agent and HTTP handlers.
type e2eMockDB struct {
	mu           sync.Mutex
	jobs         map[string]*domain.Job
	vendors      map[string]*domain.Vendor
	documents    map[string]*domain.Document
	hitlRequests map[string]*domain.HITLRequest
	auditEvents  []*domain.AuditEvent
}

func newE2EMockDB() *e2eMockDB {
	return &e2eMockDB{
		jobs:         make(map[string]*domain.Job),
		vendors:      make(map[string]*domain.Vendor),
		documents:    make(map[string]*domain.Document),
		hitlRequests: make(map[string]*domain.HITLRequest),
	}
}

func (m *e2eMockDB) Ping(_ context.Context) error { return nil }

func (m *e2eMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Preserve CreatedAt if the job already exists and new one has it zero
	if existing, ok := m.jobs[job.ID]; ok && job.CreatedAt.IsZero() {
		job.CreatedAt = existing.CreatedAt
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *e2eMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	return job, nil
}

func (m *e2eMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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

func (m *e2eMockDB) UpsertVendor(_ context.Context, vendor *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[vendor.ID] = vendor
	return nil
}

func (m *e2eMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor not found")
	}
	return v, nil
}

func (m *e2eMockDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
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

func (m *e2eMockDB) UpsertDocument(_ context.Context, doc *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[doc.ID] = doc
	return nil
}

func (m *e2eMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("document not found")
	}
	return d, nil
}

func (m *e2eMockDB) FindBySHA256(_ context.Context, tenantID, contentHash string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.documents {
		if d.TenantID == tenantID && d.ContentHash == contentHash {
			return d, nil
		}
	}
	return nil, nil
}

func (m *e2eMockDB) UpsertHITLRequest(_ context.Context, req *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlRequests[req.ID] = req
	return nil
}

func (m *e2eMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlRequests[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("HITL request not found")
	}
	return r, nil
}

func (m *e2eMockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
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

func (m *e2eMockDB) ListHITLRequests(_ context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error) {
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
	// Sort by sent_at descending.
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

func (m *e2eMockDB) AppendAuditEvent(_ context.Context, event *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, event)
	return nil
}

func (m *e2eMockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
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

func (m *e2eMockDB) GetRecentJobs(_ context.Context, tenantID string, limit int) ([]*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Job
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			result = append(result, j)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *e2eMockDB) GetRiskyVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
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

func (m *e2eMockDB) GetRecentCompliance(_ context.Context, tenantID string, limit int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

// e2eMockStorage implements StorageProvider.
type e2eMockStorage struct {
	mu   sync.Mutex
	urls map[string]string
}

func (m *e2eMockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	url := fmt.Sprintf("https://storage.local/%s/%s", container, key)
	m.urls[key] = url
	return url, nil
}

func (m *e2eMockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *e2eMockStorage) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *e2eMockStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

// e2eMockQueue implements QueueProvider + Ping.
type e2eMockQueue struct {
	mu       sync.Mutex
	messages []string
}

func (m *e2eMockQueue) Ping(_ context.Context) error { return nil }

func (m *e2eMockQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("msg-%d", len(m.messages))
	m.messages = append(m.messages, id)
	return id, nil
}

func (m *e2eMockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}

func (m *e2eMockQueue) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *e2eMockQueue) Poison(_ context.Context, _, _ string) error {
	return nil
}

// e2eOCR is an OCR provider that returns low confidence (triggers HITL).
type e2eOCR struct{}

func (e *e2eOCR) Extract(_ context.Context, _ string) (*providers.OCRResult, error) {
	return &providers.OCRResult{
		Text:       "Extracted invoice text with low confidence",
		Confidence: 0.45, // Below 0.85 threshold → triggers HITL
		Language:   "en",
		Provider:   "e2e-mock-ocr",
		KeyValues: map[string]string{
			"gst":  "22AAAAA0000A1Z5",
			"pan":  "AAAAA0000A",
			"ifsc": "HDFC0001234",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// E2E HITL test: upload → process → HITL → approve → complete
// ---------------------------------------------------------------------------

func TestHITLE2E_FullFlow(t *testing.T) {
	// Shared dependencies
	e2eDB := newE2EMockDB()
	e2eStorage := &e2eMockStorage{urls: make(map[string]string)}
	e2eQueue := &e2eMockQueue{}
	tracer := telemetry.NoopTracer{}
	validator := domain.NewIndiaValidator()

	// Create the document agent with OCR that returns low confidence
	docAgent := agents.NewDocumentAgent(e2eStorage, e2eQueue, e2eDB, &e2eOCR{}, validator, tracer)

	// Create HTTP server with handlers that share the same mock DB/storage/queue
	mux := http.NewServeMux()
	e2eSrv := &e2eServerDeps{
		db:    e2eDB,
		stor:  e2eStorage,
		q:     e2eQueue,
		agent: docAgent,
	}
	e2eSrv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// -----------------------------------------------------------------------
	// Step 1: Upload a PDF document via the HTTP endpoint
	// -----------------------------------------------------------------------
	content := fmt.Sprintf("%%PDF-1.4 HITL E2E test document %d", time.Now().UnixNano())

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "low-quality-invoice.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte(content))
	w.Close()

	req, err := http.NewRequest("POST", ts.URL+"/upload", &buf)
	if err != nil {
		t.Fatalf("Upload request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "hitl-e2e")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Upload returned %d, want 202; body=%s", resp.StatusCode, string(body))
	}

	var uploadResp struct {
		JobID   string `json:"job_id"`
		Status  string `json:"status"`
		BlobURL string `json:"blob_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		t.Fatalf("Decode upload response: %v", err)
	}
	t.Logf("Uploaded: job_id=%s, status=%s", uploadResp.JobID, uploadResp.Status)

	if uploadResp.JobID == "" {
		t.Fatal("upload response missing job_id")
	}

	// -----------------------------------------------------------------------
	// Step 2: Simulate worker — process the document via agent
	// -----------------------------------------------------------------------
	docJob := &agents.DocumentJob{
		TenantID: "hitl-e2e",
		JobID:    uploadResp.JobID,
		BlobURL:  uploadResp.BlobURL,
		FileName: "low-quality-invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := docAgent.ProcessDocument(context.Background(), docJob)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	t.Logf("Document processed: needs_hitl=%v, confidence=%v", result["needs_hitl"], result["confidence"])

	// -----------------------------------------------------------------------
	// Step 3: Poll for AWAITING_HITL status via HTTP
	// -----------------------------------------------------------------------
	var jobStatus string
	maxPolls := 10
	for i := 0; i < maxPolls; i++ {
		resp, err := ts.Client().Get(ts.URL + "/jobs/" + uploadResp.JobID)
		if err != nil {
			t.Fatalf("Get job status: %v", err)
		}

		var statusResp struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		json.NewDecoder(resp.Body).Decode(&statusResp)
		resp.Body.Close()

		jobStatus = statusResp.Status
		t.Logf("Poll %d: job status = %s", i+1, jobStatus)

		if jobStatus == "AWAITING_HITL" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if jobStatus != "AWAITING_HITL" {
		t.Fatalf("Job did not reach AWAITING_HITL (final status: %s)", jobStatus)
	}
	t.Log("✓ Job reached AWAITING_HITL status")

	// -----------------------------------------------------------------------
	// Step 4: Create HITL request (simulating worker behavior)
	// -----------------------------------------------------------------------
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-" + uploadResp.JobID,
		TenantID: "hitl-e2e",
		JobID:    uploadResp.JobID,
		Reason:   "Document low-quality-invoice.pdf requires approval | confidence=0.45",
		Status:   domain.HITLStatusPending,
		SentAt:   time.Now(),
	}
	if err := e2eDB.UpsertHITLRequest(context.Background(), hitlReq); err != nil {
		t.Fatalf("Failed to create HITL request: %v", err)
	}
	t.Log("✓ HITL request created for approval")

	// -----------------------------------------------------------------------
	// Step 5: Simulate Slack approve action
	// -----------------------------------------------------------------------
	slackPayload := map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": "U_E2E_TESTER", "name": "e2etester"},
		"actions": []map[string]string{
			{
				"action_id": "approve:" + uploadResp.JobID,
				"block_id":  "approval_" + uploadResp.JobID,
				"value":     "approve",
			},
		},
	}
	bodyBytes, _ := json.Marshal(slackPayload)

	resp, err = ts.Client().Post(ts.URL+"/slack/webhook", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Slack webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Slack webhook returned %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	t.Log("✓ Slack approve action sent")

	// -----------------------------------------------------------------------
	// Step 6: Verify job is now COMPLETED
	// -----------------------------------------------------------------------
	resp, err = ts.Client().Get(ts.URL + "/jobs/" + uploadResp.JobID)
	if err != nil {
		t.Fatalf("Get final job status: %v", err)
	}
	defer resp.Body.Close()

	var finalStatus struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&finalStatus)

	if finalStatus.Status != "COMPLETED" {
		t.Fatalf("Final job status = %s, want COMPLETED", finalStatus.Status)
	}
	t.Logf("✓ Job completed: id=%s, status=%s", finalStatus.ID, finalStatus.Status)

	// -----------------------------------------------------------------------
	// Step 7: Verify HITL request was approved
	// -----------------------------------------------------------------------
	hitlReq, err = e2eDB.GetHITLRequest(nil, "hitl-"+uploadResp.JobID, "hitl-e2e")
	if err != nil {
		t.Fatalf("GetHITLRequest failed: %v", err)
	}
	if hitlReq.Status != domain.HITLStatusApproved {
		t.Errorf("HITL request status = %s, want APPROVED", hitlReq.Status)
	}
	if hitlReq.RespondedAt == nil {
		t.Error("HITL request RespondedAt should be set")
	}
	t.Logf("✓ HITL request approved: id=%s", hitlReq.ID)

	// -----------------------------------------------------------------------
	// Step 8: Verify audit event was written
	// -----------------------------------------------------------------------
	events, err := e2eDB.ListAuditEvents(nil, "hitl-e2e", "job", uploadResp.JobID, 10)
	if err != nil {
		t.Fatalf("ListAuditEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("No audit events found for the job")
	}
	t.Logf("✓ Audit events recorded: %d events", len(events))
}

// ---------------------------------------------------------------------------
// E2E test server — mirrors handler_test.go but with real agent integration
// ---------------------------------------------------------------------------

type e2eServerDeps struct {
	db    *e2eMockDB
	stor  *e2eMockStorage
	q     *e2eMockQueue
	agent *agents.DocumentAgent
}

func (s *e2eServerDeps) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/upload", s.uploadHandler)
	mux.HandleFunc("/jobs/", s.jobStatusHandler)
	mux.HandleFunc("/slack/webhook", s.slackWebhookHandler)
}

func writeE2EJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeE2EError(w http.ResponseWriter, status int, message string) {
	writeE2EJSON(w, status, map[string]string{"error": message})
}

func (s *e2eServerDeps) uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeE2EError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeE2EError(w, http.StatusBadRequest, "Content-Type must be multipart/form-data")
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeE2EError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeE2EError(w, http.StatusBadRequest, "No file provided in 'file' field")
		return
	}
	defer file.Close()

	ext := ""
	if idx := strings.LastIndex(header.Filename, "."); idx >= 0 {
		ext = strings.ToLower(header.Filename[idx:])
	}
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeE2EError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

	contentHash, err := hashReader(file)
	if err != nil {
		writeE2EError(w, http.StatusInternalServerError, "Failed to compute file hash")
		return
	}
	_, _ = file.Seek(0, io.SeekStart)
	fileContent, _ := io.ReadAll(file)

	ctx := r.Context()

	existingDoc, _ := s.db.FindBySHA256(ctx, tenantID, contentHash)
	if existingDoc != nil {
		writeE2EJSON(w, http.StatusConflict, map[string]string{
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
		".pdf": "application/pdf", ".jpg": "image/jpeg",
		".jpeg": "image/jpeg", ".png": "image/png",
	}
	ct := contentTypeMap[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}

	blobURL, err := s.stor.Upload(ctx, "documents", blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		writeE2EError(w, http.StatusInternalServerError, "Failed to upload file")
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
			"filename": header.Filename, "blob_url": blobURL, "content_hash": contentHash,
		},
	}
	_ = s.db.UpsertJob(ctx, job)

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
	_, _ = s.q.Enqueue(ctx, "document-queue", map[string]string{"job_id": jobID})

	writeE2EJSON(w, http.StatusAccepted, map[string]string{
		"job_id":   jobID,
		"status":   "queued",
		"blob_url": blobURL,
		"message":  "Document uploaded successfully and queued for processing",
	})
}

func (s *e2eServerDeps) jobStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeE2EError(w, http.StatusMethodNotAllowed, "Method not allowed")
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
		writeE2EError(w, http.StatusBadRequest, "Missing job ID")
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	job, err := s.db.GetJob(r.Context(), jobID, tenantID)
	if err != nil {
		writeE2EError(w, http.StatusNotFound, "Job not found")
		return
	}
	writeE2EJSON(w, http.StatusOK, map[string]any{
		"id":     job.ID,
		"status": string(job.Status),
		"error":  job.Error,
	})
}

func (s *e2eServerDeps) slackWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeE2EError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	bodyBytes, _ := io.ReadAll(r.Body)
	body := string(bodyBytes)

	payloadBytes := bodyBytes
	if strings.HasPrefix(body, "payload=") {
		values, _ := parseQuery(body)
		if pf, ok := values["payload"]; ok && len(pf) > 0 {
			payloadBytes = []byte(pf[0])
		}
	}

	var payload struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge,omitempty"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		writeE2EError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if payload.Type == "url_verification" {
		writeE2EJSON(w, http.StatusOK, map[string]string{"challenge": payload.Challenge})
		return
	}

	if payload.Type == "block_actions" {
		// Parse the callback ourselves
		callback := payloadBody(body)
		var cb struct {
			Type string `json:"type"`
			User struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"user"`
			Actions []struct {
				ActionID string `json:"action_id"`
				BlockID  string `json:"block_id"`
				Value    string `json:"value"`
			} `json:"actions"`
		}
		if err := json.Unmarshal([]byte(callback), &cb); err != nil {
			writeE2EError(w, http.StatusBadRequest, "Invalid callback")
			return
		}

		if len(cb.Actions) == 0 {
			writeE2EError(w, http.StatusBadRequest, "No actions")
			return
		}

		actionID := cb.Actions[0].ActionID
		parts := strings.SplitN(actionID, ":", 2)
		if len(parts) != 2 {
			writeE2EError(w, http.StatusBadRequest, "Invalid action ID")
			return
		}
		action := parts[0]
		jobID := parts[1]
		userID := cb.User.ID

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
			writeE2EError(w, http.StatusBadRequest, "Unknown action")
			return
		}

		ctx := r.Context()
		job, err := s.db.GetJob(ctx, jobID, "default")
		if err != nil {
			writeE2EError(w, http.StatusNotFound, "Job not found")
			return
		}
		job.Status = newStatus
		job.UpdatedAt = time.Now()
		_ = s.db.UpsertJob(ctx, job)

		hitlReq, err := s.db.GetHITLRequest(ctx, "hitl-"+jobID, job.TenantID)
		if err == nil && hitlReq != nil {
			hitlReq.Status = hitlStatus
			t := time.Now()
			hitlReq.RespondedAt = &t
			_ = s.db.UpsertHITLRequest(ctx, hitlReq)
		}

		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   job.TenantID,
			Actor:      userID,
			Action:     string(hitlStatus),
			TargetType: "job",
			TargetID:   jobID,
			OldState:   string(domain.JobStatusAwaitingHITL),
			NewState:   string(newStatus),
			Timestamp:  time.Now(),
		})

		writeE2EJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeE2EJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

// Use hashContent to compute a quick SHA256 for E2E test validation
func hashContent(content string) string {
	h, _ := hashReader(bytes.NewReader([]byte(content)))
	return h
}

func hashReader(r io.Reader) (string, error) {
	return computeSHA256(r)
}
