package contracts_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	agentic "github.com/aparna/opscore/tests/agentic"
)

// ---------------------------------------------------------------------------
// OCRProvider Contract Tests
// ---------------------------------------------------------------------------

func TestContract_OCRResult_Shape(t *testing.T) {
	// Verifies OCRResult shape: all fields populated for each trigger word,
	// error mapping, and KeyValues correctness.

	ocr := &agentic.StubOCR{}
	ctx := context.Background()

	t.Run("high_confidence_populates_all_fields", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "http://example.com/confidence_0.95/doc.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Assert result shape — all fields populated
		if result.Text == "" {
			t.Error("Text must not be empty")
		}
		if result.Language == "" {
			t.Error("Language must not be empty")
		}
		if result.Provider == "" {
			t.Error("Provider must not be empty")
		}
		if result.Confidence != 0.95 {
			t.Errorf("Confidence = %f, want 0.95", result.Confidence)
		}
		if result.KeyValues == nil {
			t.Fatal("KeyValues must not be nil")
		}
		if gst, ok := result.KeyValues["gst"]; !ok || gst != "22AAAAA0000A1Z5" {
			t.Errorf("KeyValues['gst'] = %q, want valid GST", gst)
		}
		t.Logf("OCRResult shape OK: Text=%q Lang=%q Provider=%q Confidence=%.2f Keys=%v",
			result.Text, result.Language, result.Provider, result.Confidence, result.KeyValues)
	})

	t.Run("low_confidence_result_shape", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "confidence_0.45/blurry.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.45 {
			t.Errorf("Confidence = %f, want 0.45", result.Confidence)
		}
		if result.KeyValues["amount"] != "1000" {
			t.Errorf("KeyValues['amount'] = %q, want 1000", result.KeyValues["amount"])
		}
		if !strings.Contains(result.Text, "low confidence") {
			t.Errorf("Text = %q, want low confidence text", result.Text)
		}
	})

	t.Run("trigger_error_returns_error", func(t *testing.T) {
		_, err := ocr.Extract(ctx, "/trigger_error/doc.png")
		if err == nil {
			t.Fatal("expected error for trigger_error, got nil")
		}
		if !strings.Contains(err.Error(), "simulated failure") {
			t.Errorf("error = %v, want simulated failure", err)
		}
	})

	t.Run("key_values_error_invalid_data", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "/key_values_error/test.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.90 {
			t.Errorf("Confidence = %f, want 0.90", result.Confidence)
		}
		if result.KeyValues["gst"] != "invalid_gst_123" {
			t.Errorf("KeyValues['gst'] = %q, want invalid_gst_123", result.KeyValues["gst"])
		}
		if result.KeyValues["pan"] != "short" {
			t.Errorf("KeyValues['pan'] = %q, want short", result.KeyValues["pan"])
		}
	})

	t.Run("default_confidence_0.90", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "http://example.com/normal_doc.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.90 {
			t.Errorf("Confidence = %f, want 0.90", result.Confidence)
		}
		if result.KeyValues["amount"] != "500" {
			t.Errorf("KeyValues['amount'] = %q, want 500", result.KeyValues["amount"])
		}
	})
}

// ---------------------------------------------------------------------------
// LLMProvider Contract Tests
// ---------------------------------------------------------------------------

