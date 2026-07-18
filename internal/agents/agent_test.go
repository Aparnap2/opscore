package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// Mock implementations of provider interfaces
// ---------------------------------------------------------------------------

// mockDB implements providers.DBProvider with in-memory maps.
type mockDB struct {
	mu           sync.Mutex
	jobs         map[string]*domain.Job
	vendors      map[string]*domain.Vendor
	documents    map[string]*domain.Document
	hitlRequests map[string]*domain.HITLRequest
	auditEvents  []*domain.AuditEvent
}

func newMockDB() *mockDB {
	return &mockDB{
		jobs:         make(map[string]*domain.Job),
		vendors:      make(map[string]*domain.Vendor),
		documents:    make(map[string]*domain.Document),
		hitlRequests: make(map[string]*domain.HITLRequest),
	}
}

func (m *mockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, exists := m.jobs[job.ID]
	if exists {
		// Simulate version tracking
		job.Version = existing.Version + 1
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *mockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	if job.TenantID != tenantID {
		return nil, fmt.Errorf("job not found")
	}
	return job, nil
}

func (m *mockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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

func (m *mockDB) UpsertVendor(_ context.Context, vendor *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[vendor.ID] = vendor
	return nil
}

func (m *mockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor not found")
	}
	return v, nil
}

func (m *mockDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
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

func (m *mockDB) UpsertDocument(_ context.Context, doc *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[doc.ID] = doc
	return nil
}

func (m *mockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("document not found")
	}
	return d, nil
}

func (m *mockDB) FindBySHA256(_ context.Context, tenantID, contentHash string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.documents {
		if d.TenantID == tenantID && d.ContentHash == contentHash {
			return d, nil
		}
	}
	return nil, nil
}

func (m *mockDB) UpsertHITLRequest(_ context.Context, req *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlRequests[req.ID] = req
	return nil
}

func (m *mockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlRequests[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("HITL request not found")
	}
	return r, nil
}

func (m *mockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
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

func (m *mockDB) ListHITLRequests(_ context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error) {
	return nil, nil
}

func (m *mockDB) AppendAuditEvent(_ context.Context, event *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, event)
	return nil
}

func (m *mockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
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

func (m *mockDB) GetRecentJobs(_ context.Context, tenantID string, limit int) ([]*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Job
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			result = append(result, j)
		}
	}
	// Return up to limit jobs (no ordering in mock)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockDB) GetRiskyVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Vendor
	for _, v := range m.vendors {
		if v.TenantID == tenantID {
			// Mock: consider vendors with risk < 30 or trust tier PROBATION/BLOCKED as risky
			if v.RiskScore < 30 || v.TrustBattery.Tier == domain.TrustTierProbation || v.TrustBattery.Tier == domain.TrustTierBlocked {
				result = append(result, v)
			}
		}
	}
	return result, nil
}

func (m *mockDB) GetRecentCompliance(_ context.Context, tenantID string, limit int) ([]*domain.ComplianceRecord, error) {
	return nil, nil // Not needed for mock tests
}

// --- Manufacturing pivot (Phase 1.4A) mock stubs ---------------------------

func (m *mockDB) UpsertPurchaseOrder(_ context.Context, _ *domain.PurchaseOrder) error {
	return nil
}
func (m *mockDB) GetPurchaseOrderByID(_ context.Context, _, _ string) (*domain.PurchaseOrder, error) {
	return nil, nil
}
func (m *mockDB) ListPurchaseOrders(_ context.Context, _ string, _, _ int) ([]*domain.PurchaseOrder, error) {
	return nil, nil
}
func (m *mockDB) UpsertGoodsReceipt(_ context.Context, _ *domain.GoodsReceipt) error {
	return nil
}
func (m *mockDB) GetGoodsReceiptByID(_ context.Context, _, _ string) (*domain.GoodsReceipt, error) {
	return nil, nil
}
func (m *mockDB) ListGoodsReceipts(_ context.Context, _, _ string, _, _ int) ([]*domain.GoodsReceipt, error) {
	return nil, nil
}
func (m *mockDB) UpsertInvoice(_ context.Context, _ *domain.Invoice) error {
	return nil
}
func (m *mockDB) GetInvoiceByID(_ context.Context, _, _ string) (*domain.Invoice, error) {
	return nil, nil
}
func (m *mockDB) ListInvoices(_ context.Context, _, _ string, _, _ int) ([]*domain.Invoice, error) {
	return nil, nil
}
func (m *mockDB) UpsertExceptionCase(_ context.Context, _ *domain.ExceptionCase) error {
	return nil
}
func (m *mockDB) GetExceptionCaseByID(_ context.Context, _, _ string) (*domain.ExceptionCase, error) {
	return nil, nil
}
func (m *mockDB) ListExceptionCases(_ context.Context, _, _, _ string, _, _ int) ([]*domain.ExceptionCase, error) {
	return nil, nil
}
func (m *mockDB) UpdateExceptionCaseStatus(_ context.Context, _, _, _ string) error {
	return nil
}

