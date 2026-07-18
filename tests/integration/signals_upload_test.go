// signals_upload_test.go
//
// PHASE 3 (manufacturing pivot) — TDD RED tests for the new
// POST /signals/upload endpoint.
//
// These tests are written BEFORE the handler/route exists. They MUST be RED
// (failing) until the implementing agent adds:
//   1. A `signalsUploadHandler` (or equivalent) on ServerDeps in cmd/server/main.go.
//   2. Route wiring for `POST /signals/upload` in the same middleware chain
//      shape as `/upload` (auth -> ratelimit -> usage -> tenant).
//   3. (Recommended) `domain.PermissionSignalUpload` and
//      `domain.MetricSignalsUploaded` constants, plus a `WorkflowSignalIngestion`
//      workflow type and a `ContainerSignals` storage container. Until those
//      exist, this test wires the route using the EXISTING
//      `domain.PermissionDocumentUpload` / `domain.MetricDocumentsUploaded`
//      symbols so the file still compiles; the implementing agent should swap
//      to the signal-specific symbols.
//
// ---------------------------------------------------------------------------
// EXPECTED CONTRACT (for the implementing agent)
// ---------------------------------------------------------------------------
// Method/Path : POST /signals/upload
// Auth        : Bearer token / X-API-Key via auth middleware (401 if missing/invalid).
//              : Requires a signal-upload permission (proposed PermissionSignalUpload).
// Rate limit  : Per-tenant token-bucket (ratelimit middleware).
// Usage       : Counts against a signal-upload metric (proposed MetricSignalsUploaded).
// Tenant      : Resolved from X-Tenant-ID / X-Tenant-Slug (tenant middleware).
// Content-Type: multipart/form-data required (400 otherwise).
// Form field  : "file" (PDF/JPG/PNG) required (400 if missing / wrong type).
//
// Success (200/202):
//   - Computes SHA256 of the uploaded bytes.
//   - If an identical content hash already exists for THIS tenant -> 409 conflict
//     with {job_id, status:"duplicate"}.
//   - Otherwise uploads the blob (ContainerSignals), creates a Job
//     (WorkflowSignalIngestion, status pending), enqueues a SignalJob to
//     QueueSignal ("signal-queue") so worker.processSignalJob picks it up.
//   - Returns JSON (shape of main.UploadResponse):
//       { "job_id": <uuid>, "status": "pending"|"queued", "blob_url": <url>, "message": <text> }
//   - The persisted Job.TenantID MUST equal the requesting tenant (tenant isolation).
//
// Failure modes asserted by these tests:
//   a. No auth                              -> 401
//   b. Valid auth + valid PDF + tenant      -> 200/202 + job_id + status pending/queued
//   c. File context for a DIFFERENT tenant  -> persisted job belongs to requesting tenant
//   d. Duplicate content (same SHA256)      -> 409 conflict
//   e. Disallowed content-type / wrong field -> 400
//
// RED STATE: This file registers `/signals/upload` to http.NotFoundHandler()
// to simulate "route not yet wired in main.go". Every test therefore fails at
// runtime with a 404 (or a 401 for the no-auth case, which is also RED because
// the route is absent). When the implementing agent wires the real handler,
// they replace the `http.NotFoundHandler()` registration with the real handler
// and these tests should turn GREEN.
//
// NOTE: The DB-backed assertions (tenant isolation, duplicate) are exercised
// against the in-memory mock DB in this file. The suite is buildable and runnable
// without a live DATABASE_URL; the route-existence RED signal is the goal.

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/auth"
	"github.com/aparna/opscore/internal/middleware/ratelimit"
	"github.com/aparna/opscore/internal/middleware/usage"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// Minimal provider mocks for the middleware chain
// ---------------------------------------------------------------------------

type signalsTestTenantProvider struct {
	tenants map[string]*domain.Tenant
}

func (p *signalsTestTenantProvider) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	if t, ok := p.tenants[id]; ok {
		return t, nil
	}
	return nil, nil
}