func TestContract_LLMProvider_AllMethods(t *testing.T) {
	// Verifies StubLLM correctly implements all 3 LLMProvider methods
	// with trigger word routing and error propagation.

	llm := &agentic.StubLLM{}
	ctx := context.Background()

	t.Run("ExtractFields_default", func(t *testing.T) {
		result, confidence, err := llm.ExtractFields(ctx, "extract data from this text", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if confidence != 0.95 {
			t.Errorf("Confidence = %f, want 0.95", confidence)
		}
		if string(result) != `{"analysis":"default"}` {
			t.Errorf("Result = %s, want default analysis JSON", string(result))
		}
	})

	t.Run("ExtractFields_trigger_error", func(t *testing.T) {
		_, _, err := llm.ExtractFields(ctx, "trigger_error data here", nil)
		if err == nil {
			t.Fatal("expected error for trigger_error, got nil")
		}
		if !strings.Contains(err.Error(), "simulated failure") {
			t.Errorf("error = %v, want simulated failure", err)
		}
	})

	t.Run("Reason_routing_approve", func(t *testing.T) {
		result, err := llm.Reason(ctx, "please approve this vendor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "approved" {
			t.Errorf("Reason = %q, want approved", result)
		}
	})

	t.Run("Reason_routing_reject", func(t *testing.T) {
		result, err := llm.Reason(ctx, "reject this transaction please")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "rejected" {
			t.Errorf("Reason = %q, want rejected", result)
		}
	})

	t.Run("Reason_routing_default", func(t *testing.T) {
		result, err := llm.Reason(ctx, "analyze this data carefully")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "analyzed" {
			t.Errorf("Reason = %q, want analyzed", result)
		}
	})

	t.Run("Reason_trigger_error", func(t *testing.T) {
		_, err := llm.Reason(ctx, "trigger_error in this prompt")
		if err == nil {
			t.Fatal("expected error for trigger_error, got nil")
		}
	})

	t.Run("Chat_routing_approve", func(t *testing.T) {
		result, _, err := llm.Chat(ctx, []providers.ChatMessage{
			{Role: "user", Content: "please approve this"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "approved" {
			t.Errorf("Chat = %q, want approved", result)
		}
	})

	t.Run("Chat_routing_reject", func(t *testing.T) {
		result, _, err := llm.Chat(ctx, []providers.ChatMessage{
			{Role: "user", Content: "please reject this"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "rejected" {
			t.Errorf("Chat = %q, want rejected", result)
		}
	})

	t.Run("Chat_trigger_error", func(t *testing.T) {
		_, _, err := llm.Chat(ctx, []providers.ChatMessage{
			{Role: "user", Content: "trigger_error please"},
		})
		if err == nil {
			t.Fatal("expected error for trigger_error, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// HITLProvider Contract Tests (nilSlackProvider)
// ---------------------------------------------------------------------------

type nilSlack struct{}

func (n nilSlack) SendApprovalRequest(_ context.Context, _ *domain.HITLRequest) error {
	return nil
}

func (n nilSlack) SendMessage(_ context.Context, _, _ string) error {
	return nil
}

var _ providers.HITLProvider = (*nilSlack)(nil)

func TestContract_HITLProvider_NoopBehavior(t *testing.T) {
	// Verifies nilSlackProvider correctly no-ops both methods.
	ctx := context.Background()
	slack := nilSlack{}

	t.Run("SendApprovalRequest_returns_nil", func(t *testing.T) {
		err := slack.SendApprovalRequest(ctx, &domain.HITLRequest{
			ID:       "test-hitl",
			TenantID: "test-tenant",
			JobID:    "test-job",
			Status:   domain.HITLStatusPending,
		})
		if err != nil {
			t.Errorf("SendApprovalRequest returned error: %v", err)
		}
	})

	t.Run("SendMessage_returns_nil", func(t *testing.T) {
		err := slack.SendMessage(ctx, "test-tenant", "hello world")
		if err != nil {
			t.Errorf("SendMessage returned error: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// In-memory DBProvider Contract Tests
// ---------------------------------------------------------------------------

// contractMockDB implements providers.DBProvider with in-memory maps for contract testing.
type contractMockDB struct {
	jobs         map[string]*domain.Job
	vendors      map[string]*domain.Vendor
	documents    map[string]*domain.Document
	hitlRequests map[string]*domain.HITLRequest
	auditEvents  []*domain.AuditEvent
}

func newContractMockDB() *contractMockDB {
	return &contractMockDB{
		jobs:         make(map[string]*domain.Job),
		vendors:      make(map[string]*domain.Vendor),
		documents:    make(map[string]*domain.Document),
		hitlRequests: make(map[string]*domain.HITLRequest),
	}
}

func (m *contractMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.jobs[job.ID] = job
	return nil
}
func (m *contractMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	j, ok := m.jobs[id]
	if !ok {
		return nil, io.EOF
	}
	return j, nil
}
func (m *contractMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
	var r []*domain.Job
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			r = append(r, j)
		}
	}
	return r, nil
}
func (m *contractMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.vendors[v.ID] = v
	return nil
}
func (m *contractMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, io.EOF
	}
	return v, nil
}
func (m *contractMockDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	var r []*domain.Vendor
	for _, v := range m.vendors {
		if v.TenantID == tenantID {
			r = append(r, v)
		}
	}
	return r, nil
}
func (m *contractMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.documents[d.ID] = d
	return nil
}
func (m *contractMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, io.EOF
	}
	return d, nil
}
func (m *contractMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *contractMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.hitlRequests[r.ID] = r
	return nil
}
func (m *contractMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	r, ok := m.hitlRequests[id]
	if !ok || r.TenantID != tenantID {
		return nil, io.EOF
	}
	return r, nil
}
func (m *contractMockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	var r []*domain.HITLRequest
	for _, h := range m.hitlRequests {
		if h.TenantID == tenantID && h.Status == domain.HITLStatusPending {
			r = append(r, h)
		}
	}
	return r, nil
}
func (m *contractMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *contractMockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
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
func (m *contractMockDB) GetRecentJobs(_ context.Context, _ string, limit int) ([]*domain.Job, error) {
	var r []*domain.Job
	for _, j := range m.jobs {
		r = append(r, j)
		if limit > 0 && len(r) >= limit {
			break
		}
	}
	return r, nil
}
func (m *contractMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	var r []*domain.Vendor
	for _, v := range m.vendors {
		r = append(r, v)
	}
	return r, nil
}
func (m *contractMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

func TestContract_DBProvider_CRUDRoundTrips(t *testing.T) {
	db := newContractMockDB()
	ctx := context.Background()
	now := time.Now()

	t.Run("Job_CRUD", func(t *testing.T) {
		job := &domain.Job{
			ID: "test-job-1", TenantID: "tenant-1",
			WorkflowType: domain.WorkflowDocumentIngestion,
			Status:       domain.JobStatusPending,
			CreatedAt:    now, UpdatedAt: now,
		}
		if err := db.UpsertJob(ctx, job); err != nil {
			t.Fatalf("UpsertJob failed: %v", err)
		}
		got, err := db.GetJob(ctx, "test-job-1", "tenant-1")
		if err != nil {
			t.Fatalf("GetJob failed: %v", err)
		}
		if got.Status != domain.JobStatusPending {
			t.Errorf("Status = %s, want PENDING", got.Status)
		}
		if got.WorkflowType != domain.WorkflowDocumentIngestion {
			t.Errorf("WorkflowType = %s", got.WorkflowType)
		}

		// Update to PROCESSING
		job.Status = domain.JobStatusProcessing
		job.UpdatedAt = time.Now()
		if err := db.UpsertJob(ctx, job); err != nil {
			t.Fatalf("UpsertJob update failed: %v", err)
		}
		got2, _ := db.GetJob(ctx, "test-job-1", "tenant-1")
		if got2.Status != domain.JobStatusProcessing {
			t.Errorf("After update Status = %s, want PROCESSING", got2.Status)
		}
	})

	t.Run("Vendor_CRUD", func(t *testing.T) {
		v := &domain.Vendor{
			ID: "vendor-1", TenantID: "tenant-1",
			Name: "Test Vendor", GSTNumber: "22AAAAA0000A1Z5",
			RiskScore: 65, Approved: false,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.UpsertVendor(ctx, v); err != nil {
			t.Fatalf("UpsertVendor failed: %v", err)
		}
		got, err := db.GetVendor(ctx, "vendor-1", "tenant-1")
		if err != nil {
			t.Fatalf("GetVendor failed: %v", err)
		}
		if got.Name != "Test Vendor" {
			t.Errorf("Name = %q, want Test Vendor", got.Name)
		}
		if got.GSTNumber != "22AAAAA0000A1Z5" {
			t.Errorf("GSTNumber = %q", got.GSTNumber)
		}
	})

	t.Run("Document_CRUD", func(t *testing.T) {
		d := &domain.Document{
			ID: "doc-1", TenantID: "tenant-1", JobID: "job-1",
			FileName: "invoice.pdf", Type: "INVOICE", Status: "INDEXED",
			CreatedAt: now,
		}
		if err := db.UpsertDocument(ctx, d); err != nil {
			t.Fatalf("UpsertDocument failed: %v", err)
		}
		got, err := db.GetDocument(ctx, "doc-1", "tenant-1")
		if err != nil {
			t.Fatalf("GetDocument failed: %v", err)
		}
		if got.FileName != "invoice.pdf" {
			t.Errorf("FileName = %q", got.FileName)
		}
		if got.Type != "INVOICE" {
			t.Errorf("Type = %q", got.Type)
		}
	})

	t.Run("HITLRequest_CRUD", func(t *testing.T) {
		req := &domain.HITLRequest{
			ID: "hitl-1", TenantID: "tenant-1", JobID: "job-1",
			Reason: "low confidence", Status: domain.HITLStatusPending,
			SentAt: now,
		}
		if err := db.UpsertHITLRequest(ctx, req); err != nil {
			t.Fatalf("UpsertHITLRequest failed: %v", err)
		}
		got, err := db.GetHITLRequest(ctx, "hitl-1", "tenant-1")
		if err != nil {
			t.Fatalf("GetHITLRequest failed: %v", err)
		}
		if got.Status != domain.HITLStatusPending {
			t.Errorf("Status = %s, want PENDING", got.Status)
		}
		if got.Reason != "low confidence" {
			t.Errorf("Reason = %q", got.Reason)
		}

		// Approve
		now2 := time.Now()
		got.Status = domain.HITLStatusApproved
		got.RespondedAt = &now2
		got.Responder = "user-1"
		if err := db.UpsertHITLRequest(ctx, got); err != nil {
			t.Fatalf("UpsertHITLRequest update failed: %v", err)
		}
		got2, _ := db.GetHITLRequest(ctx, "hitl-1", "tenant-1")
		if got2.Status != domain.HITLStatusApproved {
			t.Errorf("Status after approve = %s, want APPROVED", got2.Status)
		}
		if got2.Responder != "user-1" {
			t.Errorf("Responder = %q", got2.Responder)
		}
	})

	t.Run("AuditEvent_AppendAndList", func(t *testing.T) {
		e1 := &domain.AuditEvent{
			Actor: "system", Action: "PROCESSED",
			TargetType: "job", TargetID: "job-1",
			NewState: "COMPLETED", Timestamp: time.Now(),
			TenantID: "tenant-1",
		}
		if err := db.AppendAuditEvent(ctx, e1); err != nil {
			t.Fatalf("AppendAuditEvent failed: %v", err)
		}
		e2 := &domain.AuditEvent{
			Actor: "user", Action: "APPROVED",
			TargetType: "hitl", TargetID: "hitl-1",
			NewState: "APPROVED", Timestamp: time.Now(),
			TenantID: "tenant-1",
		}
		if err := db.AppendAuditEvent(ctx, e2); err != nil {
			t.Fatalf("AppendAuditEvent failed: %v", err)
		}

		events, err := db.ListAuditEvents(ctx, "tenant-1", "", "", 10)
		if err != nil {
			t.Fatalf("ListAuditEvents failed: %v", err)
		}
		if len(events) != 2 {
			t.Errorf("expected 2 events, got %d", len(events))
		}
		if events[0].Action != "PROCESSED" {
			t.Errorf("first event action = %q", events[0].Action)
		}

		// Filter by target
		jobEvents, err := db.ListAuditEvents(ctx, "tenant-1", "job", "job-1", 10)
		if err != nil {
			t.Fatalf("ListAuditEvents filtered failed: %v", err)
		}
		if len(jobEvents) != 1 {
			t.Errorf("expected 1 job event, got %d", len(jobEvents))
		}
	})
}

// ---------------------------------------------------------------------------
// QueueProvider In-Memory Contract Test
// ---------------------------------------------------------------------------

// contractMockQueue implements providers.QueueProvider in-memory.
type contractMockQueue struct {
	queues map[string][]*providers.QueueMessage
}

func newContractMockQueue() *contractMockQueue {
	return &contractMockQueue{queues: make(map[string][]*providers.QueueMessage)}
}

func (m *contractMockQueue) Enqueue(_ context.Context, qname string, msg any) (string, error) {
	id := "msg-" + qname
	m.queues[qname] = append(m.queues[qname], &providers.QueueMessage{ID: id})
	return id, nil
}
func (m *contractMockQueue) Dequeue(_ context.Context, qname string) (*providers.QueueMessage, error) {
	q := m.queues[qname]
	if len(q) == 0 {
		return nil, nil
	}
	msg := q[0]
	m.queues[qname] = q[1:]
	return msg, nil
}
func (m *contractMockQueue) Delete(_ context.Context, _, _ string) error { return nil }
func (m *contractMockQueue) Poison(_ context.Context, _, _ string) error { return nil }

func TestContract_QueueProvider_EnqueueDequeueRoundTrip(t *testing.T) {
	q := newContractMockQueue()
	ctx := context.Background()

	t.Run("enqueue_then_dequeue", func(t *testing.T) {
		id, err := q.Enqueue(ctx, "test-q", "hello")
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
		if id == "" {
			t.Fatal("Enqueue returned empty id")
		}
		msg, err := q.Dequeue(ctx, "test-q")
		if err != nil {
			t.Fatalf("Dequeue failed: %v", err)
		}
		if msg == nil {
			t.Fatal("Dequeue returned nil after enqueue")
		}
		if msg.ID != id {
			t.Errorf("Dequeue msg.ID = %q, want %q", msg.ID, id)
		}
	})

	t.Run("empty_queue_returns_nil", func(t *testing.T) {
		msg, err := q.Dequeue(ctx, "nonexistent-q")
		if err != nil {
			t.Fatalf("Dequeue on empty queue returned error: %v", err)
		}
		if msg != nil {
			t.Errorf("Dequeue on empty queue returned non-nil msg: %+v", msg)
		}
	})

	t.Run("enqueue_delete_poison_no_error", func(t *testing.T) {
		if err := q.Delete(ctx, "test-q", "msg-1"); err != nil {
			t.Errorf("Delete returned error: %v", err)
		}
		if err := q.Poison(ctx, "test-q", "msg-1"); err != nil {
			t.Errorf("Poison returned error: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// In-memory TenantProvider Contract Tests
// ---------------------------------------------------------------------------

// contractMockTenantProvider implements providers.TenantProvider with in-memory maps.
type contractMockTenantProvider struct {
	tenants map[string]*domain.Tenant
	slugs   map[string]*domain.Tenant
}

func newContractMockTenantProvider() *contractMockTenantProvider {
	return &contractMockTenantProvider{
		tenants: make(map[string]*domain.Tenant),
		slugs:   make(map[string]*domain.Tenant),
	}
}

func (m *contractMockTenantProvider) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	t, ok := m.tenants[id]
	if !ok {
		return nil, io.EOF
	}
	return t, nil
}

func (m *contractMockTenantProvider) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	t, ok := m.slugs[slug]
	if !ok {
		return nil, io.EOF
	}
	return t, nil
}

func (m *contractMockTenantProvider) CreateTenant(_ context.Context, tenant *domain.Tenant) error {
	m.tenants[tenant.ID] = tenant
	m.slugs[tenant.Slug] = tenant
	return nil
}

func (m *contractMockTenantProvider) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	var result []*domain.Tenant
	for _, t := range m.tenants {
		result = append(result, t)
	}
	return result, nil
}

func (m *contractMockTenantProvider) UpdateTenantStatus(_ context.Context, id, status string) error {
	t, ok := m.tenants[id]
	if !ok {
		return io.EOF
	}
	t.Status = status
	return nil
}

func TestContract_TenantProvider_CRUD(t *testing.T) {
	p := newContractMockTenantProvider()
	ctx := context.Background()

	t.Run("Create_and_GetTenant", func(t *testing.T) {
		tenant := domain.NewTenant("tenant-c1", "Contract Corp", "contract-corp")
		if err := p.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant failed: %v", err)
		}

		got, err := p.GetTenant(ctx, "tenant-c1")
		if err != nil {
			t.Fatalf("GetTenant failed: %v", err)
		}
		if got.Name != "Contract Corp" {
			t.Errorf("Name = %q, want Contract Corp", got.Name)
		}
		if got.Slug != "contract-corp" {
			t.Errorf("Slug = %q, want contract-corp", got.Slug)
		}
		if got.Plan != "starter" {
			t.Errorf("Plan = %q, want starter", got.Plan)
		}
		if got.Status != "active" {
			t.Errorf("Status = %q, want active", got.Status)
		}
	})

	t.Run("GetTenantBySlug", func(t *testing.T) {
		got, err := p.GetTenantBySlug(ctx, "contract-corp")
		if err != nil {
			t.Fatalf("GetTenantBySlug failed: %v", err)
		}
		if got.ID != "tenant-c1" {
			t.Errorf("ID = %q, want tenant-c1", got.ID)
		}
	})

	t.Run("GetTenantBySlug_not_found", func(t *testing.T) {
		_, err := p.GetTenantBySlug(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown slug, got nil")
		}
	})

	t.Run("ListTenants", func(t *testing.T) {
		// Create a second tenant.
		tenant2 := domain.NewTenant("tenant-c2", "Beta LLC", "beta-llc")
		if err := p.CreateTenant(ctx, tenant2); err != nil {
			t.Fatalf("CreateTenant failed: %v", err)
		}

		tenants, err := p.ListTenants(ctx)
		if err != nil {
			t.Fatalf("ListTenants failed: %v", err)
		}
		if len(tenants) != 2 {
			t.Errorf("expected 2 tenants, got %d", len(tenants))
		}
	})

	t.Run("UpdateTenantStatus", func(t *testing.T) {
		if err := p.UpdateTenantStatus(ctx, "tenant-c1", "suspended"); err != nil {
			t.Fatalf("UpdateTenantStatus failed: %v", err)
		}

		got, err := p.GetTenant(ctx, "tenant-c1")
		if err != nil {
			t.Fatalf("GetTenant after update failed: %v", err)
		}
		if got.Status != "suspended" {
			t.Errorf("Status = %q, want suspended", got.Status)
		}
	})

	t.Run("UpdateTenantStatus_not_found", func(t *testing.T) {
		err := p.UpdateTenantStatus(ctx, "nonexistent", "suspended")
		if err == nil {
			t.Fatal("expected error for unknown tenant, got nil")
		}
	})

	t.Run("GetTenant_not_found", func(t *testing.T) {
		_, err := p.GetTenant(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown tenant, got nil")
		}
	})
}