// mockStorage implements providers.StorageProvider.
type mockStorage struct {
	mu   sync.Mutex
	urls map[string]string
}

func newMockStorage() *mockStorage {
	return &mockStorage{urls: make(map[string]string)}
}

func (m *mockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	url := fmt.Sprintf("https://storage.local/%s/%s", container, key)
	m.urls[key] = url
	return url, nil
}

func (m *mockStorage) Download(_ context.Context, container, key string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockStorage) Delete(_ context.Context, container, key string) error {
	return nil
}

func (m *mockStorage) List(_ context.Context, container, prefix string) ([]providers.BlobItem, error) {
	return nil, nil
}

// mockQueue implements providers.QueueProvider.
type mockQueue struct {
	mu       sync.Mutex
	messages []string
}

func newMockQueue() *mockQueue {
	return &mockQueue{}
}

func (m *mockQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("msg-%d", len(m.messages))
	m.messages = append(m.messages, id)
	return id, nil
}

func (m *mockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}

func (m *mockQueue) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockQueue) Poison(_ context.Context, _, _ string) error {
	return nil
}

// mockOCR implements providers.OCRProvider with configurable result/error.
type mockOCR struct {
	mu         sync.Mutex
	confidence float64
	keyValues  map[string]string
	extractErr error
}

func newMockOCR(confidence float64, keyValues map[string]string) *mockOCR {
	return &mockOCR{
		confidence: confidence,
		keyValues:  keyValues,
	}
}

func (m *mockOCR) Extract(_ context.Context, _ string) (*providers.OCRResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.extractErr != nil {
		return nil, m.extractErr
	}
	return &providers.OCRResult{
		Text:       "Extracted text from document",
		Confidence: m.confidence,
		Language:   "en",
		Provider:   "mock-ocr",
		KeyValues:  m.keyValues,
	}, nil
}

// mockLLM implements providers.LLMProvider.
type mockLLM struct {
	reasonFunc func(ctx context.Context, prompt string) (string, error)
}

func newMockLLM() *mockLLM {
	return &mockLLM{
		reasonFunc: func(_ context.Context, prompt string) (string, error) {
			return "mock analysis: vendor document looks valid", nil
		},
	}
}

func (m *mockLLM) ExtractFields(_ context.Context, _ string, _ any) (json.RawMessage, float64, error) {
	return json.RawMessage("{}"), 1.0, nil
}

func (m *mockLLM) Reason(ctx context.Context, prompt string) (string, error) {
	return m.reasonFunc(ctx, prompt)
}

