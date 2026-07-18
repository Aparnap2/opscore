//go:build agentic

// Package isolation tests that tenants cannot access each other's data
// through the DBProvider interface. These tests verify tenant isolation
// at the data access layer using an in-memory mock.
//
// NOTE: GetVendor, GetDocument, and GetHITLRequest now accept a tenantID
// parameter and enforce tenant isolation at the mock level, matching the
// updated DBProvider interface and postgres adapter.
package isolation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// inMemoryDB — thread-safe in-memory DBProvider for cross-tenant isolation tests
// ---------------------------------------------------------------------------

// inMemoryDB implements providers.DBProvider with tenant-aware maps.
// Every listing/query/find method enforces tenant isolation by filtering
// on the tenantID parameter. Methods that lack a tenantID parameter
// (GetVendor, GetDocument, GetHITLRequest) return data without tenant
// filtering — mirroring the postgres adapter's behaviour.
type inMemoryDB struct {
	mu sync.RWMutex

	jobs       map[string]*domain.Job
	vendors    map[string]*domain.Vendor
	documents  map[string]*domain.Document
	hitlReqs   map[string]*domain.HITLRequest
	auditEvent []*domain.AuditEvent
	compliance []*domain.ComplianceRecord
}

// newInMemoryDB creates an empty inMemoryDB.
func newInMemoryDB() *inMemoryDB {
	return &inMemoryDB{
		jobs:       make(map[string]*domain.Job),
		vendors:    make(map[string]*domain.Vendor),
		documents:  make(map[string]*domain.Document),
		hitlReqs:   make(map[string]*domain.HITLRequest),
		auditEvent: make([]*domain.AuditEvent, 0),
		compliance: make([]*domain.ComplianceRecord, 0),
	}
}

// compile-time check
var _ providers.DBProvider = (*inMemoryDB)(nil)

// -- Jobs -------------------------------------------------------------------

func (d *inMemoryDB) UpsertJob(_ context.Context, job *domain.Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jobs[job.ID] = job
	return nil
}

func (d *inMemoryDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	job, ok := d.jobs[id]
	if !ok || job.TenantID != tenantID {
		return nil, fmt.Errorf("job %s not found", id)
	}
	return job, nil
}

func (d *inMemoryDB) ListJobs(_ context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.Job
	for _, j := range d.jobs {
		if j.TenantID != tenantID {
			continue
		}
		if workflowType != "" && j.WorkflowType != workflowType {
			continue
		}
		if status != "" && j.Status != status {
			continue
		}
		result = append(result, j)
	}
	return result, nil
}

// -- Vendors ----------------------------------------------------------------

func (d *inMemoryDB) UpsertVendor(_ context.Context, vendor *domain.Vendor) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.vendors[vendor.ID] = vendor
	return nil
}

// GetVendor retrieves a vendor by id and tenantID, enforcing tenant isolation.
func (d *inMemoryDB) GetVendor(_ context.Context, id, tenantID string) (*domain.Vendor, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v, ok := d.vendors[id]
	if !ok || v.TenantID != tenantID {
		return nil, fmt.Errorf("vendor %s not found", id)
	}
	return v, nil
}

func (d *inMemoryDB) ListVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.Vendor
	for _, v := range d.vendors {
		if v.TenantID == tenantID {
			result = append(result, v)
		}
	}
	return result, nil
}

// -- Documents --------------------------------------------------------------

func (d *inMemoryDB) UpsertDocument(_ context.Context, doc *domain.Document) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.documents[doc.ID] = doc
	return nil
}

// GetDocument retrieves a document by id and tenantID, enforcing tenant isolation.
func (d *inMemoryDB) GetDocument(_ context.Context, id, tenantID string) (*domain.Document, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	doc, ok := d.documents[id]
	if !ok || doc.TenantID != tenantID {
		return nil, fmt.Errorf("document %s not found", id)
	}
	return doc, nil
}

func (d *inMemoryDB) FindBySHA256(_ context.Context, tenantID, contentHash string) (*domain.Document, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, doc := range d.documents {
		if doc.TenantID == tenantID && doc.ContentHash == contentHash {
			return doc, nil
		}
	}
	return nil, nil
}

// -- HITL Requests ---------------------------------------------------------

func (d *inMemoryDB) UpsertHITLRequest(_ context.Context, req *domain.HITLRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hitlReqs[req.ID] = req
	return nil
}

// GetHITLRequest retrieves a HITL request by id and tenantID, enforcing tenant isolation.
func (d *inMemoryDB) GetHITLRequest(_ context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	req, ok := d.hitlReqs[id]
	if !ok || req.TenantID != tenantID {
		return nil, fmt.Errorf("HITL request %s not found", id)
	}
	return req, nil
}

func (d *inMemoryDB) ListPendingHITL(_ context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.HITLRequest
	for _, req := range d.hitlReqs {
		if req.TenantID == tenantID && req.Status == domain.HITLStatusPending {
			result = append(result, req)
		}
	}
	return result, nil
}

