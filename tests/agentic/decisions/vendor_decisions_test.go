package decisions_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// In-memory DB for VendorAgent decision tests
// ---------------------------------------------------------------------------

type vendorMockDB struct {
	mu          sync.Mutex
	jobs        map[string]*domain.Job
	vendors     map[string]*domain.Vendor
	documents   map[string]*domain.Document
	hitlReqs    map[string]*domain.HITLRequest
	auditEvents []*domain.AuditEvent
}

func newVendorMockDB() *vendorMockDB {
	return &vendorMockDB{
		jobs:      make(map[string]*domain.Job),
		vendors:   make(map[string]*domain.Vendor),
		documents: make(map[string]*domain.Document),
		hitlReqs:  make(map[string]*domain.HITLRequest),
	}
}

func (m *vendorMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, exists := m.jobs[job.ID]
	if exists {
		job.Version = existing.Version + 1
	}
	m.jobs[job.ID] = job
	return nil
}
func (m *vendorMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
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
func (m *vendorMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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
func (m *vendorMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[v.ID] = v
	return nil
}
func (m *vendorMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor not found")
	}
	return v, nil
}
func (m *vendorMockDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []*domain.Vendor
	for _, v := range m.vendors {
		if v.TenantID == tenantID {
			r = append(r, v)
		}
	}
	return r, nil
}
func (m *vendorMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[d.ID] = d
	return nil
}
func (m *vendorMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("document not found")
	}
	return d, nil
}
func (m *vendorMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *vendorMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlReqs[r.ID] = r
	return nil
}
func (m *vendorMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlReqs[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("HITL not found")
	}
	return r, nil
}
func (m *vendorMockDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
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
func (m *vendorMockDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *vendorMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *vendorMockDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
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
func (m *vendorMockDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (m *vendorMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *vendorMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

// vendorMockLLM implements providers.LLMProvider with configurable responses.
type vendorMockLLM struct {
	mu         sync.Mutex
	reasonFunc func(ctx context.Context, prompt string) (string, error)
	callCount  int
}

func newVendorMockLLM() *vendorMockLLM {
	return &vendorMockLLM{
		reasonFunc: func(_ context.Context, prompt string) (string, error) {
			return "default analysis", nil
		},
	}
}

func (m *vendorMockLLM) ExtractFields(_ context.Context, _ string, _ any) (json.RawMessage, float64, error) {
	return json.RawMessage("{}"), 1.0, nil
}
func (m *vendorMockLLM) Reason(ctx context.Context, prompt string) (string, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return m.reasonFunc(ctx, prompt)
}
func (m *vendorMockLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, *json.RawMessage, error) {
	return "", nil, nil
}

func newVendorTestAgent(llm providers.LLMProvider) (*agents.VendorAgent, *vendorMockDB) {
	db := newVendorMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)
	return agent, db
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestVendorAgent_AllValidIDs_RiskScore65_NeedsHITL(t *testing.T) {
	// Valid GST+PAN+IFSC → score=65, needs_hitl=true (score >= 60)
	t.Log("=== Test: All valid IDs → risk=65, needs HITL ===")

	llm := newVendorMockLLM()
	agent, db := newVendorTestAgent(llm)

	job := &agents.VendorJob{
		TenantID: "tenant-vdec-1",
		JobID:    "vdec-test-1",
		VendorData: &agents.VendorData{
			Name:      "Valid Vendor Ltd",
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			IFSCCode:  "SBIN0001234",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	riskScore, _ := result["risk_score"].(int)
	if riskScore != 65 {
		t.Errorf("risk_score = %d, want 65", riskScore)
	}
	needsHITL, _ := result["needs_hitl"].(bool)
	if !needsHITL {
		t.Errorf("needs_hitl = false, want true (risk 65 >= 60)")
	}
	// HITL request should exist
	_, hitlErr := db.GetHITLRequest(context.Background(), "hitl-vdec-test-1", "tenant-vdec-1")
	if hitlErr != nil {
		t.Errorf("expected HITL request, got error: %v", hitlErr)
	}
	t.Logf("✅ All valid IDs: risk=%d, needs_hitl=%v", riskScore, needsHITL)
}

func TestVendorAgent_EmptyAllIDs_RiskScore15_AutoApproved(t *testing.T) {
	// Empty IDs → score=15, needs_hitl=false, trust_tier=PREFERRED
	t.Log("=== Test: Empty IDs → risk=15, auto-approved ===")

	llm := newVendorMockLLM()
	agent, db := newVendorTestAgent(llm)

	job := &agents.VendorJob{
		TenantID: "tenant-vdec-2",
		JobID:    "vdec-test-2",
		VendorData: &agents.VendorData{
			Name: "Small Trader",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	riskScore, _ := result["risk_score"].(int)
	if riskScore != 15 {
		t.Errorf("risk_score = %d, want 15", riskScore)
	}
	needsHITL, _ := result["needs_hitl"].(bool)
	if needsHITL {
		t.Errorf("needs_hitl = true, want false (risk 15 < 60)")
	}
	trustTier, ok := result["trust_tier"].(domain.TrustTier)
	if !ok || trustTier != domain.TrustTierPreferred {
		t.Errorf("trust_tier = %v, want PREFERRED", result["trust_tier"])
	}
	// Vendor should be auto-approved
	vendor, err := db.GetVendor(context.Background(), "vdec-test-2", "tenant-vdec-2")
	if err != nil {
		t.Fatalf("GetVendor failed: %v", err)
	}
	if !vendor.Approved {
		t.Error("vendor should be approved (risk 15 < 60, no validation errors)")
	}
	t.Logf("✅ Empty IDs: risk=%d, needs_hitl=%v, tier=%s, approved=%v", riskScore, needsHITL, trustTier, vendor.Approved)
}

func TestVendorAgent_InvalidIDs_ValidationErrors_TriggersHITL(t *testing.T) {
	// Invalid GST/PAN/IFSC → validation errors, needs_hitl=true, HITLRequest created
	t.Log("=== Test: Invalid IDs → validation errors + HITL ===")

	llm := newVendorMockLLM()
	agent, db := newVendorTestAgent(llm)

	job := &agents.VendorJob{
		TenantID: "tenant-vdec-3",
		JobID:    "vdec-test-3",
		VendorData: &agents.VendorData{
			Name:      "Risky Vendor",
			GSTNumber: "INVALID_GST_123",
			PANNumber: "SHORT",
			IFSCCode:  "BAD",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok || validations == nil {
		t.Fatal("validations missing from result")
	}
	if !validations.HasErrors() {
		t.Error("expected validation errors for invalid IDs")
	}
	if len(validations.Errors) != 3 {
		t.Errorf("expected 3 validation errors, got %d: %v", len(validations.Errors), validations.Errors)
	}
	needsHITL, _ := result["needs_hitl"].(bool)
	if !needsHITL {
		t.Errorf("needs_hitl = false, want true (validation errors present)")
	}
	// HITL request should exist
	hitlReq, err := db.GetHITLRequest(context.Background(), "hitl-vdec-test-3", "tenant-vdec-3")
	if err != nil {
		t.Fatalf("expected HITL request: %v", err)
	}
	if hitlReq.Status != domain.HITLStatusPending {
		t.Errorf("HITL status = %s, want PENDING", hitlReq.Status)
	}
	t.Logf("✅ Invalid IDs: errors=%v, needs_hitl=%v, HITL=%s", validations.Errors, needsHITL, hitlReq.Status)
}

func TestVendorAgent_HasDocuments_LLMAnalysisIncluded(t *testing.T) {
	// Documents provided → LLM Reason() called, llm_analysis in result
	t.Log("=== Test: Documents → LLM analysis included ===")

	llm := newVendorMockLLM()
	llm.reasonFunc = func(_ context.Context, prompt string) (string, error) {
		return "vendor documents look clean", nil
	}
	agent, _ := newVendorTestAgent(llm)

	job := &agents.VendorJob{
		TenantID: "tenant-vdec-4",
		JobID:    "vdec-test-4",
		VendorData: &agents.VendorData{
			Name:      "Doc Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
			Documents: []string{"gst_cert.pdf", "bank_stmt.pdf"},
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	analysis, hasAnalysis := result["llm_analysis"]
	if !hasAnalysis {
		t.Fatal("llm_analysis missing from result")
	}
	if analysis != "vendor documents look clean" {
		t.Errorf("llm_analysis = %v, want 'vendor documents look clean'", analysis)
	}
	if llm.callCount != 1 {
		t.Errorf("LLM called %d times, want 1", llm.callCount)
	}
	t.Logf("✅ LLM analysis: %v", analysis)
}

func TestVendorAgent_LLMFailure_DoesNotCrash_SavesVendor(t *testing.T) {
	// LLM returns error → vendor still saved, no llm_analysis key
	t.Log("=== Test: LLM failure → vendor saved, no analysis ===")

	llm := newVendorMockLLM()
	llm.reasonFunc = func(_ context.Context, prompt string) (string, error) {
		return "", fmt.Errorf("LLM API unavailable")
	}
	agent, db := newVendorTestAgent(llm)

	job := &agents.VendorJob{
		TenantID: "tenant-vdec-5",
		JobID:    "vdec-test-5",
		VendorData: &agents.VendorData{
			Name:      "LLM Fail Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
			Documents: []string{"doc.pdf"},
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor should not crash on LLM error: %v", err)
	}
	_, hasAnalysis := result["llm_analysis"]
	if hasAnalysis {
		t.Error("llm_analysis should be absent when LLM fails")
	}
	// Vendor should still be saved
	vendor, err := db.GetVendor(context.Background(), "vdec-test-5", "tenant-vdec-5")
	if err != nil {
		t.Fatalf("vendor should be saved despite LLM error: %v", err)
	}
	if vendor.Name != "LLM Fail Vendor" {
		t.Errorf("vendor name = %q, want 'LLM Fail Vendor'", vendor.Name)
	}
	t.Logf("✅ LLM failure: vendor saved=%s, llm_analysis absent", vendor.Name)
}

func TestVendorAgent_HighRiskScore_TrustTierProbation(t *testing.T) {
	// riskScore >= 60 → result trust_tier stays PROBATION (default, none of the <60 conditions match)
	t.Log("=== Test: High risk score → PROBATION (default) ===")

	llm := newVendorMockLLM()
	agent, _ := newVendorTestAgent(llm)

	// Valid IDs → score=65 (>= 60), none of the <60 conditions match, default=PROBATION
	job := &agents.VendorJob{
		TenantID: "tenant-vdec-6",
		JobID:    "vdec-test-6",
		VendorData: &agents.VendorData{
			Name:      "Probation Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			IFSCCode:  "SBIN0001234",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	riskScore, _ := result["risk_score"].(int)
	trustTier, ok := result["trust_tier"].(domain.TrustTier)
	if !ok {
		t.Fatalf("trust_tier not a TrustTier, got %T", result["trust_tier"])
	}
	// risk 65 >= 60 → none of the <60 branches match → default PROBATION
	if trustTier != domain.TrustTierProbation {
		t.Errorf("trust_tier = %s, want PROBATION for risk >= 60 (default)", trustTier)
	}
	t.Logf("✅ High risk: score=%d, tier=%s", riskScore, trustTier)
}

func TestVendorAgent_LowRiskScore_TrustTierPreferred(t *testing.T) {
	// riskScore < 30 && validationErrors == 0 → TrustTierPreferred
	t.Log("=== Test: Low risk score → PREFERRED tier ===")

	llm := newVendorMockLLM()
	agent, _ := newVendorTestAgent(llm)

	// Empty IDs → score=15 (< 30, no validation errors) → PREFERRED
	job := &agents.VendorJob{
		TenantID: "tenant-vdec-7",
		JobID:    "vdec-test-7",
		VendorData: &agents.VendorData{
			Name: "Preferred Vendor",
		},
	}

	result, err := agent.ProcessVendor(context.Background(), job)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}
	riskScore, _ := result["risk_score"].(int)
	trustTier, ok := result["trust_tier"].(domain.TrustTier)
	if !ok {
		t.Fatalf("trust_tier not a TrustTier, got %T", result["trust_tier"])
	}
	if trustTier != domain.TrustTierPreferred {
		t.Errorf("trust_tier = %s, want PREFERRED for risk < 30 with no validation errors", trustTier)
	}
	t.Logf("✅ Low risk: score=%d, tier=%s", riskScore, trustTier)
}

func TestVendorAgent_CheckDuplicate_FindsExisting_ReturnsTrue(t *testing.T) {
	// Duplicate name → true
	t.Log("=== Test: CheckDuplicate finds existing ===")

	llm := newVendorMockLLM()
	agent, db := newVendorTestAgent(llm)

	// Pre-populate a vendor
	existing := &domain.Vendor{
		ID:       "existing-vendor",
		TenantID: "tenant-vdec-8",
		Name:     "Existing Corp",
	}
	if err := db.UpsertVendor(context.Background(), existing); err != nil {
		t.Fatalf("pre-populate vendor: %v", err)
	}

	dup, err := agent.CheckDuplicate(context.Background(), "tenant-vdec-8", "Existing Corp")
	if err != nil {
		t.Fatalf("CheckDuplicate failed: %v", err)
	}
	if !dup {
		t.Error("CheckDuplicate = false, want true for existing name")
	}
	t.Logf("✅ Duplicate check: found=%v", dup)
}

func TestVendorAgent_CheckDuplicate_NoMatch_ReturnsFalse(t *testing.T) {
	// New name → false
	t.Log("=== CheckDuplicate: no match ===")

	llm := newVendorMockLLM()
	agent, _ := newVendorTestAgent(llm)

	dup, err := agent.CheckDuplicate(context.Background(), "tenant-vdec-9", "New Vendor")
	if err != nil {
		t.Fatalf("CheckDuplicate failed: %v", err)
	}
	if dup {
		t.Error("CheckDuplicate = true, want false for new name")
	}
	t.Logf("✅ No duplicate: found=%v", dup)
}