func (m *mockLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, *json.RawMessage, error) {
	return "mock chat response", nil, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const (
	testTenantID = "test-tenant"
	testJobID    = "test-job-1"
	testBlobURL  = "https://storage.local/documents/test-tenant/1234567890/test-job-1.pdf"
	testFileName = "invoice-2024.pdf"
)

func validGST() string  { return "22AAAAA0000A1Z5" }
func validPAN() string  { return "AAAAA0000A" }
func validIFSC() string { return "HDFC0001234" }

func newTestDocumentAgent(mockOCR *mockOCR) (*DocumentAgent, *mockDB) {
	db := newMockDB()
	storage := newMockStorage()
	queue := newMockQueue()
	validator := domain.NewIndiaValidator()
	tracer := telemetry.NoopTracer{}
	agent := NewDocumentAgent(storage, queue, db, mockOCR, validator, tracer)
	return agent, db
}

func newTestVendorAgent() (*VendorAgent, *mockDB) {
	db := newMockDB()
	validator := domain.NewIndiaValidator()
	llm := newMockLLM()
	tracer := telemetry.NoopTracer{}
	agent := NewVendorAgent(db, validator, llm, tracer)
	return agent, db
}

// ---------------------------------------------------------------------------
// DocumentClassifierWrapper tests
// ---------------------------------------------------------------------------

func TestDocumentClassifierWrapper_Classify(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"invoice pdf", "invoice.pdf", "INVOICE"},
		{"invoice with prefix", "final-invoice-2024.pdf", "INVOICE"},
		{"contract pdf", "contract.pdf", "CONTRACT"},
		{"service contract", "service-contract.pdf", "CONTRACT"},
		{"purchase order pdf", "po.pdf", "PURCHASE_ORDER"},
		{"purchase order long", "purchase-order-2024.pdf", "PURCHASE_ORDER"},
		{"unknown txt", "unknown.txt", "OTHER"},
		{"gst document", "gst-report.pdf", "GST"},
		{"mixed case invoice", "INVOICE_2024.PDF", "INVOICE"},
	}

	classifier := &DocumentClassifierWrapper{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := classifier.Classify(context.Background(), tt.filename, "pdf")
			if err != nil {
				t.Fatalf("Classify() returned error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Classify(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DocumentAgent tests
// ---------------------------------------------------------------------------

func TestDocumentAgent_ProcessDocument_AutoComplete(t *testing.T) {
	// High confidence OCR → auto-complete (no HITL)
	keyValues := map[string]string{
		"gst":  validGST(),
		"pan":  validPAN(),
		"ifsc": validIFSC(),
	}
	mockOCR := newMockOCR(0.95, keyValues)
	agent, db := newTestDocumentAgent(mockOCR)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID,
		BlobURL:  testBlobURL,
		FileName: testFileName,
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument() returned error: %v", err)
	}

	// Verify result fields
	if result["document_type"] != "INVOICE" {
		t.Errorf("document_type = %v, want INVOICE", result["document_type"])
	}
	conf, ok := result["confidence"].(float64)
	if !ok || conf != 0.95 {
		t.Errorf("confidence = %v, want 0.95", result["confidence"])
	}
	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || needsHITL {
		t.Errorf("needs_hitl = %v, want false", needsHITL)
	}

	// Verify job upserted with COMPLETED status
	dbJob, err := db.GetJob(context.Background(), testJobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}
	if dbJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", dbJob.Status, domain.JobStatusCompleted)
	}

	// Verify NO HITL request was created
	_, err = db.GetHITLRequest(context.Background(), "hitl-"+testJobID, testTenantID)
	if err == nil {
		t.Error("HITL request was created but should not have been")
	}
}

func TestDocumentAgent_ProcessDocument_HITL_LowConfidence(t *testing.T) {
	// Low confidence OCR → triggers HITL
	keyValues := map[string]string{
		"gst":  validGST(),
		"pan":  validPAN(),
		"ifsc": validIFSC(),
	}
	mockOCR := newMockOCR(0.50, keyValues)
	agent, db := newTestDocumentAgent(mockOCR)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-lowconf",
		BlobURL:  testBlobURL,
		FileName: "blurry-invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument() returned error: %v", err)
	}

	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || !needsHITL {
		t.Errorf("needs_hitl = %v, want true", needsHITL)
	}

	// Verify job upserted with AWAITING_HITL status
	dbJob, err := db.GetJob(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}
	if dbJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("job status = %s, want %s", dbJob.Status, domain.JobStatusAwaitingHITL)
	}

	// HITL request is now created by the worker, not the agent — no assertion here.
}

func TestDocumentAgent_ProcessDocument_HITL_ValidationErrors(t *testing.T) {
	// High confidence but validation errors → triggers HITL
	keyValues := map[string]string{
		"gst": "invalid-gst-123",
		"pan": "invalid-pan",
	}
	mockOCR := newMockOCR(0.95, keyValues)
	agent, db := newTestDocumentAgent(mockOCR)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-valerr",
		BlobURL:  testBlobURL,
		FileName: "invoice-valid-err.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument() returned error: %v", err)
	}

	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || !needsHITL {
		t.Errorf("needs_hitl = %v, want true", needsHITL)
	}

	// Verify job status
	dbJob, err := db.GetJob(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}
	if dbJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("job status = %s, want %s", dbJob.Status, domain.JobStatusAwaitingHITL)
	}

	// HITL request is now created by the worker, not the agent — no assertion here.
}