// ListHITLRequests lists HITL requests for a tenant with optional status
// filtering and pagination, enforcing tenant isolation.
func (d *inMemoryDB) ListHITLRequests(_ context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.HITLRequest
	for _, req := range d.hitlReqs {
		if req.TenantID != tenantID {
			continue
		}
		if status != "" && req.Status != domain.HITLRequestStatus(status) {
			continue
		}
		result = append(result, req)
	}
	if offset > 0 && len(result) > offset {
		result = result[offset:]
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// -- Audit Events ----------------------------------------------------------

func (d *inMemoryDB) AppendAuditEvent(_ context.Context, event *domain.AuditEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	d.auditEvent = append(d.auditEvent, event)
	return nil
}

func (d *inMemoryDB) ListAuditEvents(_ context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var filtered []*domain.AuditEvent
	for _, ev := range d.auditEvent {
		if ev.TenantID != tenantID {
			continue
		}
		if targetType != "" && ev.TargetType != targetType {
			continue
		}
		if targetID != "" && ev.TargetID != targetID {
			continue
		}
		filtered = append(filtered, ev)
	}
	// Return in reverse order (newest first) to match DB ordering.
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// -- Ops endpoints ---------------------------------------------------------

func (d *inMemoryDB) GetRecentJobs(_ context.Context, tenantID string, limit int) ([]*domain.Job, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.Job
	for _, j := range d.jobs {
		if j.TenantID == tenantID {
			result = append(result, j)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (d *inMemoryDB) GetRiskyVendors(_ context.Context, tenantID string) ([]*domain.Vendor, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.Vendor
	for _, v := range d.vendors {
		if v.TenantID == tenantID && (v.RiskScore < 30 || v.TrustBattery.Tier == domain.TrustTierProbation || v.TrustBattery.Tier == domain.TrustTierBlocked) {
			result = append(result, v)
		}
	}
	return result, nil
}

func (d *inMemoryDB) GetRecentCompliance(_ context.Context, tenantID string, limit int) ([]*domain.ComplianceRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []*domain.ComplianceRecord
	for _, r := range d.compliance {
		if r.TenantID == tenantID {
			result = append(result, r)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// -- UsageProvider methods (unused in these tests) -------------------------

func (d *inMemoryDB) IncrementUsage(_ context.Context, _ string, _ domain.Metric, _ int64) error {
	return nil
}

func (d *inMemoryDB) GetUsage(_ context.Context, _ string, _ domain.Metric) (int64, error) {
	return 0, nil
}

func (d *inMemoryDB) GetCurrentPeriodUsage(_ context.Context, _ string) (map[domain.Metric]int64, error) {
	return make(map[domain.Metric]int64), nil
}

func (d *inMemoryDB) CheckLimit(_ context.Context, _ string, _ domain.Metric) (bool, int64, int64, error) {
	return true, 0, 0, nil
}

// -- TenantProvider methods (unused in these tests) ------------------------

func (d *inMemoryDB) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	return domain.NewTenant(id, id, id), nil
}

func (d *inMemoryDB) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	return domain.NewTenant(slug, slug, slug), nil
}

func (d *inMemoryDB) CreateTenant(_ context.Context, _ *domain.Tenant) error {
	return nil
}

func (d *inMemoryDB) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	return []*domain.Tenant{}, nil
}

func (d *inMemoryDB) UpdateTenantStatus(_ context.Context, _, _ string) error {
	return nil
}

// ---------------------------------------------------------------------------
// Helper: assertNoError is a test helper that fatals if err != nil.
// ---------------------------------------------------------------------------

func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestCrossTenant_JobIsolation verifies that tenant B cannot access tenant A's jobs.
func TestCrossTenant_JobIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 3 jobs for tenant A", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			job := &domain.Job{
				ID:       fmt.Sprintf("job-a-%d", i),
				TenantID: tenantA,
				Status:   domain.JobStatusCompleted,
			}
			assertNoError(t, db.UpsertJob(ctx, job), fmt.Sprintf("UpsertJob(job-a-%d)", i))
		}
	})

	t.Run("GetJob with tenant B returns empty/not-found", func(t *testing.T) {
		_, err := db.GetJob(ctx, "job-a-0", tenantB)
		if err == nil {
			t.Error("Expected error when tenant B calls GetJob for A's job, got nil")
		}
	})

	t.Run("ListJobs for tenant B returns empty list", func(t *testing.T) {
		jobs, err := db.ListJobs(ctx, tenantB, "", "")
		assertNoError(t, err, "ListJobs for tenant B")
		if len(jobs) != 0 {
			t.Errorf("Expected tenant B to have 0 jobs, got %d", len(jobs))
		}
	})

	t.Run("tenant A can still access own jobs", func(t *testing.T) {
		jobs, err := db.ListJobs(ctx, tenantA, "", "")
		assertNoError(t, err, "ListJobs for tenant A")
		if len(jobs) != 3 {
			t.Errorf("Expected tenant A to have 3 jobs, got %d", len(jobs))
		}
		// Verify specific job retrieval works
		job, err := db.GetJob(ctx, "job-a-0", tenantA)
		assertNoError(t, err, "GetJob for tenant A's job-a-0")
		if job.TenantID != tenantA {
			t.Errorf("Expected job tenant to be %s, got %s", tenantA, job.TenantID)
		}
	})

	t.Run("GetRecentJobs for tenant B returns empty", func(t *testing.T) {
		jobs, err := db.GetRecentJobs(ctx, tenantB, 10)
		assertNoError(t, err, "GetRecentJobs for tenant B")
		if len(jobs) != 0 {
			t.Errorf("Expected tenant B to have 0 recent jobs, got %d", len(jobs))
		}
	})

	t.Run("GetRecentJobs for tenant A returns all 3", func(t *testing.T) {
		jobs, err := db.GetRecentJobs(ctx, tenantA, 10)
		assertNoError(t, err, "GetRecentJobs for tenant A")
		if len(jobs) != 3 {
			t.Errorf("Expected tenant A to have 3 recent jobs, got %d", len(jobs))
		}
	})
}

// TestCrossTenant_VendorIsolation verifies that tenant B cannot access
// tenant A's vendors. Also documents the GetVendor design gap.
func TestCrossTenant_VendorIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 2 vendors for tenant A", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			vendor := &domain.Vendor{
				ID:        fmt.Sprintf("vendor-a-%d", i),
				TenantID:  tenantA,
				Name:      fmt.Sprintf("Vendor A-%d", i),
				RiskScore: 50,
				RiskTier:  domain.RiskTierLow,
			}
			assertNoError(t, db.UpsertVendor(ctx, vendor), fmt.Sprintf("UpsertVendor(vendor-a-%d)", i))
		}
	})

	t.Run("ListVendors for tenant B returns empty", func(t *testing.T) {
		vendors, err := db.ListVendors(ctx, tenantB)
		assertNoError(t, err, "ListVendors for tenant B")
		if len(vendors) != 0 {
			t.Errorf("Expected tenant B to have 0 vendors, got %d", len(vendors))
		}
	})

	t.Run("tenant A can still list own vendors", func(t *testing.T) {
		vendors, err := db.ListVendors(ctx, tenantA)
		assertNoError(t, err, "ListVendors for tenant A")
		if len(vendors) != 2 {
			t.Errorf("Expected tenant A to have 2 vendors, got %d", len(vendors))
		}
	})

	t.Run("GetVendor enforces tenant isolation", func(t *testing.T) {
		// GetVendor now accepts a tenantID parameter and enforces isolation.
		// Cross-tenant access by ID should now fail.

		// Create a vendor for tenant B.
		vendorB := &domain.Vendor{
			ID:        "vendor-b-0",
			TenantID:  tenantB,
			Name:      "Vendor B-0",
			RiskScore: 30,
			RiskTier:  domain.RiskTierMedium,
		}
		assertNoError(t, db.UpsertVendor(ctx, vendorB), "UpsertVendor for tenant B")

		// Tenant A calling GetVendor for B's vendor should now FAIL.
		_, err := db.GetVendor(ctx, "vendor-b-0", tenantA)
		if err == nil {
			t.Error("GetVendor(vendor-b-0) from tenant A should fail (tenant isolation)")
		}

		// Tenant B can still access their own vendor.
		v, err := db.GetVendor(ctx, "vendor-b-0", tenantB)
		assertNoError(t, err, "GetVendor(vendor-b-0) from tenant B")
		if v.TenantID != tenantB {
			t.Errorf("Expected vendor tenant to be %s, got %s", tenantB, v.TenantID)
		}

		// Reverse: Tenant B cannot access A's vendor.
		_, err = db.GetVendor(ctx, "vendor-a-0", tenantB)
		if err == nil {
			t.Error("GetVendor(vendor-a-0) from tenant B should fail (tenant isolation)")
		}

		// Tenant A can still access their own vendor.
		v2, err := db.GetVendor(ctx, "vendor-a-0", tenantA)
		assertNoError(t, err, "GetVendor(vendor-a-0) from tenant A")
		if v2.TenantID != tenantA {
			t.Errorf("Expected vendor tenant to be %s, got %s", tenantA, v2.TenantID)
		}
	})

	t.Run("GetRiskyVendors for tenant B returns only B's risky vendors", func(t *testing.T) {
		// Add a risky vendor for tenant B.
		riskyB := &domain.Vendor{
			ID:           "vendor-b-risky",
			TenantID:     tenantB,
			Name:         "Risky B",
			RiskScore:    20,
			TrustBattery: domain.TrustBattery{Tier: domain.TrustTierProbation},
		}
		assertNoError(t, db.UpsertVendor(ctx, riskyB), "UpsertVendor for risky B vendor")

		riskyVendors, err := db.GetRiskyVendors(ctx, tenantB)
		assertNoError(t, err, "GetRiskyVendors for tenant B")
		if len(riskyVendors) != 1 {
			t.Errorf("Expected tenant B to have 1 risky vendor, got %d", len(riskyVendors))
		}
		if len(riskyVendors) > 0 && riskyVendors[0].ID != "vendor-b-risky" {
			t.Errorf("Expected risky vendor 'vendor-b-risky', got %s", riskyVendors[0].ID)
		}

		// Tenant A's risky vendors should not include B's risky vendors.
		riskyA, err := db.GetRiskyVendors(ctx, tenantA)
		assertNoError(t, err, "GetRiskyVendors for tenant A")
		for _, v := range riskyA {
			if v.TenantID != tenantA {
				t.Errorf("GetRiskyVendors for tenant A returned vendor from tenant %s", v.TenantID)
			}
		}
	})
}