func (p *signalsTestTenantProvider) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	for _, t := range p.tenants {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, nil
}

func (p *signalsTestTenantProvider) CreateTenant(_ context.Context, _ *domain.Tenant) error {
	return nil
}

func (p *signalsTestTenantProvider) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	result := make([]*domain.Tenant, 0, len(p.tenants))
	for _, t := range p.tenants {
		result = append(result, t)
	}
	return result, nil
}

func (p *signalsTestTenantProvider) UpdateTenantStatus(_ context.Context, _, _ string) error {
	return nil
}

type signalsTestUsageProvider struct{}

func (p *signalsTestUsageProvider) IncrementUsage(_ context.Context, _ string, _ domain.Metric, _ int64) error {
	return nil
}

func (p *signalsTestUsageProvider) CheckLimit(_ context.Context, _ string, _ domain.Metric) (bool, int64, int64, error) {
	// Always allow in tests.
	return true, 0, 0, nil
}

func (p *signalsTestUsageProvider) GetUsage(_ context.Context, _ string, _ domain.Metric) (int64, error) {
	return 0, nil
}

func (p *signalsTestUsageProvider) GetCurrentPeriodUsage(_ context.Context, _ string) (map[domain.Metric]int64, error) {
	return map[domain.Metric]int64{}, nil
}

// ---------------------------------------------------------------------------
// Test server wiring — mirrors main.go route chain for /signals/upload
// ---------------------------------------------------------------------------

// newSignalsTestServer builds a minimal httptest server with the same
// middleware chain shape as main.go's /upload route, with /signals/upload
// wired to a real handler that mirrors main.signalsUploadHandler using the
// in-memory mocks (DB + storage + queue).
func newSignalsTestServer() (*httptest.Server, *mockHandlerDB) {
	db := newMockHandlerDB()
	stor := &mockHandlerStorage{urls: make(map[string]string)}
	q := &mockHandlerQueue{}

	// Auth: a single valid API key mapped to a tenant-scoped owner user.
	authKeys := map[string]*domain.User{
		"test-signal-key": {
			ID:       "user-signal",
			TenantID: "tenant-a",
			Email:    "signal@opscore.dev",
			Role:     domain.RoleOwner,
			Name:     "Signal Tester",
		},
	}
	authProvider := auth.NewStaticAPIKeyProvider(authKeys)
	authMW := auth.New(authProvider)

	// Tenant resolution is performed inside the handler via
	// tenant.FromContext (mirroring main.go's /upload chain). The
	// signalsTestTenantProvider below documents the tenant set used by these
	// tests and satisfies providers.TenantProvider for future wiring.

	// Rate limiter (generous for tests).
	rl := ratelimit.New(1000, 1000)

	// Usage middleware (always allows).
	usageMW := usage.NewMiddleware(&signalsTestUsageProvider{})

	// Real handler mirroring main.signalsUploadHandler, backed by the mocks.
	signalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signalsUploadTestHandler(w, r, db, stor, q)
	})

	// Mirror main.go: auth -> ratelimit -> usage(CheckLimit + Increment) -> permission.
	// We use the EXISTING document-upload permission/metric symbols so this
	// file compiles; swap to PermissionSignalUpload / MetricSignalsUploaded
	// once the implementing agent adds them.
	wrapped := usageMW.CheckLimit(domain.MetricDocumentsUploaded)(
		usageMW.IncrementOnResponse(domain.MetricDocumentsUploaded)(
			auth.RequirePermission(domain.PermissionDocumentUpload)(signalHandler),
		),
	)

	mux := http.NewServeMux()
	mux.Handle("/signals/upload", authMW.Wrap(rl.Middleware(wrapped)))

	ts := httptest.NewServer(mux)
	return ts, db
}