func TestDocumentAgent_ProcessDocument_OCRError(t *testing.T) {
	// OCR provider returns error
	mockOCR := newMockOCR(0.0, nil)
	mockOCR.extractErr = fmt.Errorf("SARVAM_API_KEY not configured: OCR service unavailable")
	agent, db := newTestDocumentAgent(mockOCR)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-ocrerr",
		BlobURL:  testBlobURL,
		FileName: "broken.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	_, err := agent.ProcessDocument(context.Background(), job)
	if err == nil {
		t.Fatal("ProcessDocument() should have returned an error")
	}
	if err.Error() != "OCR failed: SARVAM_API_KEY not configured: OCR service unavailable" {
		t.Errorf("error message = %q, want OCR failed message", err.Error())
	}

	// Verify job was NOT upserted (early return on error)
	_, err = db.GetJob(context.Background(), job.JobID, testTenantID)
	if err == nil {
		t.Error("job should not exist — ProcessDocument errored before upsert")
	}
}

func TestDocumentAgent_ProcessDocument_NilOCR(t *testing.T) {
	// OCR provider is nil
	storage := newMockStorage()
	queue := newMockQueue()
	db := newMockDB()
	validator := domain.NewIndiaValidator()
	tracer := telemetry.NoopTracer{}
	agent := NewDocumentAgent(storage, queue, db, nil, validator, tracer)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-nilocr",
		BlobURL:  testBlobURL,
		FileName: "test.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	_, err := agent.ProcessDocument(context.Background(), job)
	if err == nil {
		t.Fatal("ProcessDocument() should have returned error for nil OCR")
	}
	if err.Error() != "OCR provider not configured (missing SARVAM_API_KEY)" {
		t.Errorf("error message = %q, want OCR provider not configured", err.Error())
	}
}

// ---------------------------------------------------------------------------
// VendorAgent tests
// ---------------------------------------------------------------------------

func TestVendorAgent_ProcessVendor_Clean(t *testing.T) {
	agent, db := newTestVendorAgent()

	job := &VendorJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-vendor",
		VendorData: &VendorData{
			Name:        "Acme Corp",
			GSTNumber:   validGST(),
			PANNumber:   validPAN(),
			IFSCCode:    validIFSC(),
			BankAccount: "123456789012345678",
			Address:     "Mumbai, India",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor() returned error: %v", err)
	}

	// Check basic result fields
	if result["name"] != "Acme Corp" {
		t.Errorf("name = %v, want Acme Corp", result["name"])
	}

	// Risk score: base 50 + 15 (GST) + 10 (PAN) + 10 (IFSC) - 20 (not approved) = 65
	riskScore, ok := result["risk_score"].(int)
	if !ok || riskScore != 65 {
		t.Errorf("risk_score = %v, want 65", riskScore)
	}

	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || !needsHITL {
		t.Errorf("needs_hitl = %v, want true (risk 65 >= 60)", needsHITL)
	}

	// Verify vendor saved in DB
	vendor, err := db.GetVendor(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetVendor() returned error: %v", err)
	}
	if vendor.Name != "Acme Corp" {
		t.Errorf("vendor name = %s, want Acme Corp", vendor.Name)
	}
	if vendor.RiskScore != 65 {
		t.Errorf("vendor risk score = %d, want 65", vendor.RiskScore)
	}
	if vendor.RiskTier != domain.RiskTierMedium {
		t.Errorf("vendor risk tier = %s, want MEDIUM", vendor.RiskTier)
	}
	if vendor.Approved {
		t.Errorf("vendor Approved = true, want false (risk 65 >= 60)")
	}

	// TrustBattery.Tier: risk 65 >= 50 → STANDARD
	if vendor.TrustBattery.Tier != domain.TrustTierStandard {
		t.Errorf("TrustBattery.Tier = %s, want STANDARD", vendor.TrustBattery.Tier)
	}

	// Verify job upserted
	dbJob, err := db.GetJob(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}
	if dbJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", dbJob.Status, domain.JobStatusCompleted)
	}
	if dbJob.WorkflowType != domain.WorkflowVendorOnboarding {
		t.Errorf("workflow type = %s, want %s", dbJob.WorkflowType, domain.WorkflowVendorOnboarding)
	}
}