// TestCrossTenant_DocumentIsolation verifies that tenant B cannot access tenant A's documents.
func TestCrossTenant_DocumentIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 2 docs for tenant A", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			doc := &domain.Document{
				ID:          fmt.Sprintf("doc-a-%d", i),
				TenantID:    tenantA,
				JobID:       fmt.Sprintf("job-a-%d", i),
				FileName:    fmt.Sprintf("file-%d.pdf", i),
				ContentHash: fmt.Sprintf("hash-a-%d", i),
				Status:      "completed",
				Type:        "INVOICE",
			}
			assertNoError(t, db.UpsertDocument(ctx, doc), fmt.Sprintf("UpsertDocument(doc-a-%d)", i))
		}
	})

	t.Run("FindBySHA256 with tenant B returns nil", func(t *testing.T) {
		doc, err := db.FindBySHA256(ctx, tenantB, "hash-a-0")
		assertNoError(t, err, "FindBySHA256 for tenant B")
		if doc != nil {
			t.Errorf("Expected nil document when tenant B searches by A's hash, got ID=%s", doc.ID)
		}
	})

	t.Run("tenant A can still find own docs by hash", func(t *testing.T) {
		doc, err := db.FindBySHA256(ctx, tenantA, "hash-a-0")
		assertNoError(t, err, "FindBySHA256 for tenant A")
		if doc == nil {
			t.Fatal("Expected to find document for tenant A")
		}
		if doc.TenantID != tenantA {
			t.Errorf("Expected document tenant to be %s, got %s", tenantA, doc.TenantID)
		}
	})

	t.Run("FindBySHA256 with non-existent hash returns nil", func(t *testing.T) {
		doc, err := db.FindBySHA256(ctx, tenantA, "nonexistent-hash")
		assertNoError(t, err, "FindBySHA256 with non-existent hash")
		if doc != nil {
			t.Errorf("Expected nil for non-existent hash, got ID=%s", doc.ID)
		}
	})

	t.Run("GetDocument enforces tenant isolation", func(t *testing.T) {
		// GetDocument now accepts a tenantID parameter and enforces isolation.
		docB := &domain.Document{
			ID:          "doc-b-0",
			TenantID:    tenantB,
			JobID:       "job-b-0",
			FileName:    "b-file.pdf",
			ContentHash: "hash-b-0",
			Status:      "pending",
			Type:        "INVOICE",
		}
		assertNoError(t, db.UpsertDocument(ctx, docB), "UpsertDocument for tenant B")

		// Tenant A cannot get B's document by ID.
		_, err := db.GetDocument(ctx, "doc-b-0", tenantA)
		if err == nil {
			t.Error("GetDocument(doc-b-0) from tenant A should fail (tenant isolation)")
		}

		// Tenant B can get their own document.
		doc, err := db.GetDocument(ctx, "doc-b-0", tenantB)
		assertNoError(t, err, "GetDocument(doc-b-0) from tenant B")
		if doc.TenantID != tenantB {
			t.Errorf("Expected document tenant to be %s, got %s", tenantB, doc.TenantID)
		}
	})
}

