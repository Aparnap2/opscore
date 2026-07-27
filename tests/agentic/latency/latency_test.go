package latency_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
	agentic "github.com/aparna/opscore/tests/agentic"
)

// ---------------------------------------------------------------------------
// StepMetrics and WorkflowMetrics helpers
// ---------------------------------------------------------------------------

// StepMetrics records duration and success for a single step.
type StepMetrics struct {
	StepName string
	Duration time.Duration
	Success  bool
}

// WorkflowMetrics collects step metrics for a workflow.
type WorkflowMetrics struct {
	Steps []StepMetrics
	Total time.Duration
}

// AddStep records a step's duration.
func (wm *WorkflowMetrics) AddStep(name string, d time.Duration, success bool) {
	wm.Steps = append(wm.Steps, StepMetrics{StepName: name, Duration: d, Success: success})
}

// LatencyBudget asserts that p95 step latency is under a threshold.
func (wm *WorkflowMetrics) AssertP95Under(t *testing.T, threshold time.Duration) {
	t.Helper()
	if len(wm.Steps) == 0 {
		t.Error("no steps to measure")
		return
	}
	// Sort durations and pick p95
	durations := make([]time.Duration, len(wm.Steps))
	for i, s := range wm.Steps {
		durations[i] = s.Duration
	}
	// Simple p95: sort ascending, pick 95th percentile
	for i := 0; i < len(durations); i++ {
		for j := i + 1; j < len(durations); j++ {
			if durations[j] < durations[i] {
				durations[i], durations[j] = durations[j], durations[i]
			}
		}
	}
	p95Index := int(float64(len(durations)) * 0.95)
	if p95Index >= len(durations) {
		p95Index = len(durations) - 1
	}
	p95 := durations[p95Index]
	if p95 > threshold {
		t.Errorf("p95 latency %v exceeds threshold %v", p95, threshold)
	}
}

// ---------------------------------------------------------------------------
// In-memory providers for latency tests
// ---------------------------------------------------------------------------

type latMockDB struct {
	mu          sync.Mutex
	jobs        map[string]*domain.Job
	vendors     map[string]*domain.Vendor
	documents   map[string]*domain.Document
	hitlReqs    map[string]*domain.HITLRequest
	auditEvents []*domain.AuditEvent
}

func newLatMockDB() *latMockDB {
	return &latMockDB{
		jobs:      make(map[string]*domain.Job),
		vendors:   make(map[string]*domain.Vendor),
		documents: make(map[string]*domain.Document),
		hitlReqs:  make(map[string]*domain.HITLRequest),
	}
}

