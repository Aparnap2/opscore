package golden_test

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
// In-memory providers for golden tests
// ---------------------------------------------------------------------------

type goldMockDB struct {
	mu          sync.Mutex
	jobs        map[string]*domain.Job
	vendors     map[string]*domain.Vendor
	documents   map[string]*domain.Document
	hitlReqs    map[string]*domain.HITLRequest
	auditEvents []*domain.AuditEvent
}

func newGoldMockDB() *goldMockDB {
	return &goldMockDB{
		jobs:      make(map[string]*domain.Job),
		vendors:   make(map[string]*domain.Vendor),
		documents: make(map[string]*domain.Document),
		hitlReqs:  make(map[string]*domain.HITLRequest),
	}
}

func (m *goldMockDB) UpsertJob(_ context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return nil
}
func (m *goldMockDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
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
func (m *goldMockDB) ListJobs(_ context.Context, tenantID string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
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
func (m *goldMockDB) UpsertVendor(_ context.Context, v *domain.Vendor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vendors[v.ID] = v
	return nil
}
func (m *goldMockDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return v, nil
}
func (m *goldMockDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
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
func (m *goldMockDB) UpsertDocument(_ context.Context, d *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[d.ID] = d
	return nil
}
func (m *goldMockDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.documents[id]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return d, nil
}
func (m *goldMockDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, nil
}
func (m *goldMockDB) UpsertHITLRequest(_ context.Context, r *domain.HITLRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitlReqs[r.ID] = r
	return nil
}
func (m *goldMockDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hitlReqs[id]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}
func (m *goldMockDB) ListPendingHITL(_ context.Context, _ string) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *goldMockDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (m *goldMockDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditEvents = append(m.auditEvents, e)
	return nil
}
func (m *goldMockDB) ListAuditEvents(_ context.Context, _, _, _ string, _ int) ([]*domain.AuditEvent, error) {
	return nil, nil
}
func (m *goldMockDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (m *goldMockDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (m *goldMockDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

type goldMockStorage struct{}

func (m *goldMockStorage) Upload(_ context.Context, container, key string, _ io.Reader, _ string) (string, error) {
	return fmt.Sprintf("https://storage.local/%s/%s", container, key), nil
}
func (m *goldMockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not impl")
}
func (m *goldMockStorage) Delete(_ context.Context, _, _ string) error { return nil }
func (m *goldMockStorage) List(_ context.Context, _, _ string) ([]providers.BlobItem, error) {
	return nil, nil
}

type goldMockQueue struct{}

func (m *goldMockQueue) Enqueue(_ context.Context, _ string, _ any) (string, error) {
	return "msg-1", nil
}
func (m *goldMockQueue) Dequeue(_ context.Context, _ string) (*providers.QueueMessage, error) {
	return nil, nil
}
func (m *goldMockQueue) Delete(_ context.Context, _, _ string) error { return nil }
func (m *goldMockQueue) Poison(_ context.Context, _, _ string) error { return nil }

type goldMockLLM struct{}

func (m *goldMockLLM) ExtractFields(_ context.Context, _ string, _ any) (json.RawMessage, float64, error) {
	return json.RawMessage("{}"), 1.0, nil
}
func (m *goldMockLLM) Reason(_ context.Context, prompt string) (string, error) {
	return "golden analysis", nil
}
func (m *goldMockLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, *json.RawMessage, error) {
	return "", nil, nil
}

// ---------------------------------------------------------------------------
// Document ingestion golden scenarios
// ---------------------------------------------------------------------------

func TestGolden_DocumentIngestion(t *testing.T) {
	// Table-driven golden scenarios for document ingestion
	t.Log("=== Test: Golden document ingestion scenarios ===")

	ocr := &agentic.StubOCR{}
	db := newGoldMockDB()
	storage := &goldMockStorage{}
	queue := &goldMockQueue{}
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewDocumentAgent(storage, queue, db, ocr, validator, tracer)

	tests := []struct {
		name           string
		blobURL        string
		filename       string
		wantDocType    string
		wantConfidence float64
		wantNeedsHITL  bool
		wantError      bool
		wantTrustTier  domain.TrustTier
	}{
		{
			name:           "Clean invoice",
			blobURL:        "http://example.com/confidence_0.95/invoice_2024.pdf",
			filename:       "invoice_2024.pdf",
			wantDocType:    "INVOICE",
			wantConfidence: 0.95,
			wantNeedsHITL:  false,
			wantError:      false,
			wantTrustTier:  domain.TrustTierStandard,
		},
		{
			name:           "Blurry invoice",
			blobURL:        "http://example.com/confidence_0.45/blurry_invoice.pdf",
			filename:       "blurry_invoice.pdf",
			wantDocType:    "INVOICE",
			wantConfidence: 0.45,
			wantNeedsHITL:  true,
			wantError:      false,
			wantTrustTier:  domain.TrustTierProbation,
		},
		{
			name:      "Corrupted file",
			blobURL:   "http://example.com/trigger_error/corrupted.pdf",
			filename:  "corrupted.pdf",
			wantError: true,
		},
		{
			name:           "Invalid GST",
			blobURL:        "http://example.com/key_values_error/invoice.pdf",
			filename:       "invoice.pdf",
			wantDocType:    "INVOICE",
			wantConfidence: 0.90,
			wantNeedsHITL:  true,
			wantError:      false,
			wantTrustTier:  domain.TrustTierProbation,
		},
		{
			name:           "Contract doc",
			blobURL:        "http://example.com/confidence_0.95/contract.pdf",
			filename:       "service-contract.pdf",
			wantDocType:    "CONTRACT",
			wantConfidence: 0.95,
			wantNeedsHITL:  false,
			wantError:      false,
			wantTrustTier:  domain.TrustTierStandard,
		},
		{
			name:           "PO doc",
			blobURL:        "http://example.com/confidence_0.95/po.pdf",
			filename:       "po-2024.pdf",
			wantDocType:    "PURCHASE_ORDER",
			wantConfidence: 0.95,
			wantNeedsHITL:  false,
			wantError:      false,
			wantTrustTier:  domain.TrustTierStandard,
		},
		{
			name:           "Unknown doc",
			blobURL:        "http://example.com/confidence_0.95/unknown.txt",
			filename:       "unknown.txt",
			wantDocType:    "OTHER",
			wantConfidence: 0.95,
			wantNeedsHITL:  false,
			wantError:      false,
			wantTrustTier:  domain.TrustTierStandard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &agents.DocumentJob{
				TenantID: "tenant-gold-doc",
				JobID:    "gold-doc-" + tt.name,
				BlobURL:  tt.blobURL,
				FileName: tt.filename,
				Type:     "DOCUMENT",
				JobType:  "ocr",
			}

			result, err := agent.ProcessDocument(context.Background(), job)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error for %s, got nil", tt.name)
				}
				return
			}

			if err != nil {
				t.Fatalf("ProcessDocument failed for %s: %v", tt.name, err)
			}

			// Assert document type
			if got := result["document_type"].(string); got != tt.wantDocType {
				t.Errorf("document_type = %q, want %q", got, tt.wantDocType)
			}

			// Assert confidence
			if got := result["confidence"].(float64); got != tt.wantConfidence {
				t.Errorf("confidence = %f, want %f", got, tt.wantConfidence)
			}

			// Assert needs_hitl
			if got := result["needs_hitl"].(bool); got != tt.wantNeedsHITL {
				t.Errorf("needs_hitl = %v, want %v", got, tt.wantNeedsHITL)
			}

			t.Logf("✅ %s: type=%s, conf=%.2f, hitl=%v", tt.name, tt.wantDocType, tt.wantConfidence, tt.wantNeedsHITL)
		})
	}
}

// ---------------------------------------------------------------------------
// Vendor onboarding golden scenarios
// ---------------------------------------------------------------------------

func TestGolden_VendorOnboarding(t *testing.T) {
	// Table-driven golden scenarios for vendor onboarding
	t.Log("=== Test: Golden vendor onboarding scenarios ===")

	llm := &goldMockLLM{}
	db := newGoldMockDB()
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	agent := agents.NewVendorAgent(db, validator, llm, tracer)

	tests := []struct {
		name            string
		gst             string
		pan             string
		ifsc            string
		documents       []string
		wantRiskScore   int
		wantTrustTier   domain.TrustTier
		wantNeedsHITL   bool
		wantLLMAnalysis bool
	}{
		{
			name:            "Clean vendor",
			gst:             "22AAAAA0000A1Z5",
			pan:             "ABCDE1234F",
			ifsc:            "SBIN0001234",
			documents:       []string{},
			wantRiskScore:   65,
			wantTrustTier:   domain.TrustTierProbation,
			wantNeedsHITL:   true,
			wantLLMAnalysis: false,
		},
		{
			name:            "No IDs",
			gst:             "",
			pan:             "",
			ifsc:            "",
			documents:       []string{},
			wantRiskScore:   15,
			wantTrustTier:   domain.TrustTierPreferred,
			wantNeedsHITL:   false,
			wantLLMAnalysis: false,
		},
		{
			name:            "Invalid GST",
			gst:             "bad-gst",
			pan:             "",
			ifsc:            "",
			documents:       []string{},
			wantRiskScore:   15,
			wantTrustTier:   domain.TrustTierStandard,
			wantNeedsHITL:   true,
			wantLLMAnalysis: false,
		},
		{
			name:            "Full vendor with docs",
			gst:             "22AAAAA0000A1Z5",
			pan:             "ABCDE1234F",
			ifsc:            "SBIN0001234",
			documents:       []string{"doc.pdf"},
			wantRiskScore:   65,
			wantTrustTier:   domain.TrustTierProbation,
			wantNeedsHITL:   true,
			wantLLMAnalysis: true,
		},
		{
			name:            "Empty vendor",
			gst:             "",
			pan:             "",
			ifsc:            "",
			documents:       []string{},
			wantRiskScore:   15,
			wantTrustTier:   domain.TrustTierPreferred,
			wantNeedsHITL:   false,
			wantLLMAnalysis: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &agents.VendorJob{
				TenantID: "tenant-gold-vendor",
				JobID:    "gold-vendor-" + tt.name,
				VendorData: &agents.VendorData{
					Name:      "Golden Vendor " + tt.name,
					GSTNumber: tt.gst,
					PANNumber: tt.pan,
					IFSCCode:  tt.ifsc,
					Documents: tt.documents,
				},
			}

			result, err := agent.ProcessVendor(context.Background(), job)
			if err != nil {
				t.Fatalf("ProcessVendor failed for %s: %v", tt.name, err)
			}

			// Assert risk score
			gotScore, ok := result["risk_score"].(int)
			if !ok {
				t.Fatalf("risk_score not int, got %T", result["risk_score"])
			}
			if gotScore != tt.wantRiskScore {
				t.Errorf("risk_score = %d, want %d", gotScore, tt.wantRiskScore)
			}

			// Assert trust tier
			gotTier, ok := result["trust_tier"].(domain.TrustTier)
			if !ok {
				t.Fatalf("trust_tier not TrustTier, got %T", result["trust_tier"])
			}
			if gotTier != tt.wantTrustTier {
				t.Errorf("trust_tier = %s, want %s", gotTier, tt.wantTrustTier)
			}

			// Assert needs_hitl
			gotHITL, ok := result["needs_hitl"].(bool)
			if !ok {
				t.Fatalf("needs_hitl not bool, got %T", result["needs_hitl"])
			}
			if gotHITL != tt.wantNeedsHITL {
				t.Errorf("needs_hitl = %v, want %v", gotHITL, tt.wantNeedsHITL)
			}

			// Assert llm_analysis
			_, hasAnalysis := result["llm_analysis"]
			if hasAnalysis != tt.wantLLMAnalysis {
				t.Errorf("llm_analysis present = %v, want %v", hasAnalysis, tt.wantLLMAnalysis)
			}

			t.Logf("✅ %s: risk=%d, tier=%s, hitl=%v, llm=%v",
				tt.name, gotScore, gotTier, gotHITL, hasAnalysis)
		})
	}
}

// ---------------------------------------------------------------------------
// Compliance golden scenarios
// ---------------------------------------------------------------------------

func TestGolden_ComplianceSeverity(t *testing.T) {
	// Table-driven golden scenarios for compliance severity classification
	t.Log("=== Test: Golden compliance severity scenarios ===")

	agent := agents.NewComplianceAgent(newGoldMockDB(), domain.NewIndiaValidator(), &goldMockLLM{})
	ctx := context.Background()

	tests := []struct {
		name               string
		title              string
		content            string
		wantSeverity       string
		wantClassification string
	}{
		{
			name:               "Penalty notice",
			title:              "Penalty notice for late filing",
			content:            "This is a penalty notice",
			wantSeverity:       "HIGH",
			wantClassification: "penalty found",
		},
		{
			name:               "Advisory note",
			title:              "Advisory on new regulations",
			content:            "This is an advisory note",
			wantSeverity:       "MEDIUM",
			wantClassification: "advisory found",
		},
		{
			name:               "General update",
			title:              "General update on compliance",
			content:            "This is a general update",
			wantSeverity:       "LOW",
			wantClassification: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, err := agent.ClassifySeverity(ctx, tt.title, tt.content)
			if err != nil {
				t.Fatalf("ClassifySeverity failed: %v", err)
			}
			if severity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", severity, tt.wantSeverity)
			}
			t.Logf("✅ %s: severity=%s", tt.name, severity)
		})
	}
}

