package rag_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
	agentic "github.com/aparna/opscore/tests/agentic"
)

// ---------------------------------------------------------------------------
// In-memory providers for structured output tests
// ---------------------------------------------------------------------------

type soMockDB struct {
	mu          sync.Mutex
	jobs        map[string]*domain.Job
	vendors     map[string]*domain.Vendor
	documents   map[string]*domain.Document
	hitlReqs    map[string]*domain.HITLRequest
	auditEvents []*domain.AuditEvent
}

func newSOMockDB() *soMockDB {
	return &soMockDB{
		jobs:      make(map[string]*domain.Job),
		vendors:   make(map[string]*domain.Vendor),
		documents: make(map[string]*domain.Document),
		hitlReqs:  make(map[string]*domain.HITLRequest),
	}
}

func (m *soMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return nil
}
func (m *soMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
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
func (m *soMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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
func (m *soMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[v.ID] = v
	return nil
}
func (m *soMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return v, nil
}
func (m *soMockDB) ListVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *soMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[d.ID] = d
	return nil
}
func (m *soMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return d, nil
}
func (m *soMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *soMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlReqs[r.ID] = r
	return nil
}
func (m *soMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlReqs[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}
func (m *soMockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
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
func (m *soMockDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *soMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *soMockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
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
func (m *soMockDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (m *soMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *soMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

type soMockStorage struct {
	mu sync.Mutex
}

func (m *soMockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	return fmt.Sprintf("https://storage.local/%s/%s", container, key), nil
}
func (m *soMockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not impl")
}
func (m *soMockStorage) Delete(_ context.Context, _, _ string) error { return nil }
func (m *soMockStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

type soMockQueue struct{}

func (m *soMockQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	return "msg-1", nil
}
func (m *soMockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}
func (m *soMockQueue) Delete(_ context.Context, _, _ string) error { return nil }
func (m *soMockQueue) Poison(_ context.Context, _, _ string) error { return nil }

type soMockLLM struct{}

func (m *soMockLLM) ExtractFields(_ context.Context, _ string, _ any) (json.RawMessage, float64, error) {
	return json.RawMessage("{}"), 1.0, nil
}
func (m *soMockLLM) Reason(_ context.Context, prompt string) (string, error) {
	return "analysis result", nil
}
func (m *soMockLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, *json.RawMessage, error) {
	return "", nil, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestStructuredOutput_DocumentAgent_ResultSchema(t *testing.T) {
	// DocumentAgent result map has all expected keys with correct types
	t.Log("=== Test: DocumentAgent result schema ===")

	ocr := &agentic.StubOCR{}
	db := newSOMockDB()
	storage := &soMockStorage{}
	queue := &soMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	job := &agents.DocumentJob{
		TenantID: "tenant-so-1",
		JobID:    "so-doc-1",
		BlobURL:  "http://example.com/confidence_0.95/invoice.pdf",
		FileName: "invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}

	// Assert all expected keys exist
	expectedKeys := []string{"document_type", "text", "confidence", "key_values", "validations", "needs_hitl"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("missing expected key %q in result", key)
		}
	}

	// Assert types
	if _, ok := result["document_type"].(string); !ok {
		t.Errorf("document_type is not string, got %T", result["document_type"])
	}
	if _, ok := result["text"].(string); !ok {
		t.Errorf("text is not string, got %T", result["text"])
	}
	if conf, ok := result["confidence"].(float64); !ok {
		t.Errorf("confidence is not float64, got %T", result["confidence"])
	} else if conf < 0.0 || conf > 1.0 {
		t.Errorf("confidence %f out of range [0,1]", conf)
	}
	if _, ok := result["key_values"].(map[string]string); !ok {
		t.Errorf("key_values is not map[string]string, got %T", result["key_values"])
	}
	if v, ok := result["validations"].(*domain.ValidationResult); !ok || v == nil {
		t.Errorf("validations is not *ValidationResult, got %T", result["validations"])
	}
	if _, ok := result["needs_hitl"].(bool); !ok {
		t.Errorf("needs_hitl is not bool, got %T", result["needs_hitl"])
	}

	t.Logf("✅ DocumentAgent result schema valid: %d keys, all types correct", len(result))
}

func TestStructuredOutput_VendorAgent_ResultSchema(t *testing.T) {
	// VendorAgent result map has all expected keys with correct types
	t.Log("=== Test: VendorAgent result schema ===")

	llm := &soMockLLM{}
	db := newSOMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)

	job := &agents.VendorJob{
		TenantID: "tenant-so-2",
		JobID:    "so-vendor-1",
		VendorData: &agents.VendorData{
			Name:      "Schema Test Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
			Documents: []string{"doc.pdf"},
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}

	// Assert all expected keys
	expectedKeys := []string{"name", "validations", "risk_score", "trust_tier", "needs_hitl"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("missing key %q in result", key)
		}
	}

	// llm_analysis should be present when documents provided and LLM succeeds
	if _, ok := result["llm_analysis"]; !ok {
		t.Error("llm_analysis should be present when documents provided")
	}

	// Type assertions
	if _, ok := result["name"].(string); !ok {
		t.Errorf("name is not string, got %T", result["name"])
	}
	if v, ok := result["validations"].(*domain.ValidationResult); !ok || v == nil {
		t.Errorf("validations is not *ValidationResult, got %T", result["validations"])
	}
	if score, ok := result["risk_score"].(int); !ok {
		t.Errorf("risk_score is not int, got %T", result["risk_score"])
	} else if score < 0 || score > 100 {
		t.Errorf("risk_score %d out of range [0,100]", score)
	}
	if tier, ok := result["trust_tier"].(domain.TrustTier); !ok {
		t.Errorf("trust_tier is not TrustTier, got %T", result["trust_tier"])
	} else if tier != domain.TrustTierProbation && tier != domain.TrustTierStandard &&
		tier != domain.TrustTierPreferred && tier != domain.TrustTierBlocked {
		t.Errorf("trust_tier %q is not a valid TrustTier value", tier)
	}
	if _, ok := result["needs_hitl"].(bool); !ok {
		t.Errorf("needs_hitl is not bool, got %T", result["needs_hitl"])
	}
	if _, ok := result["llm_analysis"].(string); !ok {
		t.Errorf("llm_analysis is not string, got %T", result["llm_analysis"])
	}

	t.Logf("✅ VendorAgent result schema valid: %d keys, all types correct", len(result))
}

func TestStructuredOutput_ValidationsNeverNil(t *testing.T) {
	// validations is always a *domain.ValidationResult (never nil)
	t.Log("=== Test: validations never nil ===")

	ocr := &agentic.StubOCR{}
	db := newSOMockDB()
	storage := &soMockStorage{}
	queue := &soMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	job := &agents.DocumentJob{
		TenantID: "tenant-so-3",
		JobID:    "so-doc-nil",
		BlobURL:  "http://example.com/confidence_0.95/doc.pdf",
		FileName: "doc.pdf",
	}

	result, err := agent.ProcessDocument(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}
	v, ok := result["validations"].(*domain.ValidationResult)
	if !ok {
		t.Fatalf("validations is %T, want *ValidationResult", result["validations"])
	}
	if v == nil {
		t.Fatal("validations is nil")
	}
	t.Logf("✅ validations is non-nil *ValidationResult: errors=%d, warnings=%d", len(v.Errors), len(v.Warnings))
}

func TestStructuredOutput_ConfidenceInRange(t *testing.T) {
	// confidence is always a valid float64 (0.0–1.0)
	t.Log("=== Test: confidence in range [0,1] ===")

	ocr := &agentic.StubOCR{}
	db := newSOMockDB()
	storage := &soMockStorage{}
	queue := &soMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	// Test all confidence paths
	urls := []struct {
		name string
		url  string
		want float64
	}{
		{"high", "http://example.com/confidence_0.95/doc.pdf", 0.95},
		{"low", "http://example.com/confidence_0.45/doc.pdf", 0.45},
		{"default", "http://example.com/normal_doc.pdf", 0.90},
	}

	for _, tt := range urls {
		t.Run(tt.name, func(t *testing.T) {
			job := &agents.DocumentJob{
				TenantID: "tenant-so-4",
				JobID:    "so-conf-" + tt.name,
				BlobURL:  tt.url,
				FileName: "doc.pdf",
			}
			result, err := agent.ProcessDocument(context.Background(), job)
			if err != nil {
				t.Fatalf("ProcessDocument failed: %v", err)
			}
			conf, ok := result["confidence"].(float64)
			if !ok {
				t.Fatalf("confidence not float64, got %T", result["confidence"])
			}
			if conf < 0.0 || conf > 1.0 {
				t.Errorf("confidence %f out of range [0,1]", conf)
			}
			if conf != tt.want {
				t.Errorf("confidence = %f, want %f", conf, tt.want)
			}
		})
	}
	t.Log("✅ All confidence values in [0,1] range")
}

func TestStructuredOutput_NeedsHITLIsBool(t *testing.T) {
	// needs_hitl is always a bool
	t.Log("=== Test: needs_hitl is bool ===")

	ocr := &agentic.StubOCR{}
	db := newSOMockDB()
	storage := &soMockStorage{}
	queue := &soMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	// Auto-complete path
	job1 := &agents.DocumentJob{
		TenantID: "tenant-so-5", JobID: "so-hitl-1",
		BlobURL:  "http://example.com/confidence_0.95/doc.pdf",
		FileName: "invoice.pdf",
	}
	r1, _ := agent.ProcessDocument(context.Background(), job1)
	if _, ok := r1["needs_hitl"].(bool); !ok {
		t.Errorf("needs_hitl not bool, got %T", r1["needs_hitl"])
	}

	// HITL path
	job2 := &agents.DocumentJob{
		TenantID: "tenant-so-5", JobID: "so-hitl-2",
		BlobURL:  "http://example.com/confidence_0.45/doc.pdf",
		FileName: "blurry.pdf",
	}
	r2, _ := agent.ProcessDocument(context.Background(), job2)
	if _, ok := r2["needs_hitl"].(bool); !ok {
		t.Errorf("needs_hitl not bool, got %T", r2["needs_hitl"])
	}

	t.Log("✅ needs_hitl is bool in both paths")
}

func TestStructuredOutput_TrustTierIsValid(t *testing.T) {
	// trust_tier is always a valid domain.TrustTier value
	t.Log("=== Test: trust_tier is valid TrustTier ===")

	llm := &soMockLLM{}
	db := newSOMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)

	validTiers := map[domain.TrustTier]bool{
		domain.TrustTierProbation: true,
		domain.TrustTierStandard:  true,
		domain.TrustTierPreferred: true,
		domain.TrustTierBlocked:   true,
	}

	// Test with empty IDs → score=15 → BLOCKED (risk < 30)
	job := &agents.VendorJob{
		TenantID: "tenant-so-6", JobID: "so-tier-1",
		VendorData: &agents.VendorData{Name: "Tier Test"},
	}
	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	tier, ok := result["trust_tier"].(domain.TrustTier)
	if !ok {
		t.Fatalf("trust_tier not TrustTier, got %T", result["trust_tier"])
	}
	if !validTiers[tier] {
		t.Errorf("trust_tier %q is not a valid TrustTier", tier)
	}
	t.Logf("✅ trust_tier = %q (valid)", tier)
}

func TestStructuredOutput_RiskScoreInRange(t *testing.T) {
	// risk_score is always a valid int (0–100)
	t.Log("=== Test: risk_score in [0,100] ===")

	llm := &soMockLLM{}
	db := newSOMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)

	job := &agents.VendorJob{
		TenantID: "tenant-so-7", JobID: "so-risk-1",
		VendorData: &agents.VendorData{Name: "Risk Test"},
	}
	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	score, ok := result["risk_score"].(int)
	if !ok {
		t.Fatalf("risk_score not int, got %T", result["risk_score"])
	}
	if score < 0 || score > 100 {
		t.Errorf("risk_score %d out of range [0,100]", score)
	}
	t.Logf("✅ risk_score = %d (in range)", score)
}

func TestStructuredOutput_JobStatusTransitions(t *testing.T) {
	// Job status transitions follow the state machine
	t.Log("=== Test: Job status transitions ===")

	// Pure domain test — no containers needed
	t.Run("PENDING_to_PROCESSING_to_COMPLETED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusPending}
		job.Status = domain.JobStatusProcessing
		job.Status = domain.JobStatusCompleted
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
	})

	t.Run("PENDING_to_PROCESSING_to_AWAITING_HITL", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusPending}
		job.Status = domain.JobStatusProcessing
		job.Status = domain.JobStatusAwaitingHITL
		if job.Status.IsTerminal() {
			t.Error("AWAITING_HITL should not be terminal")
		}
	})

	t.Run("AWAITING_HITL_to_COMPLETED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusAwaitingHITL}
		job.Status = domain.JobStatusCompleted
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
	})

	t.Run("AWAITING_HITL_to_FAILED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusAwaitingHITL}
		job.Status = domain.JobStatusFailed
		if !job.Status.IsTerminal() {
			t.Error("FAILED should be terminal")
		}
	})
}

func TestStructuredOutput_VendorIDFormats(t *testing.T) {
	// Vendor IDs (GST/PAN/IFSC) conform to their respective regexes when set
	t.Log("=== Test: Vendor ID formats ===")

	t.Run("valid_GST_format", func(t *testing.T) {
		if !domain.ValidateGST("22AAAAA0000A1Z5") {
			t.Error("ValidateGST returned false for valid GST")
		}
	})
	t.Run("valid_PAN_format", func(t *testing.T) {
		if !domain.ValidatePAN("ABCDE1234F") {
			t.Error("ValidatePAN returned false for valid PAN")
		}
	})
	t.Run("valid_IFSC_format", func(t *testing.T) {
		if !domain.ValidateIFSC("SBIN0001234") {
			t.Error("ValidateIFSC returned false for valid IFSC")
		}
	})
	t.Run("invalid_GST_rejected", func(t *testing.T) {
		if domain.ValidateGST("invalid") {
			t.Error("ValidateGST returned true for invalid GST")
		}
	})
	t.Run("empty_values_skipped", func(t *testing.T) {
		if domain.ValidateGST("") {
			t.Error("ValidateGST should return false for empty string")
		}
	})
	t.Log("✅ All vendor ID format validations correct")
}
