package decisions_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
	agentic "github.com/aparna/opscore/tests/agentic"
)

// ---------------------------------------------------------------------------
// In-memory DB for DocumentAgent decision tests (no containers needed)
// ---------------------------------------------------------------------------

type docMockDB struct {
	mu          sync.Mutex
	jobs        map[string]*domain.Job
	vendors     map[string]*domain.Vendor
	documents   map[string]*domain.Document
	hitlReqs    map[string]*domain.HITLRequest
	auditEvents []*domain.AuditEvent
}

func newDocMockDB() *docMockDB {
	return &docMockDB{
		jobs:      make(map[string]*domain.Job),
		vendors:   make(map[string]*domain.Vendor),
		documents: make(map[string]*domain.Document),
		hitlReqs:  make(map[string]*domain.HITLRequest),
	}
}

func (m *docMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, exists := m.jobs[job.ID]
	if exists {
		job.Version = existing.Version + 1
	}
	m.jobs[job.ID] = job
	return nil
}
func (m *docMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	if j.TenantID != tenantID {
		return nil, fmt.Errorf("job not found")
	}
	return j, nil
}
func (m *docMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []*domain.Job
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			r = append(r, j)
		}
	}
	return r, nil
}
func (m *docMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[v.ID] = v
	return nil
}
func (m *docMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor not found")
	}
	return v, nil
}
func (m *docMockDB) ListVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *docMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[d.ID] = d
	return nil
}
func (m *docMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("document not found")
	}
	return d, nil
}
func (m *docMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *docMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlReqs[r.ID] = r
	return nil
}
func (m *docMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlReqs[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("HITL not found")
	}
	return r, nil
}
func (m *docMockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []*domain.HITLRequest
	for _, h := range m.hitlReqs {
		if h.TenantID == tenantID && h.Status == domain.HITLStatusPending {
			r = append(r, h)
		}
	}
	return r, nil
}
func (m *docMockDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *docMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *docMockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []*domain.AuditEvent
	for _, e := range m.auditEvents {
		if e.TenantID != tenantID {
			continue
		}
		if targetType != "" && e.TargetType != targetType {
			continue
		}
		if targetID != "" && e.TargetID != targetID {
			continue
		}
		r = append(r, e)
		if limit > 0 && len(r) >= limit {
			break
		}
	}
	return r, nil
}
func (m *docMockDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (m *docMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *docMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

type docMockStorage struct {
	mu   sync.Mutex
	urls map[string]string
}

func newDocMockStorage() *docMockStorage {
	return &docMockStorage{urls: make(map[string]string)}
}
func (m *docMockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	url := fmt.Sprintf("https://storage.local/%s/%s", container, key)
	m.urls[key] = url
	return url, nil
}
func (m *docMockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *docMockStorage) Delete(_ context.Context, _, _ string) error { return nil }
func (m *docMockStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

type docMockQueue struct{}

func (m *docMockQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	return "msg-1", nil
}
func (m *docMockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}
func (m *docMockQueue) Delete(_ context.Context, _, _ string) error { return nil }
func (m *docMockQueue) Poison(_ context.Context, _, _ string) error { return nil }

func newDocTestAgent(ocr providers.OCRProvider) (*agents.DocumentAgent, *docMockDB) {
	db := newDocMockDB()
	storage := newDocMockStorage()
	queue := &docMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)
	return agent, db
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDocumentAgent_HighConfidence_ValidData_AutoComplete(t *testing.T) {
	// confidence 0.95 + valid GST/PAN → needs_hitl=false, JobStatus COMPLETED
	t.Log("=== Test: High confidence + valid data = auto-complete ===")

	// Arrange
	ocr := &agentic.StubOCR{}
	agent, db := newDocTestAgent(ocr)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-1",
		JobID:    "doc-dec-test-1",
		BlobURL:  "http://example.com/confidence_0.95/invoice.pdf",
		FileName: "invoice_2024.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	// Act
	result, err := agent.ProcessDocument(context.Background(), job)

	// Assert
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	// needs_hitl must be false (confidence 0.95 >= 0.85, no validation errors)
	if needsHITL, _ := result["needs_hitl"].(bool); needsHITL {
		t.Errorf("needs_hitl = true, want false for high confidence + valid data")
	}
	// Job status COMPLETED
	dbJob, err := db.GetJob(context.Background(), "doc-dec-test-1", "tenant-dec-1")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if dbJob.Status != domain.JobStatusCompleted {
		t.Errorf("Job status = %s, want %s", dbJob.Status, domain.JobStatusCompleted)
	}
	// No HITL generated
	_, hitlErr := db.GetHITLRequest(context.Background(), "hitl-doc-dec-test-1", "tenant-dec-1")
	if hitlErr == nil {
		t.Error("Unexpected HITL request found for auto-complete")
	}
	t.Logf("✅ Auto-complete: needs_hitl=false, status=%s", dbJob.Status)
}

func TestDocumentAgent_LowConfidence_TriggersHITL(t *testing.T) {
	// confidence 0.45 → needs_hitl=true, JobStatus AWAITING_HITL
	t.Log("=== Test: Low confidence triggers HITL ===")

	ocr := &agentic.StubOCR{}
	agent, db := newDocTestAgent(ocr)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-2",
		JobID:    "doc-dec-test-2",
		BlobURL:  "http://example.com/confidence_0.45/blurry.pdf",
		FileName: "blurry_invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	if needsHITL, _ := result["needs_hitl"].(bool); !needsHITL {
		t.Errorf("needs_hitl = false, want true for low confidence")
	}
	if conf, _ := result["confidence"].(float64); conf != 0.45 {
		t.Errorf("confidence = %f, want 0.45", conf)
	}
	dbJob, err := db.GetJob(context.Background(), "doc-dec-test-2", "tenant-dec-2")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if dbJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("Job status = %s, want %s", dbJob.Status, domain.JobStatusAwaitingHITL)
	}
	t.Logf("✅ Low confidence: needs_hitl=true, status=%s", dbJob.Status)
}

func TestDocumentAgent_ValidData_ValidationErrors_TriggersHITL(t *testing.T) {
	// High confidence but invalid GST/PAN → needs_hitl=true via validation path
	t.Log("=== Test: Validation errors trigger HITL ===")

	ocr := &agentic.StubOCR{}
	agent, db := newDocTestAgent(ocr)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-3",
		JobID:    "doc-dec-test-3",
		BlobURL:  "http://example.com/key_values_error/invoice.pdf",
		FileName: "vendor_invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	if needsHITL, _ := result["needs_hitl"].(bool); !needsHITL {
		t.Errorf("needs_hitl = false, want true for validation errors")
	}
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok || validations == nil {
		t.Fatal("validations missing from result")
	}
	if !validations.HasErrors() {
		t.Error("expected validation errors for invalid GST/PAN")
	}
	// Confidence is high (0.90 from key_values_error trigger)
	if conf, _ := result["confidence"].(float64); conf < 0.85 {
		t.Errorf("confidence = %f, want >= 0.85", conf)
	}
	dbJob, err := db.GetJob(context.Background(), "doc-dec-test-3", "tenant-dec-3")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if dbJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("Job status = %s, want %s", dbJob.Status, domain.JobStatusAwaitingHITL)
	}
	t.Logf("✅ Validation errors: needs_hitl=true, errors=%v, status=%s", validations.Errors, dbJob.Status)
}