// --- Manufacturing pivot (Phase 1.4A) mock stubs (not exercised by this test) ---

func (m *goldMockDB) UpsertPurchaseOrder(_ context.Context, _ *domain.PurchaseOrder) error {
	return nil
}
func (m *goldMockDB) GetPurchaseOrderByID(_ context.Context, _, _ string) (*domain.PurchaseOrder, error) {
	return nil, fmt.Errorf("not found")
}
func (m *goldMockDB) ListPurchaseOrders(_ context.Context, _ string, _, _ int) ([]*domain.PurchaseOrder, error) {
	return nil, nil
}
func (m *goldMockDB) UpsertGoodsReceipt(_ context.Context, _ *domain.GoodsReceipt) error { return nil }
func (m *goldMockDB) GetGoodsReceiptByID(_ context.Context, _, _ string) (*domain.GoodsReceipt, error) {
	return nil, fmt.Errorf("not found")
}
func (m *goldMockDB) ListGoodsReceipts(_ context.Context, _, _ string, _, _ int) ([]*domain.GoodsReceipt, error) {
	return nil, nil
}
func (m *goldMockDB) UpsertInvoice(_ context.Context, _ *domain.Invoice) error { return nil }
func (m *goldMockDB) GetInvoiceByID(_ context.Context, _, _ string) (*domain.Invoice, error) {
	return nil, fmt.Errorf("not found")
}
func (m *goldMockDB) ListInvoices(_ context.Context, _, _ string, _, _ int) ([]*domain.Invoice, error) {
	return nil, nil
}
func (m *goldMockDB) UpsertExceptionCase(_ context.Context, _ *domain.ExceptionCase) error {
	return nil
}
func (m *goldMockDB) GetExceptionCaseByID(_ context.Context, _, _ string) (*domain.ExceptionCase, error) {
	return nil, fmt.Errorf("not found")
}
func (m *goldMockDB) ListExceptionCases(_ context.Context, _, _, _ string, _, _ int) ([]*domain.ExceptionCase, error) {
	return nil, nil
}
func (m *goldMockDB) UpdateExceptionCaseStatus(_ context.Context, _, _, _ string) error { return nil }