func TestVendorAgent_ProcessVendor_EmptyIDs(t *testing.T) {
	// Vendor with no IDs provided → lowest risk, auto-approved
	agent, db := newTestVendorAgent()

	job := &VendorJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-vendor-empty",
		VendorData: &VendorData{
			Name:      "Small Trader",
			GSTNumber: "",
			PANNumber: "",
			IFSCCode:  "",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor() returned error: %v", err)
	}

	// Empty IDs: base 50 - 20 (not approved) - 15 (incomplete docs) = 15
	riskScore, ok := result["risk_score"].(int)
	if !ok || riskScore != 15 {
		t.Errorf("risk_score = %v, want 15", riskScore)
	}

	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || needsHITL {
		t.Errorf("needs_hitl = %v, want false (risk 15 < 60)", needsHITL)
	}

	// Verify vendor saved and auto-approved (risk < 60 and no validation errors)
	vendor, err := db.GetVendor(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetVendor() returned error: %v", err)
	}
	if !vendor.Approved {
		t.Errorf("vendor Approved = false, want true (risk 15 < 60)")
	}

	// Trust tier from result: risk < 30 → PREFERRED
	trustTier, ok := result["trust_tier"].(domain.TrustTier)
	if !ok || trustTier != domain.TrustTierPreferred {
		t.Errorf("trust_tier = %v, want PREFERRED", result["trust_tier"])
	}
}

func TestVendorAgent_ProcessVendor_InvalidGST(t *testing.T) {
	// Vendor with invalid GST → validation error + HITL
	agent, db := newTestVendorAgent()

	job := &VendorJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-vendor-invalid",
		VendorData: &VendorData{
			Name:      "Risky Vendor",
			GSTNumber: "bad-gst-number",
			PANNumber: "",
			IFSCCode:  "",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor() returned error: %v", err)
	}

	// Invalid GST provided → validation error
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok || validations == nil {
		t.Fatal("validations missing from result")
	}
	if !validations.HasErrors() {
		t.Error("expected validation errors for invalid GST")
	}

	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok || !needsHITL {
		t.Errorf("needs_hitl = %v, want true (validation errors present)", needsHITL)
	}

	// Verify HITL request was created
	hitlReq, err := db.GetHITLRequest(context.Background(), "hitl-"+job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetHITLRequest() returned error: %v", err)
	}
	if hitlReq.Status != domain.HITLStatusPending {
		t.Errorf("HITL request status = %s, want PENDING", hitlReq.Status)
	}
	if hitlReq.JobID != job.JobID {
		t.Errorf("HITL request JobID = %s, want %s", hitlReq.JobID, job.JobID)
	}
}

func TestVendorAgent_ProcessVendor_WithLLMAnalysis(t *testing.T) {
	// Vendor with documents → LLM analysis called
	agent, db := newTestVendorAgent()

	called := false
	agent.llm.(*mockLLM).reasonFunc = func(_ context.Context, prompt string) (string, error) {
		called = true
		if prompt == "" {
			t.Error("LLM prompt should not be empty")
		}
		return "LLM analysis: documents look clean", nil
	}

	job := &VendorJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-vendor-llm",
		VendorData: &VendorData{
			Name:      "LLM Vendor",
			GSTNumber: "",
			PANNumber: "",
			IFSCCode:  "",
			Documents: []string{"gst_certificate.pdf", "bank_statement.pdf"},
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor() returned error: %v", err)
	}

	if !called {
		t.Error("LLM Reason() was not called despite vendor having documents")
	}

	llmAnalysis, ok := result["llm_analysis"]
	if !ok || llmAnalysis != "LLM analysis: documents look clean" {
		t.Errorf("llm_analysis = %v, want LLM analysis response", llmAnalysis)
	}

	// Verify vendor saved
	vendor, err := db.GetVendor(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetVendor() returned error: %v", err)
	}
	if vendor.Name != "LLM Vendor" {
		t.Errorf("vendor name = %s, want LLM Vendor", vendor.Name)
	}
}

func TestVendorAgent_ProcessVendor_Duplicate(t *testing.T) {
	// CheckDuplicate should find existing vendor by name
	agent, db := newTestVendorAgent()

	// Pre-populate a vendor
	existing := &domain.Vendor{
		ID:       "existing-vendor-1",
		TenantID: testTenantID,
		Name:     "Existing Corp",
	}
	if err := db.UpsertVendor(context.Background(), existing); err != nil {
		t.Fatalf("failed to pre-populate vendor: %v", err)
	}

	// Check for duplicate
	dup, err := agent.CheckDuplicate(context.Background(), testTenantID, "Existing Corp")
	if err != nil {
		t.Fatalf("CheckDuplicate() returned error: %v", err)
	}
	if !dup {
		t.Error("CheckDuplicate() = false, want true (Existing Corp exists)")
	}

	// Non-existent vendor
	dup, err = agent.CheckDuplicate(context.Background(), testTenantID, "New Vendor")
	if err != nil {
		t.Fatalf("CheckDuplicate() returned error: %v", err)
	}
	if dup {
		t.Error("CheckDuplicate() = true, want false (New Vendor does not exist)")
	}
}