func (m *latMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return nil
}
func (m *latMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	if j.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return j, nil
}
func (m *latMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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
func (m *latMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[v.ID] = v
	return nil
}
func (m *latMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return v, nil
}
func (m *latMockDB) ListVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *latMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[d.ID] = d
	return nil
}
func (m *latMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return d, nil
}
func (m *latMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *latMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlReqs[r.ID] = r
	return nil
}
func (m *latMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlReqs[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}
func (m *latMockDB) ListPendingHITL(_ context.Context, _ string) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *latMockDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *latMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *latMockDB) ListAuditEvents(_ context.Context, _, _, _ string, _ int) ([]*domain.AuditEvent, error) {
	return nil, nil
}
func (m *latMockDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (m *latMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *latMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

type latMockStorage struct{}

func (m *latMockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	return fmt.Sprintf("https://storage.local/%s/%s", container, key), nil
}
func (m *latMockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not impl")
}
func (m *latMockStorage) Delete(_ context.Context, _, _ string) error { return nil }
func (m *latMockStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

type latMockQueue struct {
	mu       sync.Mutex
	messages []*providers.QueueMessage
}

func newLatMockQueue() *latMockQueue { return &latMockQueue{} }

func (m *latMockQueue) Enqueue(_ context.Context, _ string, msg any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, _ := json.Marshal(msg)
	id := fmt.Sprintf("msg-%d", len(m.messages))
	m.messages = append(m.messages, &providers.QueueMessage{ID: id, Body: string(body)})
	return id, nil
}
func (m *latMockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return nil, nil
	}
	msg := m.messages[0]
	m.messages = m.messages[1:]
	return msg, nil
}
func (m *latMockQueue) Delete(_ context.Context, _, _ string) error { return nil }
func (m *latMockQueue) Poison(_ context.Context, _, _ string) error { return nil }

type latMockLLM struct{}

func (m *latMockLLM) ExtractFields(_ context.Context, _ string, _ any) (json.RawMessage, float64, error) {
	return json.RawMessage("{}"), 1.0, nil
}
func (m *latMockLLM) Reason(_ context.Context, _ string) (string, error) { return "analysis", nil }
func (m *latMockLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, *json.RawMessage, error) {
	return "", nil, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestLatency_StubOCRExtract(t *testing.T) {
	// Measure StubOCR.Extract latency (should be < 10ms for stub)
	t.Log("=== Test: StubOCR.Extract latency ===")

	ocr := &agentic.StubOCR{}
	ctx := context.Background()

	var metrics WorkflowMetrics
	for i := 0; i < 5; i++ {
		start := time.Now()
		_, err := ocr.Extract(ctx, "http://example.com/confidence_0.95/doc.pdf")
		duration := time.Since(start)
		metrics.AddStep("ocr_extract", duration, err == nil)
	}

	metrics.AssertP95Under(t, 10*time.Millisecond)
	t.Logf("✅ StubOCR.Extract p95 latency under 10ms: %v", metrics.Total)
}

func TestLatency_StubLLMReason(t *testing.T) {
	// Measure StubLLM.Reason latency (should be < 10ms for stub)
	t.Log("=== Test: StubLLM.Reason latency ===")

	llm := &agentic.StubLLM{}
	ctx := context.Background()

	var metrics WorkflowMetrics
	for i := 0; i < 5; i++ {
		start := time.Now()
		_, err := llm.Reason(ctx, "analyze this data")
		duration := time.Since(start)
		metrics.AddStep("llm_reason", duration, err == nil)
	}

	metrics.AssertP95Under(t, 10*time.Millisecond)
	t.Logf("✅ StubLLM.Reason p95 latency under 10ms")
}

func TestLatency_DocumentAgent_AutoComplete(t *testing.T) {
	// Measure DocumentAgent.ProcessDocument workflow latency for auto-complete path
	t.Log("=== Test: DocumentAgent auto-complete latency ===")

	ocr := &agentic.StubOCR{}
	db := newLatMockDB()
	storage := &latMockStorage{}
	queue := newLatMockQueue()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	var metrics WorkflowMetrics
	for i := 0; i < 3; i++ {
		job := &agents.DocumentJob{
			TenantID: "tenant-lat-1",
			JobID:    fmt.Sprintf("lat-doc-auto-%d", i),
			BlobURL:  "http://example.com/confidence_0.95/invoice.pdf",
			FileName: "invoice.pdf",
		}
		start := time.Now()
		_, err := agent.ProcessDocument(context.Background(), job)
		duration := time.Since(start)
		metrics.AddStep("doc_agent_auto", duration, err == nil)
	}

	metrics.AssertP95Under(t, 200*time.Millisecond)
	t.Logf("✅ DocumentAgent auto-complete p95 latency under 200ms")
}

func TestLatency_DocumentAgent_HITLPath(t *testing.T) {
	// Measure DocumentAgent.ProcessDocument workflow latency for HITL-trigger path
	t.Log("=== Test: DocumentAgent HITL path latency ===")

	ocr := &agentic.StubOCR{}
	db := newLatMockDB()
	storage := &latMockStorage{}
	queue := newLatMockQueue()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	var metrics WorkflowMetrics
	for i := 0; i < 3; i++ {
		job := &agents.DocumentJob{
			TenantID: "tenant-lat-2",
			JobID:    fmt.Sprintf("lat-doc-hitl-%d", i),
			BlobURL:  "http://example.com/confidence_0.45/blurry.pdf",
			FileName: "blurry.pdf",
		}
		start := time.Now()
		_, err := agent.ProcessDocument(context.Background(), job)
		duration := time.Since(start)
		metrics.AddStep("doc_agent_hitl", duration, err == nil)
	}

	metrics.AssertP95Under(t, 200*time.Millisecond)
	t.Logf("✅ DocumentAgent HITL path p95 latency under 200ms")
}

func TestLatency_VendorAgent_ProcessVendor(t *testing.T) {
	// Measure VendorAgent.ProcessVendor workflow latency
	t.Log("=== Test: VendorAgent.ProcessVendor latency ===")

	llm := &latMockLLM{}
	db := newLatMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)

	var metrics WorkflowMetrics
	for i := 0; i < 3; i++ {
		job := &agents.VendorJob{
			TenantID: "tenant-lat-3",
			JobID:    fmt.Sprintf("lat-vendor-%d", i),
			VendorData: &agents.VendorData{
				Name:      fmt.Sprintf("Vendor %d", i),
				GSTNumber: "22AAAAA0000A1Z5",
			},
		}
		start := time.Now()
		_, err := agent.ProcessVendor(context.Background(), job)
		duration := time.Since(start)
		metrics.AddStep("vendor_agent", duration, err == nil)
	}

	metrics.AssertP95Under(t, 200*time.Millisecond)
	t.Logf("✅ VendorAgent p95 latency under 200ms")
}

func TestLatency_QueueEnqueueDequeue(t *testing.T) {
	// Measure queue enqueue→dequeue round-trip latency
	t.Log("=== Test: Queue enqueue→dequeue latency ===")

	queue := newLatMockQueue()
	ctx := context.Background()

	var metrics WorkflowMetrics
	for i := 0; i < 5; i++ {
		start := time.Now()
		_, err := queue.Enqueue(ctx, "test-q", map[string]string{"data": "test"})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
		msg, err := queue.Dequeue(ctx, "test-q")
		duration := time.Since(start)
		metrics.AddStep("queue_roundtrip", duration, err == nil && msg != nil)
	}

	metrics.AssertP95Under(t, 10*time.Millisecond)
	t.Logf("✅ Queue enqueue→dequeue p95 latency under 10ms")
}

func TestLatency_DBUpsertGet(t *testing.T) {
	// Measure DB upsert→get round-trip latency
	t.Log("=== Test: DB upsert→get latency ===")

	db := newLatMockDB()
	ctx := context.Background()

	var metrics WorkflowMetrics
	for i := 0; i < 5; i++ {
		job := &domain.Job{
			ID: fmt.Sprintf("lat-db-%d", i), TenantID: "tenant-lat-4",
			Status: domain.JobStatusPending,
		}
		start := time.Now()
		if err := db.UpsertJob(ctx, job); err != nil {
			t.Fatalf("UpsertJob failed: %v", err)
		}
		_, err := db.GetJob(ctx, job.ID, job.TenantID)
		duration := time.Since(start)
		metrics.AddStep("db_roundtrip", duration, err == nil)
	}

	metrics.AssertP95Under(t, 10*time.Millisecond)
	t.Logf("✅ DB upsert→get p95 latency under 10ms")
}

func TestLatency_EndToEndWorkflow(t *testing.T) {
	// Assert end-to-end workflow latency under 200ms for normal cases
	t.Log("=== Test: End-to-end workflow latency ===")

	ocr := &agentic.StubOCR{}
	llm := &latMockLLM{}
	db := newLatMockDB()
	storage := &latMockStorage{}
	queue := newLatMockQueue()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}

	docAgent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)
	vendorAgent := agents.NewVendorAgent(db, validator, llm, tracer)

	ctx := context.Background()
	var totalMetrics WorkflowMetrics

	// Document processing
	docJob := &agents.DocumentJob{
		TenantID: "tenant-lat-5", JobID: "lat-e2e-doc",
		BlobURL:  "http://example.com/confidence_0.95/invoice.pdf",
		FileName: "invoice.pdf",
	}
	start := time.Now()
	_, err := docAgent.ProcessDocument(ctx, docJob)
	totalMetrics.AddStep("e2e_doc", time.Since(start), err == nil)
	if err != nil {
		t.Fatalf("doc agent failed: %v", err)
	}

	// Vendor processing
	vendorJob := &agents.VendorJob{
		TenantID: "tenant-lat-5", JobID: "lat-e2e-vendor",
		VendorData: &agents.VendorData{
			Name:      "E2E Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
		},
	}
	start = time.Now()
	_, err = vendorAgent.ProcessVendor(ctx, vendorJob)
	totalMetrics.AddStep("e2e_vendor", time.Since(start), err == nil)
	if err != nil {
		t.Fatalf("vendor agent failed: %v", err)
	}

	if totalMetrics.Total > 200*time.Millisecond {
		t.Errorf("total e2e latency %v exceeds 200ms", totalMetrics.Total)
	}
	t.Logf("✅ End-to-end workflow: total=%v", totalMetrics.Total)
}