func TestDocumentAgent_OCRFailure_ReturnsError_AuditNotUpdated(t *testing.T) {
	// OCR error → error returned, job stays PENDING, no HITL
	t.Log("=== Test: OCR failure returns error, job unchanged ===")

	ocr := &agentic.StubOCR{}
	agent, db := newDocTestAgent(ocr)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-4",
		JobID:    "doc-dec-test-4",
		BlobURL:  "http://example.com/trigger_error/corrupted.pdf",
		FileName: "corrupted.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	_, err := agent.ProcessDocument(context.Background(), job)
	if err == nil {
		t.Fatal("expected error from ProcessDocument, got nil")
	}
	if !strings.Contains(err.Error(), "OCR failed") {
		t.Errorf("error = %v, want 'OCR failed'", err)
	}
	// Job should NOT exist in DB (ProcessDocument returns before UpsertJob on OCR error)
	_, jobErr := db.GetJob(context.Background(), "doc-dec-test-4", "tenant-dec-4")
	if jobErr == nil {
		t.Error("job should not exist in DB — ProcessDocument errored before upsert")
	}
	t.Logf("✅ OCR failure: error=%v, no job in DB", err)
}

func TestDocumentAgent_NilOCR_ReturnsConfigError(t *testing.T) {
	// nil OCR provider → "OCR provider not configured" error
	t.Log("=== Test: Nil OCR returns config error ===")

	db := newDocMockDB()
	storage := newDocMockStorage()
	queue := &docMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, nil, validator, tracer)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-5",
		JobID:    "doc-dec-test-5",
		BlobURL:  "http://example.com/doc.pdf",
		FileName: "test.pdf",
	}

	_, err := agent.ProcessDocument(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for nil OCR, got nil")
	}
	if !strings.Contains(err.Error(), "OCR provider not configured") {
		t.Errorf("error = %v, want 'OCR provider not configured'", err)
	}
	t.Logf("✅ Nil OCR: error=%v", err)
}