// TestCrossTenant_HITLIsolation verifies that tenant B cannot access tenant A's HITL requests.
func TestCrossTenant_HITLIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 2 HITL requests for tenant A (1 pending, 1 approved)", func(t *testing.T) {
		now := time.Now()

		// Pending request
		pending := &domain.HITLRequest{
			ID:       "hitl-pending-a",
			TenantID: tenantA,
			JobID:    "job-pending-a",
			Reason:   "Low OCR confidence",
			Status:   domain.HITLStatusPending,
			SentAt:   now,
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, pending), "UpsertHITLRequest pending A")

		// Also add the associated job (pending HITL listing expects it)
		pendingJob := &domain.Job{
			ID:       "job-pending-a",
			TenantID: tenantA,
			Status:   domain.JobStatusAwaitingHITL,
		}
		assertNoError(t, db.UpsertJob(ctx, pendingJob), "UpsertJob for pending A")

		// Approved request
		approved := &domain.HITLRequest{
			ID:       "hitl-approved-a",
			TenantID: tenantA,
			JobID:    "job-approved-a",
			Reason:   "Already reviewed",
			Status:   domain.HITLStatusApproved,
			SentAt:   now,
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, approved), "UpsertHITLRequest approved A")
	})

	t.Run("ListPendingHITL for tenant B returns empty", func(t *testing.T) {
		pending, err := db.ListPendingHITL(ctx, tenantB)
		assertNoError(t, err, "ListPendingHITL for tenant B")
		if len(pending) != 0 {
			t.Errorf("Expected tenant B to have 0 pending HITL, got %d", len(pending))
		}
	})

	t.Run("tenant A sees only their pending HITL (not approved)", func(t *testing.T) {
		pending, err := db.ListPendingHITL(ctx, tenantA)
		assertNoError(t, err, "ListPendingHITL for tenant A")
		if len(pending) != 1 {
			t.Errorf("Expected tenant A to have 1 pending HITL, got %d", len(pending))
		}
		if len(pending) > 0 && pending[0].ID != "hitl-pending-a" {
			t.Errorf("Expected pending HITL 'hitl-pending-a', got %s", pending[0].ID)
		}
	})

	t.Run("tenant A's approved request does not appear in pending list", func(t *testing.T) {
		pending, err := db.ListPendingHITL(ctx, tenantA)
		assertNoError(t, err, "ListPendingHITL for tenant A")
		for _, r := range pending {
			if r.Status != domain.HITLStatusPending {
				t.Errorf("Non-pending request %s leaked into pending list with status %s", r.ID, r.Status)
			}
		}
	})

	t.Run("GetHITLRequest enforces tenant isolation", func(t *testing.T) {
		// GetHITLRequest now accepts a tenantID parameter and enforces isolation.
		// Create a HITL request for tenant B (approved so it doesn't affect pending counts).
		hitlB := &domain.HITLRequest{
			ID:       "hitl-b-0",
			TenantID: tenantB,
			JobID:    "job-b-0",
			Reason:   "B's review",
			Status:   domain.HITLStatusApproved,
			SentAt:   time.Now(),
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, hitlB), "UpsertHITLRequest for tenant B")

		// Tenant A cannot get B's HITL request by ID.
		_, err := db.GetHITLRequest(ctx, "hitl-b-0", tenantA)
		if err == nil {
			t.Error("GetHITLRequest(hitl-b-0) from tenant A should fail (tenant isolation)")
		}

		// Tenant B can get their own HITL request.
		req, err := db.GetHITLRequest(ctx, "hitl-b-0", tenantB)
		assertNoError(t, err, "GetHITLRequest(hitl-b-0) from tenant B")
		if req.TenantID != tenantB {
			t.Errorf("Expected HITL request tenant to be %s, got %s", tenantB, req.TenantID)
		}
	})

	t.Run("tenant B sees only their own pending HITL", func(t *testing.T) {
		// Create a pending HITL for B
		hitlPendingB := &domain.HITLRequest{
			ID:       "hitl-pending-b",
			TenantID: tenantB,
			JobID:    "job-pending-b",
			Reason:   "B's pending review",
			Status:   domain.HITLStatusPending,
			SentAt:   time.Now(),
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, hitlPendingB), "UpsertHITLRequest pending B")
		jobB := &domain.Job{
			ID:       "job-pending-b",
			TenantID: tenantB,
			Status:   domain.JobStatusAwaitingHITL,
		}
		assertNoError(t, db.UpsertJob(ctx, jobB), "UpsertJob for pending B")

		pendingB, err := db.ListPendingHITL(ctx, tenantB)
		assertNoError(t, err, "ListPendingHITL for tenant B")
		if len(pendingB) != 1 {
			t.Errorf("Expected tenant B to have 1 pending HITL, got %d", len(pendingB))
		}
		if len(pendingB) > 0 && pendingB[0].TenantID != tenantB {
			t.Errorf("Expected pending HITL to belong to tenant B, got tenant %s", pendingB[0].TenantID)
		}
	})
}