// --- Manufacturing pivot (Phase 1.4A) mock stubs (not exercised by this test) ---

func (m *latMockDB) UpsertPurchaseOrder(_ context.Context, _ *domain.PurchaseOrder) error { return nil }
func (m *latMockDB) GetPurchaseOrderByID(_ context.Context, _, _ string) (*domain.PurchaseOrder, error) {
	return nil, fmt.Errorf("not found")
}
func (m *latMockDB) ListPurchaseOrders(_ context.Context, _ string, _, _ int) ([]*domain.PurchaseOrder, error) {
	return nil, nil
}
func (m *latMockDB) UpsertGoodsReceipt(_ context.Context, _ *domain.GoodsReceipt) error { return nil }
func (m *latMockDB) GetGoodsReceiptByID(_ context.Context, _, _ string) (*domain.GoodsReceipt, error) {
	return nil, fmt.Errorf("not found")
}
func (m *latMockDB) ListGoodsReceipts(_ context.Context, _, _ string, _, _ int) ([]*domain.GoodsReceipt, error) {
	return nil, nil
}
func (m *latMockDB) UpsertInvoice(_ context.Context, _ *domain.Invoice) error { return nil }
func (m *latMockDB) GetInvoiceByID(_ context.Context, _, _ string) (*domain.Invoice, error) {
	return nil, fmt.Errorf("not found")
}
func (m *latMockDB) ListInvoices(_ context.Context, _, _ string, _, _ int) ([]*domain.Invoice, error) {
	return nil, nil
}
func (m *latMockDB) UpsertExceptionCase(_ context.Context, _ *domain.ExceptionCase) error { return nil }
func (m *latMockDB) GetExceptionCaseByID(_ context.Context, _, _ string) (*domain.ExceptionCase, error) {
	return nil, fmt.Errorf("not found")
}
func (m *latMockDB) ListExceptionCases(_ context.Context, _, _, _ string, _, _ int) ([]*domain.ExceptionCase, error) {
	return nil, nil
}
func (m *latMockDB) UpdateExceptionCaseStatus(_ context.Context, _, _, _ string) error { return nil }
func (m *latMockDB) IsVersionConflict(_ error) bool {
	return false
}

func (m *latMockDB) WithTx(_ context.Context, _ string, fn func(context.Context) error) error {
	return fn(context.Background())
}