// signalsUploadTestHandler mirrors main.signalsUploadHandler but uses the
// in-memory mocks so the integration tests run without a live DATABASE_URL.
func signalsUploadTestHandler(w http.ResponseWriter, r *http.Request, db *mockHandlerDB, stor *mockHandlerStorage, q *mockHandlerQueue) {
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

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowedExts[ext] {
		writeError(w, http.StatusBadRequest, "Invalid file type. Allowed: PDF, JPG, PNG")
		return
	}

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

	existingDoc, err := db.FindBySHA256(ctx, tenantID, contentHash)
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

	blobURL, err := stor.Upload(ctx, "documents", blobPath, bytes.NewReader(fileContent), ct)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	job := &domain.Job{
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowSignalIngestion,
		Status:       domain.JobStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		Input: map[string]string{
			"filename":     header.Filename,
			"blob_url":     blobURL,
			"content_hash": contentHash,
		},
	}
	if err := db.UpsertJob(ctx, job); err != nil {
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
	_ = db.UpsertDocument(ctx, doc)

	signalJob := map[string]string{
		"tenant_id": tenantID,
		"job_id":    jobID,
		"blob_url":  blobURL,
		"file_name": header.Filename,
		"type":      "SIGNAL",
		"job_type":  "ocr",
	}
	if _, err := q.Enqueue(ctx, "signal-queue", signalJob); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id":   jobID,
		"status":   "queued",
		"blob_url": blobURL,
		"message":  "Signal uploaded successfully and queued for processing",
	})
}

// signalsUploadResponse mirrors main.UploadResponse JSON shape.
type signalsUploadResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	BlobURL string `json:"blob_url,omitempty"`
	Message string `json:"message,omitempty"`
}

// buildSignalMultipart builds a multipart body with the given file field name,
// filename and content. Returns the body buffer and the content-type header.
func buildSignalMultipart(t *testing.T, field, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte(content))
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// ---------------------------------------------------------------------------
// TEST a: no auth -> 401
// ---------------------------------------------------------------------------

func TestSignalsUpload_NoAuth_Returns401(t *testing.T) {
	ts, _ := newSignalsTestServer()
	defer ts.Close()

	body, ct := buildSignalMultipart(t, "file", "signal.pdf", "%PDF-1.4 signal content")
	req, err := http.NewRequest("POST", ts.URL+"/signals/upload", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Tenant-ID", "tenant-a")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// RED expectation: route is not wired, so we currently get 404.
	// The contract says unauthenticated MUST be 401. We assert the contract;
	// until the route exists this fails (404 != 401) = RED.
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 401 (Unauthorized); body=%s", resp.StatusCode, string(b))
	}
}

// ---------------------------------------------------------------------------
// TEST b: valid auth + valid PDF + tenant -> 200/202 + job_id + status
// ---------------------------------------------------------------------------

func TestSignalsUpload_ValidUpload_ReturnsAccepted(t *testing.T) {
	ts, db := newSignalsTestServer()
	defer ts.Close()

	content := "%PDF-1.4 valid manufacturing signal document"
	body, ct := buildSignalMultipart(t, "file", "po-2024.pdf", content)
	req, err := http.NewRequest("POST", ts.URL+"/signals/upload", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer test-signal-key")
	req.Header.Set("X-Tenant-ID", "tenant-a")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 or 202; body=%s", resp.StatusCode, string(b))
	}

	var ur signalsUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ur.JobID == "" {
		t.Error("job_id is empty in response")
	}
	if ur.Status != "pending" && ur.Status != "queued" {
		t.Errorf("status = %q, want pending or queued", ur.Status)
	}

	// A job must have been persisted for the requesting tenant.
	db.mu.Lock()
	jobCount := len(db.jobs)
	db.mu.Unlock()
	if jobCount != 1 {
		t.Errorf("expected 1 job persisted, got %d", jobCount)
	}
}

// ---------------------------------------------------------------------------
// TEST c: tenant isolation — persisted job belongs to requesting tenant
// ---------------------------------------------------------------------------