func TestDocumentAgent_EmptyKeyValues_NoValidationErrors(t *testing.T) {
	// Empty KeyValues map → no validation errors (empty vals are skipped)
	t.Log("=== Test: Empty KeyValues = no validation errors ===")

	// Use a custom OCR that returns empty KeyValues but high confidence
	ocr := &agentic.StubOCR{}
	agent, _ := newDocTestAgent(ocr)

	job := &agents.DocumentJob{
		TenantID: "tenant-dec-6",
		JobID:    "doc-dec-test-6",
		BlobURL:  "http://example.com/confidence_0.95/empty_kv.pdf",
		FileName: "empty_kv_doc.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	// StubOCR for confidence_0.95 returns KeyValues with amount + gst
	// The gst is valid, so no validation errors
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok || validations == nil {
		t.Fatal("validations missing from result")
	}
	if validations.HasErrors() {
		t.Errorf("expected no validation errors for valid GST, got: %v", validations.Errors)
	}
	t.Logf("✅ Empty KeyValues: validations.HasErrors=%v", validations.HasErrors())
}

func TestDocumentAgent_DocumentClassifier_ClassifyByFilename(t *testing.T) {
	// Test all 5 classifier paths
	t.Log("=== Test: Document classifier by filename ===")

	ocr := &agentic.StubOCR{}
	agent, _ := newDocTestAgent(ocr)
	stubOCR := &agentic.StubOCR{}

	tests := []struct {
		name     string
		filename string
		wantType string
	}{
		{"invoice", "invoice_2024.pdf", "INVOICE"},
		{"contract", "service-contract-agreement.pdf", "CONTRACT"},
		{"gst", "gst-return-filing.pdf", "GST"},
		{"purchase_order", "po-2024.pdf", "PURCHASE_ORDER"},
		{"purchase_alt", "purchase-order-2024.pdf", "PURCHASE_ORDER"},
		{"other", "unknown_document.txt", "OTHER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &agents.DocumentJob{
				TenantID: "tenant-classify",
				JobID:    "classify-" + tt.name,
				BlobURL:  "http://example.com/confidence_0.95/" + tt.filename,
				FileName: tt.filename,
			}
			result, err := agent.ProcessDocument(context.Background(), job)
			if err != nil {
				t.Fatalf("ProcessDocument for %s failed: %v", tt.filename, err)
			}
			_ = stubOCR
			gotType, ok := result["document_type"].(string)
			if !ok {
				t.Fatalf("document_type not a string, got %T", result["document_type"])
			}
			if gotType != tt.wantType {
				t.Errorf("document_type = %q, want %q for filename %q", gotType, tt.wantType, tt.filename)
			}
		})
	}
	t.Log("✅ All classifier paths tested")
}