// TestCrossTenant_AuditEventIsolation verifies that tenant B cannot access tenant A's audit events.
func TestCrossTenant_AuditEventIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 3 audit events for tenant A (oldest first to test newest-first ordering)", func(t *testing.T) {
		now := time.Now()
		// Append oldest first: the ListAuditEvents implementation reverses the
		// collected slice to produce newest-first order, so we insert [t-2h, t-1h, t]
		// to verify the reversal logic produces [t, t-1h, t-2h].
		for i := 2; i >= 0; i-- {
			event := &domain.AuditEvent{
				TenantID:   tenantA,
				Actor:      "system",
				Action:     fmt.Sprintf("ACTION_%d", i),
				TargetType: "job",
				TargetID:   fmt.Sprintf("job-a-%d", i),
				Timestamp:  now.Add(-time.Duration(i) * time.Hour),
			}
			assertNoError(t, db.AppendAuditEvent(ctx, event), fmt.Sprintf("AppendAuditEvent(%d)", i))
		}
	})

	t.Run("ListAuditEvents for tenant B returns empty", func(t *testing.T) {
		events, err := db.ListAuditEvents(ctx, tenantB, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant B")
		if len(events) != 0 {
			t.Errorf("Expected tenant B to have 0 audit events, got %d", len(events))
		}
	})

	t.Run("ListAuditEvents for tenant B with specific target returns empty", func(t *testing.T) {
		events, err := db.ListAuditEvents(ctx, tenantB, "job", "job-a-0", 50)
		assertNoError(t, err, "ListAuditEvents for tenant B with target filter")
		if len(events) != 0 {
			t.Errorf("Expected tenant B to have 0 audit events for A's job, got %d", len(events))
		}
	})

	t.Run("tenant A can still see own audit events", func(t *testing.T) {
		events, err := db.ListAuditEvents(ctx, tenantA, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant A")
		if len(events) != 3 {
			t.Errorf("Expected tenant A to have 3 audit events, got %d", len(events))
		}
	})

	t.Run("ListAuditEvents with target filter for tenant A", func(t *testing.T) {
		events, err := db.ListAuditEvents(ctx, tenantA, "job", "job-a-0", 50)
		assertNoError(t, err, "ListAuditEvents for tenant A with target filter")
		if len(events) != 1 {
			t.Errorf("Expected 1 audit event for job-a-0, got %d", len(events))
		}
	})

	t.Run("events are ordered newest first", func(t *testing.T) {
		events, err := db.ListAuditEvents(ctx, tenantA, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for ordering check")
		if len(events) >= 2 {
			if events[0].Timestamp.Before(events[1].Timestamp) {
				t.Error("Audit events should be ordered newest first")
			}
		}
	})

	t.Run("tenant B's own audit events are isolated from A", func(t *testing.T) {
		// Create events for B.
		for i := 0; i < 2; i++ {
			event := &domain.AuditEvent{
				TenantID:   tenantB,
				Actor:      "system-b",
				Action:     fmt.Sprintf("B_ACTION_%d", i),
				TargetType: "job",
				TargetID:   fmt.Sprintf("job-b-%d", i),
				Timestamp:  time.Now(),
			}
			assertNoError(t, db.AppendAuditEvent(ctx, event), fmt.Sprintf("AppendAuditEvent for B(%d)", i))
		}

		// A should still see only 3 events (not 5).
		eventsA, err := db.ListAuditEvents(ctx, tenantA, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant A after B's events")
		if len(eventsA) != 3 {
			t.Errorf("Expected tenant A to still have 3 audit events, got %d (B's events leaked)", len(eventsA))
		}

		// B should see its own 2 events (not 5).
		eventsB, err := db.ListAuditEvents(ctx, tenantB, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant B")
		if len(eventsB) != 2 {
			t.Errorf("Expected tenant B to have 2 audit events, got %d", len(eventsB))
		}
	})
}

// TestCrossTenant_ComplianceIsolation verifies that tenant B cannot access tenant A's compliance records.
func TestCrossTenant_ComplianceIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	t.Run("create 2 compliance records for tenant A", func(t *testing.T) {
		now := time.Now()
		for i := 0; i < 2; i++ {
			rec := &domain.ComplianceRecord{
				ID:        fmt.Sprintf("comp-a-%d", i),
				TenantID:  tenantA,
				SourceURL: fmt.Sprintf("https://example.com/gap-%d", i),
				Gap:       fmt.Sprintf("Gap description %d", i),
				Severity:  "medium",
				Score:     0.75,
				CreatedAt: now.Add(-time.Duration(i) * time.Hour),
			}
			db.compliance = append(db.compliance, rec)
		}
	})

	t.Run("GetRecentCompliance for tenant B returns empty", func(t *testing.T) {
		records, err := db.GetRecentCompliance(ctx, tenantB, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant B")
		if len(records) != 0 {
			t.Errorf("Expected tenant B to have 0 compliance records, got %d", len(records))
		}
	})

	t.Run("tenant A can still see own compliance records", func(t *testing.T) {
		records, err := db.GetRecentCompliance(ctx, tenantA, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant A")
		if len(records) != 2 {
			t.Errorf("Expected tenant A to have 2 compliance records, got %d", len(records))
		}
	})

	t.Run("limit parameter works correctly", func(t *testing.T) {
		records, err := db.GetRecentCompliance(ctx, tenantA, 1)
		assertNoError(t, err, "GetRecentCompliance with limit=1")
		if len(records) != 1 {
			t.Errorf("Expected 1 compliance record with limit=1, got %d", len(records))
		}
	})

	t.Run("tenant B's compliance records are isolated from A", func(t *testing.T) {
		// Add compliance records for B.
		for i := 0; i < 2; i++ {
			rec := &domain.ComplianceRecord{
				ID:        fmt.Sprintf("comp-b-%d", i),
				TenantID:  tenantB,
				SourceURL: fmt.Sprintf("https://example.com/b-gap-%d", i),
				Gap:       fmt.Sprintf("B gap %d", i),
				Severity:  "high",
				Score:     0.5,
				CreatedAt: time.Now(),
			}
			db.compliance = append(db.compliance, rec)
		}

		// A should still only see 2 records.
		recordsA, err := db.GetRecentCompliance(ctx, tenantA, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant A after B's records added")
		if len(recordsA) != 2 {
			t.Errorf("Expected tenant A to still have 2 compliance records, got %d", len(recordsA))
		}
		for _, r := range recordsA {
			if r.TenantID != tenantA {
				t.Errorf("Compliance record %s belongs to tenant %s, leaked to tenant A", r.ID, r.TenantID)
			}
		}

		// B should see its own 2 records.
		recordsB, err := db.GetRecentCompliance(ctx, tenantB, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant B")
		if len(recordsB) != 2 {
			t.Errorf("Expected tenant B to have 2 compliance records, got %d", len(recordsB))
		}
		for _, r := range recordsB {
			if r.TenantID != tenantB {
				t.Errorf("Compliance record %s belongs to tenant %s, leaked to tenant B", r.ID, r.TenantID)
			}
		}
	})
}

// TestCrossTenant_MixedDataIsolation creates data for both tenants and verifies
// that each tenant only sees their own data across all resource types.
func TestCrossTenant_MixedDataIsolation(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantA := "tenant-mixed-a"
	tenantB := "tenant-mixed-b"

	// Expected counts per tenant.
	const (
		expectedJobsA        = 2
		expectedJobsB        = 3
		expectedVendorsA     = 2
		expectedVendorsB     = 1
		expectedDocsA        = 2
		expectedDocsB        = 2
		expectedPendingHitlA = 1
		expectedPendingHitlB = 2
		expectedAuditA       = 2
		expectedAuditB       = 3
		expectedComplianceA  = 2
		expectedComplianceB  = 1
	)

	t.Run("seed data for both tenants", func(t *testing.T) {
		now := time.Now()

		// -- Jobs --
		for i := 0; i < expectedJobsA; i++ {
			job := &domain.Job{
				ID:       fmt.Sprintf("mix-job-a-%d", i),
				TenantID: tenantA,
				Status:   domain.JobStatusCompleted,
			}
			assertNoError(t, db.UpsertJob(ctx, job), fmt.Sprintf("UpsertJob A-%d", i))
		}
		for i := 0; i < expectedJobsB; i++ {
			job := &domain.Job{
				ID:       fmt.Sprintf("mix-job-b-%d", i),
				TenantID: tenantB,
				Status:   domain.JobStatusCompleted,
			}
			assertNoError(t, db.UpsertJob(ctx, job), fmt.Sprintf("UpsertJob B-%d", i))
		}

		// -- Vendors --
		for i := 0; i < expectedVendorsA; i++ {
			vendor := &domain.Vendor{
				ID:       fmt.Sprintf("mix-vendor-a-%d", i),
				TenantID: tenantA,
				Name:     fmt.Sprintf("Mix Vendor A-%d", i),
			}
			assertNoError(t, db.UpsertVendor(ctx, vendor), fmt.Sprintf("UpsertVendor A-%d", i))
		}
		for i := 0; i < expectedVendorsB; i++ {
			vendor := &domain.Vendor{
				ID:       fmt.Sprintf("mix-vendor-b-%d", i),
				TenantID: tenantB,
				Name:     fmt.Sprintf("Mix Vendor B-%d", i),
			}
			assertNoError(t, db.UpsertVendor(ctx, vendor), fmt.Sprintf("UpsertVendor B-%d", i))
		}

		// -- Documents --
		for i := 0; i < expectedDocsA; i++ {
			doc := &domain.Document{
				ID:          fmt.Sprintf("mix-doc-a-%d", i),
				TenantID:    tenantA,
				JobID:       fmt.Sprintf("mix-job-a-%d", i),
				FileName:    fmt.Sprintf("a-file-%d.pdf", i),
				ContentHash: fmt.Sprintf("mix-hash-a-%d", i),
				Status:      "completed",
				Type:        "INVOICE",
			}
			assertNoError(t, db.UpsertDocument(ctx, doc), fmt.Sprintf("UpsertDocument A-%d", i))
		}
		for i := 0; i < expectedDocsB; i++ {
			doc := &domain.Document{
				ID:          fmt.Sprintf("mix-doc-b-%d", i),
				TenantID:    tenantB,
				JobID:       fmt.Sprintf("mix-job-b-%d", i),
				FileName:    fmt.Sprintf("b-file-%d.pdf", i),
				ContentHash: fmt.Sprintf("mix-hash-b-%d", i),
				Status:      "pending",
				Type:        "INVOICE",
			}
			assertNoError(t, db.UpsertDocument(ctx, doc), fmt.Sprintf("UpsertDocument B-%d", i))
		}

		// -- HITL Requests --
		// A: 1 pending + 1 approved (only pending counts)
		// Note: we don't upsert associated jobs here to avoid inflating
		// the job count expectations. HITL-specific isolation tests cover
		// the full lifecycle including associated jobs.
		hitlAPending := &domain.HITLRequest{
			ID:       "mix-hitl-a-pending",
			TenantID: tenantA,
			JobID:    "mix-job-hitl-a",
			Reason:   "A pending review",
			Status:   domain.HITLStatusPending,
			SentAt:   now,
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, hitlAPending), "UpsertHITLRequest A pending")

		hitlAApproved := &domain.HITLRequest{
			ID:       "mix-hitl-a-approved",
			TenantID: tenantA,
			JobID:    "mix-job-a-approved",
			Reason:   "A approved",
			Status:   domain.HITLStatusApproved,
			SentAt:   now,
		}
		assertNoError(t, db.UpsertHITLRequest(ctx, hitlAApproved), "UpsertHITLRequest A approved")

		// B: 2 pending
		for i := 0; i < expectedPendingHitlB; i++ {
			hitlBPending := &domain.HITLRequest{
				ID:       fmt.Sprintf("mix-hitl-b-pending-%d", i),
				TenantID: tenantB,
				JobID:    fmt.Sprintf("mix-job-hitl-b-%d", i),
				Reason:   fmt.Sprintf("B pending review %d", i),
				Status:   domain.HITLStatusPending,
				SentAt:   now,
			}
			assertNoError(t, db.UpsertHITLRequest(ctx, hitlBPending), fmt.Sprintf("UpsertHITLRequest B pending %d", i))
		}

		// -- Audit Events --
		// Insert oldest-first so that the reversal in ListAuditEvents
		// produces newest-first order.
		for i := expectedAuditA - 1; i >= 0; i-- {
			ev := &domain.AuditEvent{
				TenantID:   tenantA,
				Actor:      "system-a",
				Action:     fmt.Sprintf("A_EVENT_%d", i),
				TargetType: "job",
				TargetID:   fmt.Sprintf("mix-job-a-%d", i),
				Timestamp:  now.Add(-time.Duration(i) * time.Minute),
			}
			assertNoError(t, db.AppendAuditEvent(ctx, ev), fmt.Sprintf("AppendAuditEvent A-%d", i))
		}
		for i := expectedAuditB - 1; i >= 0; i-- {
			ev := &domain.AuditEvent{
				TenantID:   tenantB,
				Actor:      "system-b",
				Action:     fmt.Sprintf("B_EVENT_%d", i),
				TargetType: "job",
				TargetID:   fmt.Sprintf("mix-job-b-%d", i),
				Timestamp:  now.Add(-time.Duration(i) * time.Minute),
			}
			assertNoError(t, db.AppendAuditEvent(ctx, ev), fmt.Sprintf("AppendAuditEvent B-%d", i))
		}

		// -- Compliance Records --
		for i := 0; i < expectedComplianceA; i++ {
			rec := &domain.ComplianceRecord{
				ID:        fmt.Sprintf("mix-comp-a-%d", i),
				TenantID:  tenantA,
				SourceURL: fmt.Sprintf("https://example.com/a-gap-%d", i),
				Gap:       fmt.Sprintf("A gap %d", i),
				Severity:  "low",
				Score:     0.9,
				CreatedAt: now.Add(-time.Duration(i) * time.Hour),
			}
			db.compliance = append(db.compliance, rec)
		}
		for i := 0; i < expectedComplianceB; i++ {
			rec := &domain.ComplianceRecord{
				ID:        fmt.Sprintf("mix-comp-b-%d", i),
				TenantID:  tenantB,
				SourceURL: fmt.Sprintf("https://example.com/b-gap-%d", i),
				Gap:       fmt.Sprintf("B gap %d", i),
				Severity:  "high",
				Score:     0.3,
				CreatedAt: now.Add(-time.Duration(i) * time.Hour),
			}
			db.compliance = append(db.compliance, rec)
		}
	})

	// -- Tenant A verifications --
	t.Run("tenant A sees only A's data", func(t *testing.T) {
		// Jobs
		jobs, err := db.ListJobs(ctx, tenantA, "", "")
		assertNoError(t, err, "ListJobs for tenant A")
		if len(jobs) != expectedJobsA {
			t.Errorf("Expected %d jobs for tenant A, got %d", expectedJobsA, len(jobs))
		}
		for _, j := range jobs {
			if j.TenantID != tenantA {
				t.Errorf("Job %s belongs to tenant %s, leaked to tenant A", j.ID, j.TenantID)
			}
		}

		// GetRecentJobs
		recentJobs, err := db.GetRecentJobs(ctx, tenantA, 10)
		assertNoError(t, err, "GetRecentJobs for tenant A")
		if len(recentJobs) != expectedJobsA {
			t.Errorf("Expected %d recent jobs for tenant A, got %d", expectedJobsA, len(recentJobs))
		}

		// Vendors
		vendors, err := db.ListVendors(ctx, tenantA)
		assertNoError(t, err, "ListVendors for tenant A")
		if len(vendors) != expectedVendorsA {
			t.Errorf("Expected %d vendors for tenant A, got %d", expectedVendorsA, len(vendors))
		}
		for _, v := range vendors {
			if v.TenantID != tenantA {
				t.Errorf("Vendor %s belongs to tenant %s, leaked to tenant A", v.ID, v.TenantID)
			}
		}

		// Documents (FindBySHA256)
		for i := 0; i < expectedDocsA; i++ {
			doc, err := db.FindBySHA256(ctx, tenantA, fmt.Sprintf("mix-hash-a-%d", i))
			assertNoError(t, err, fmt.Sprintf("FindBySHA256 for tenant A hash %d", i))
			if doc == nil {
				t.Errorf("Expected tenant A to find document with hash mix-hash-a-%d", i)
			} else if doc.TenantID != tenantA {
				t.Errorf("Document %s belongs to tenant %s, leaked to tenant A", doc.ID, doc.TenantID)
			}
		}

		// HITL
		pending, err := db.ListPendingHITL(ctx, tenantA)
		assertNoError(t, err, "ListPendingHITL for tenant A")
		if len(pending) != expectedPendingHitlA {
			t.Errorf("Expected %d pending HITL for tenant A, got %d", expectedPendingHitlA, len(pending))
		}
		for _, r := range pending {
			if r.TenantID != tenantA {
				t.Errorf("HITL request %s belongs to tenant %s, leaked to tenant A", r.ID, r.TenantID)
			}
		}

		// Audit Events
		events, err := db.ListAuditEvents(ctx, tenantA, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant A")
		if len(events) != expectedAuditA {
			t.Errorf("Expected %d audit events for tenant A, got %d", expectedAuditA, len(events))
		}
		for _, ev := range events {
			if ev.TenantID != tenantA {
				t.Errorf("Audit event %s belongs to tenant %s, leaked to tenant A", ev.ID, ev.TenantID)
			}
		}

		// Compliance
		compliance, err := db.GetRecentCompliance(ctx, tenantA, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant A")
		if len(compliance) != expectedComplianceA {
			t.Errorf("Expected %d compliance records for tenant A, got %d", expectedComplianceA, len(compliance))
		}
		for _, r := range compliance {
			if r.TenantID != tenantA {
				t.Errorf("Compliance record %s belongs to tenant %s, leaked to tenant A", r.ID, r.TenantID)
			}
		}
	})

	// -- Tenant B verifications --
	t.Run("tenant B sees only B's data", func(t *testing.T) {
		// Jobs
		jobs, err := db.ListJobs(ctx, tenantB, "", "")
		assertNoError(t, err, "ListJobs for tenant B")
		if len(jobs) != expectedJobsB {
			t.Errorf("Expected %d jobs for tenant B, got %d", expectedJobsB, len(jobs))
		}
		for _, j := range jobs {
			if j.TenantID != tenantB {
				t.Errorf("Job %s belongs to tenant %s, leaked to tenant B", j.ID, j.TenantID)
			}
		}

		// GetRecentJobs
		recentJobs, err := db.GetRecentJobs(ctx, tenantB, 10)
		assertNoError(t, err, "GetRecentJobs for tenant B")
		if len(recentJobs) != expectedJobsB {
			t.Errorf("Expected %d recent jobs for tenant B, got %d", expectedJobsB, len(recentJobs))
		}

		// Vendors
		vendors, err := db.ListVendors(ctx, tenantB)
		assertNoError(t, err, "ListVendors for tenant B")
		if len(vendors) != expectedVendorsB {
			t.Errorf("Expected %d vendors for tenant B, got %d", expectedVendorsB, len(vendors))
		}
		for _, v := range vendors {
			if v.TenantID != tenantB {
				t.Errorf("Vendor %s belongs to tenant %s, leaked to tenant B", v.ID, v.TenantID)
			}
		}

		// Documents (FindBySHA256)
		for i := 0; i < expectedDocsB; i++ {
			doc, err := db.FindBySHA256(ctx, tenantB, fmt.Sprintf("mix-hash-b-%d", i))
			assertNoError(t, err, fmt.Sprintf("FindBySHA256 for tenant B hash %d", i))
			if doc == nil {
				t.Errorf("Expected tenant B to find document with hash mix-hash-b-%d", i)
			} else if doc.TenantID != tenantB {
				t.Errorf("Document %s belongs to tenant %s, leaked to tenant B", doc.ID, doc.TenantID)
			}
		}

		// Tenant B should NOT find A's documents by hash
		for i := 0; i < expectedDocsA; i++ {
			doc, err := db.FindBySHA256(ctx, tenantB, fmt.Sprintf("mix-hash-a-%d", i))
			assertNoError(t, err, fmt.Sprintf("FindBySHA256 for tenant B with A's hash %d", i))
			if doc != nil {
				t.Errorf("Tenant B found A's document %s by hash (isolation breach)", doc.ID)
			}
		}

		// HITL
		pending, err := db.ListPendingHITL(ctx, tenantB)
		assertNoError(t, err, "ListPendingHITL for tenant B")
		if len(pending) != expectedPendingHitlB {
			t.Errorf("Expected %d pending HITL for tenant B, got %d", expectedPendingHitlB, len(pending))
		}
		for _, r := range pending {
			if r.TenantID != tenantB {
				t.Errorf("HITL request %s belongs to tenant %s, leaked to tenant B", r.ID, r.TenantID)
			}
		}

		// Audit Events
		events, err := db.ListAuditEvents(ctx, tenantB, "", "", 50)
		assertNoError(t, err, "ListAuditEvents for tenant B")
		if len(events) != expectedAuditB {
			t.Errorf("Expected %d audit events for tenant B, got %d", expectedAuditB, len(events))
		}
		for _, ev := range events {
			if ev.TenantID != tenantB {
				t.Errorf("Audit event %s belongs to tenant %s, leaked to tenant B", ev.ID, ev.TenantID)
			}
		}

		// Compliance
		compliance, err := db.GetRecentCompliance(ctx, tenantB, 10)
		assertNoError(t, err, "GetRecentCompliance for tenant B")
		if len(compliance) != expectedComplianceB {
			t.Errorf("Expected %d compliance records for tenant B, got %d", expectedComplianceB, len(compliance))
		}
		for _, r := range compliance {
			if r.TenantID != tenantB {
				t.Errorf("Compliance record %s belongs to tenant %s, leaked to tenant B", r.ID, r.TenantID)
			}
		}
	})

	// -- Cross-tenant access via get-by-ID methods (now tenant-filtered) --
	t.Run("GetVendor cross-tenant access blocked", func(t *testing.T) {
		// GetVendor now enforces tenant isolation.
		_, err := db.GetVendor(ctx, "mix-vendor-b-0", tenantA)
		if err == nil {
			t.Error("GetVendor(mix-vendor-b-0) from tenant A should fail (tenant isolation)")
		}

		_, err = db.GetVendor(ctx, "mix-vendor-a-0", tenantB)
		if err == nil {
			t.Error("GetVendor(mix-vendor-a-0) from tenant B should fail (tenant isolation)")
		}

		// Each tenant can still access their own vendors.
		vA, err := db.GetVendor(ctx, "mix-vendor-a-0", tenantA)
		assertNoError(t, err, "GetVendor(mix-vendor-a-0) from tenant A")
		if vA.TenantID != tenantA {
			t.Errorf("Expected vendor tenant %s, got %s", tenantA, vA.TenantID)
		}

		vB, err := db.GetVendor(ctx, "mix-vendor-b-0", tenantB)
		assertNoError(t, err, "GetVendor(mix-vendor-b-0) from tenant B")
		if vB.TenantID != tenantB {
			t.Errorf("Expected vendor tenant %s, got %s", tenantB, vB.TenantID)
		}
	})

	t.Run("GetDocument cross-tenant access blocked", func(t *testing.T) {
		_, err := db.GetDocument(ctx, "mix-doc-b-0", tenantA)
		if err == nil {
			t.Error("GetDocument(mix-doc-b-0) from tenant A should fail (tenant isolation)")
		}

		docB, err := db.GetDocument(ctx, "mix-doc-b-0", tenantB)
		assertNoError(t, err, "GetDocument(mix-doc-b-0) from tenant B")
		if docB.TenantID != tenantB {
			t.Errorf("Expected document tenant %s, got %s", tenantB, docB.TenantID)
		}
	})

	t.Run("GetHITLRequest cross-tenant access blocked", func(t *testing.T) {
		_, err := db.GetHITLRequest(ctx, "mix-hitl-b-pending-0", tenantA)
		if err == nil {
			t.Error("GetHITLRequest(mix-hitl-b-pending-0) from tenant A should fail (tenant isolation)")
		}

		hitlB, err := db.GetHITLRequest(ctx, "mix-hitl-b-pending-0", tenantB)
		assertNoError(t, err, "GetHITLRequest(mix-hitl-b-pending-0) from tenant B")
		if hitlB.TenantID != tenantB {
			t.Errorf("Expected HITL request tenant %s, got %s", tenantB, hitlB.TenantID)
		}
	})

	// -- Verify totals are correct across all resources --
	t.Run("total data integrity across tenants", func(t *testing.T) {
		// Count all jobs in the system.
		allJobsA, _ := db.ListJobs(ctx, tenantA, "", "")
		allJobsB, _ := db.ListJobs(ctx, tenantB, "", "")
		totalJobs := len(allJobsA) + len(allJobsB)

		if totalJobs != expectedJobsA+expectedJobsB {
			t.Errorf("Expected total %d jobs across both tenants, got A=%d + B=%d = %d",
				expectedJobsA+expectedJobsB, len(allJobsA), len(allJobsB), totalJobs)
		}

		// Count all vendors.
		allVendorsA, _ := db.ListVendors(ctx, tenantA)
		allVendorsB, _ := db.ListVendors(ctx, tenantB)
		totalVendors := len(allVendorsA) + len(allVendorsB)
		if totalVendors != expectedVendorsA+expectedVendorsB {
			t.Errorf("Expected total %d vendors, got %d",
				expectedVendorsA+expectedVendorsB, totalVendors)
		}
	})
}

// TestCrossTenant_EmptyTenant verifies that a tenant with no data gets empty results.
func TestCrossTenant_EmptyTenant(t *testing.T) {
	db := newInMemoryDB()
	ctx := context.Background()

	tenantID := "brand-new-tenant"

	t.Run("all list methods return empty for tenant with no data", func(t *testing.T) {
		jobs, err := db.ListJobs(ctx, tenantID, "", "")
		assertNoError(t, err, "ListJobs")
		if len(jobs) != 0 {
			t.Errorf("Expected 0 jobs for new tenant, got %d", len(jobs))
		}

		vendors, err := db.ListVendors(ctx, tenantID)
		assertNoError(t, err, "ListVendors")
		if len(vendors) != 0 {
			t.Errorf("Expected 0 vendors for new tenant, got %d", len(vendors))
		}

		pending, err := db.ListPendingHITL(ctx, tenantID)
		assertNoError(t, err, "ListPendingHITL")
		if len(pending) != 0 {
			t.Errorf("Expected 0 pending HITL for new tenant, got %d", len(pending))
		}

		events, err := db.ListAuditEvents(ctx, tenantID, "", "", 10)
		assertNoError(t, err, "ListAuditEvents")
		if len(events) != 0 {
			t.Errorf("Expected 0 audit events for new tenant, got %d", len(events))
		}

		compliance, err := db.GetRecentCompliance(ctx, tenantID, 10)
		assertNoError(t, err, "GetRecentCompliance")
		if len(compliance) != 0 {
			t.Errorf("Expected 0 compliance records for new tenant, got %d", len(compliance))
		}

		recentJobs, err := db.GetRecentJobs(ctx, tenantID, 10)
		assertNoError(t, err, "GetRecentJobs")
		if len(recentJobs) != 0 {
			t.Errorf("Expected 0 recent jobs for new tenant, got %d", len(recentJobs))
		}

		riskyVendors, err := db.GetRiskyVendors(ctx, tenantID)
		assertNoError(t, err, "GetRiskyVendors")
		if len(riskyVendors) != 0 {
			t.Errorf("Expected 0 risky vendors for new tenant, got %d", len(riskyVendors))
		}
	})

	t.Run("get-by-ID methods fail for non-existent IDs", func(t *testing.T) {
		_, err := db.GetJob(ctx, "nonexistent-job", tenantID)
		if err == nil {
			t.Error("Expected error for GetJob with non-existent ID")
		}

		_, err = db.GetVendor(ctx, "nonexistent-vendor", tenantID)
		if err == nil {
			t.Error("Expected error for GetVendor with non-existent ID")
		}

		_, err = db.GetDocument(ctx, "nonexistent-doc", tenantID)
		if err == nil {
			t.Error("Expected error for GetDocument with non-existent ID")
		}

		_, err = db.GetHITLRequest(ctx, "nonexistent-hitl", tenantID)
		if err == nil {
			t.Error("Expected error for GetHITLRequest with non-existent ID")
		}

		doc, err := db.FindBySHA256(ctx, tenantID, "nonexistent-hash")
		assertNoError(t, err, "FindBySHA256 with non-existent hash")
		if doc != nil {
			t.Errorf("Expected nil for non-existent hash, got ID=%s", doc.ID)
		}
	})
}

// --- Manufacturing pivot (Phase 1.4A) in-memory stubs (not exercised by these tests) ---

func (d *inMemoryDB) UpsertPurchaseOrder(_ context.Context, _ *domain.PurchaseOrder) error {
	return nil
}
func (d *inMemoryDB) GetPurchaseOrderByID(_ context.Context, _, _ string) (*domain.PurchaseOrder, error) {
	return nil, fmt.Errorf("not found")
}
func (d *inMemoryDB) ListPurchaseOrders(_ context.Context, _ string, _, _ int) ([]*domain.PurchaseOrder, error) {
	return nil, nil
}
func (d *inMemoryDB) UpsertGoodsReceipt(_ context.Context, _ *domain.GoodsReceipt) error { return nil }
func (d *inMemoryDB) GetGoodsReceiptByID(_ context.Context, _, _ string) (*domain.GoodsReceipt, error) {
	return nil, fmt.Errorf("not found")
}
func (d *inMemoryDB) ListGoodsReceipts(_ context.Context, _, _ string, _, _ int) ([]*domain.GoodsReceipt, error) {
	return nil, nil
}
func (d *inMemoryDB) UpsertInvoice(_ context.Context, _ *domain.Invoice) error { return nil }
func (d *inMemoryDB) GetInvoiceByID(_ context.Context, _, _ string) (*domain.Invoice, error) {
	return nil, fmt.Errorf("not found")
}
func (d *inMemoryDB) ListInvoices(_ context.Context, _, _ string, _, _ int) ([]*domain.Invoice, error) {
	return nil, nil
}
func (d *inMemoryDB) UpsertExceptionCase(_ context.Context, _ *domain.ExceptionCase) error {
	return nil
}
func (d *inMemoryDB) GetExceptionCaseByID(_ context.Context, _, _ string) (*domain.ExceptionCase, error) {
	return nil, fmt.Errorf("not found")
}
func (d *inMemoryDB) ListExceptionCases(_ context.Context, _, _, _ string, _, _ int) ([]*domain.ExceptionCase, error) {
	return nil, nil
}
func (d *inMemoryDB) UpdateExceptionCaseStatus(_ context.Context, _, _, _ string) error { return nil }