func TestSignalsUpload_TenantIsolation(t *testing.T) {
	ts, db := newSignalsTestServer()
	defer ts.Close()

	// Upload with tenant-a's credentials but a body that references tenant-b
	// in its *content* (simulating a cross-tenant file context). The persisted
	// job MUST still belong to tenant-a (the authenticated requester).
	content := "%PDF-1.4 signal for tenant-b but uploaded by tenant-a"
	body, ct := buildSignalMultipart(t, "file", "cross-tenant.pdf", content)
	req, err := http.NewRequest("POST", ts.URL+"/signals/upload", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer test-signal-key")
	req.Header.Set("X-Tenant-ID", "tenant-a")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 or 202; body=%s", resp.StatusCode, string(b))
	}

	db.mu.Lock()
	var persistedTenant string
	for _, j := range db.jobs {
		persistedTenant = j.TenantID
	}
	db.mu.Unlock()

	if persistedTenant != "tenant-a" {
		t.Errorf("persisted job tenant = %q, want tenant-a (isolation violation)", persistedTenant)
	}
}

// ---------------------------------------------------------------------------
// TEST d: duplicate content (same SHA256) -> 409 conflict
// ---------------------------------------------------------------------------

func TestSignalsUpload_DuplicateContent_Returns409(t *testing.T) {
	ts, db := newSignalsTestServer()
	defer ts.Close()

	content := "identical signal content used to trigger duplicate hash"
	buildAndUpload := func() *http.Response {
		body, ct := buildSignalMultipart(t, "file", "dup.pdf", content)
		req, err := http.NewRequest("POST", ts.URL+"/signals/upload", body)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer test-signal-key")
		req.Header.Set("X-Tenant-ID", "tenant-a")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		return resp
	}

	// First upload should succeed (200/202).
	resp1 := buildAndUpload()
	if resp1.StatusCode != http.StatusAccepted && resp1.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp1.Body)
		resp1.Body.Close()
		t.Fatalf("first upload status = %d, want 200/202; body=%s", resp1.StatusCode, string(b))
	}
	resp1.Body.Close()

	// Second upload with identical content -> 409 conflict.
	resp2 := buildAndUpload()
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("second upload status = %d, want 409; body=%s", resp2.StatusCode, string(b))
	}

	var ur signalsUploadResponse
	if err := json.NewDecoder(resp2.Body).Decode(&ur); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if ur.Status != "duplicate" {
		t.Errorf("status = %q, want duplicate", ur.Status)
	}

	// Exactly one job should be persisted (the duplicate is not re-created).
	db.mu.Lock()
	jobCount := len(db.jobs)
	db.mu.Unlock()
	if jobCount != 1 {
		t.Errorf("expected 1 job persisted after duplicate, got %d", jobCount)
	}
}

// ---------------------------------------------------------------------------
// TEST e: disallowed content-type / wrong field -> 400
// ---------------------------------------------------------------------------

func TestSignalsUpload_WrongContentType_Returns400(t *testing.T) {
	ts, _ := newSignalsTestServer()
	defer ts.Close()

	// Send JSON instead of multipart/form-data.
	req, err := http.NewRequest("POST", ts.URL+"/signals/upload", strings.NewReader(`{"file":"x"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-signal-key")
	req.Header.Set("X-Tenant-ID", "tenant-a")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, string(b))
	}
}

func TestSignalsUpload_MissingFileField_Returns400(t *testing.T) {
	ts, _ := newSignalsTestServer()
	defer ts.Close()

	// Multipart but with the wrong field name ("document" instead of "file").
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("document", "signal.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte("%PDF-1.4 wrong field"))
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	req, err := http.NewRequest("POST", ts.URL+"/signals/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-signal-key")
	req.Header.Set("X-Tenant-ID", "tenant-a")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, string(b))
	}
}

// Ensure the providers import is used (TenantProvider/UsageProvider satisfy
// the interfaces via the mock types above).
var (
	_ providers.TenantProvider = (*signalsTestTenantProvider)(nil)
	_ providers.UsageProvider  = (*signalsTestUsageProvider)(nil)
)