func TestVendorAgent_ValidateVendor(t *testing.T) {
	agent, _ := newTestVendorAgent()

	tests := []struct {
		name string
		gst  string
		pan  string
		ifsc string
		want bool
	}{
		{"all valid", validGST(), validPAN(), validIFSC(), true},
		{"empty all", "", "", "", true},
		{"invalid gst", "bad-gst", validPAN(), validIFSC(), false},
		{"invalid pan", validGST(), "bad-pan", validIFSC(), false},
		{"invalid ifsc", validGST(), validPAN(), "bad-ifsc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := agent.ValidateVendor(context.Background(), tt.gst, tt.pan, tt.ifsc)
			if err != nil {
				t.Fatalf("ValidateVendor() returned error: %v", err)
			}
			if result.Valid != tt.want {
				t.Errorf("ValidateVendor().Valid = %v, want %v; errors=%v", result.Valid, tt.want, result.Errors)
			}
		})
	}
}

func TestVendorAgent_DocumentAgentTools(t *testing.T) {
	// Verify tool listing
	storage := newMockStorage()
	queue := newMockQueue()
	db := newMockDB()
	agent := NewDocumentAgent(storage, queue, db, newMockOCR(1.0, nil), domain.NewIndiaValidator(), telemetry.NoopTracer{})

	tools := agent.DocumentAgentTools()
	expected := []string{"process_document", "queue_document", "get_document_status"}
	if len(tools) != len(expected) {
		t.Errorf("DocumentAgentTools() returned %d tools, want %d", len(tools), len(expected))
	}
	for i, tool := range expected {
		if tools[i] != tool {
			t.Errorf("DocumentAgentTools()[%d] = %s, want %s", i, tools[i], tool)
		}
	}
}

func TestVendorAgent_VendorAgentTools(t *testing.T) {
	agent, _ := newTestVendorAgent()

	tools := agent.VendorAgentTools()
	expected := []string{"process_vendor", "validate_vendor", "check_duplicate"}
	if len(tools) != len(expected) {
		t.Errorf("VendorAgentTools() returned %d tools, want %d", len(tools), len(expected))
	}
	for i, tool := range expected {
		if tools[i] != tool {
			t.Errorf("VendorAgentTools()[%d] = %s, want %s", i, tools[i], tool)
		}
	}
}

// ---------------------------------------------------------------------------
// Timing-related test helpers
// ---------------------------------------------------------------------------

// Ensure ProcessDocument sets UpdatedAt to a non-zero time
func TestDocumentAgent_ProcessDocument_SetsTimestamps(t *testing.T) {
	mockOCR := newMockOCR(0.95, map[string]string{"gst": validGST()})
	agent, db := newTestDocumentAgent(mockOCR)

	job := &DocumentJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-ts",
		BlobURL:  testBlobURL,
		FileName: "timestamps.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	_, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument() returned error: %v", err)
	}

	dbJob, err := db.GetJob(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}

	if dbJob.UpdatedAt.IsZero() {
		t.Error("job UpdatedAt is zero")
	}
	if !dbJob.UpdatedAt.After(time.Now().Add(-time.Minute)) {
		t.Error("job UpdatedAt is not recent")
	}
}

func TestVendorAgent_ProcessVendor_SetsTimestamps(t *testing.T) {
	agent, db := newTestVendorAgent()

	job := &VendorJob{
		TenantID: testTenantID,
		JobID:    testJobID + "-vendor-ts",
		VendorData: &VendorData{
			Name: "Timestamped Vendor",
		},
	}

	_, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor() returned error: %v", err)
	}

	// Verify vendor timestamps
	vendor, err := db.GetVendor(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetVendor() returned error: %v", err)
	}
	if vendor.CreatedAt.IsZero() {
		t.Error("vendor CreatedAt is zero")
	}
	if vendor.UpdatedAt.IsZero() {
		t.Error("vendor UpdatedAt is zero")
	}

	// Verify job timestamps
	dbJob, err := db.GetJob(context.Background(), job.JobID, testTenantID)
	if err != nil {
		t.Fatalf("GetJob() returned error: %v", err)
	}
	if dbJob.UpdatedAt.IsZero() {
		t.Error("job UpdatedAt is zero")
	}
}
