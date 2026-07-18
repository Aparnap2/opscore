//go:build agentic

// Package agentic — PHASE 2 manufacturing persistence test.
//
// This test proves that the signal workflow now writes the deterministic
// manufacturing entities (PurchaseOrder / GoodsReceipt / Invoice) and any
// ExceptionCase produced by the Signal Agent into the real persistence tables
// via DBProvider, instead of only stashing them on the transitional
// jobs.extracted_data bridge.
//
// It is written RED-first: before the persistSignalEntities helper existed,
// processSignalJob only wrote the job record, so the Upsert* assertions below
// would fail. The helper is the minimal implementation that makes them green.
package agentic

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// recordingDB — in-memory DBProvider that records manufacturing upsert calls.
// It implements EVERY method of providers.DBProvider so it can stand in for a
// real adapter in the worker. Only the manufacturing upserts are asserted on;
// the rest are stubbed to satisfy the interface.
// ---------------------------------------------------------------------------

type recordingDB struct {
	mu sync.Mutex

	pos   map[string]*domain.PurchaseOrder
	grs   map[string]*domain.GoodsReceipt
	invs  map[string]*domain.Invoice
	excs  map[string]*domain.ExceptionCase
	jobs  map[string]*domain.Job
	audit []*domain.AuditEvent

	// call counts for assertions
	poUpserts   int
	grUpserts   int
	invUpserts  int
	excUpserts  int
}

func newRecordingDB() *recordingDB {
	return &recordingDB{
		pos:  make(map[string]*domain.PurchaseOrder),
		grs:  make(map[string]*domain.GoodsReceipt),
		invs: make(map[string]*domain.Invoice),
		excs: make(map[string]*domain.ExceptionCase),
		jobs: make(map[string]*domain.Job),
	}
}

var _ providers.DBProvider = (*recordingDB)(nil)

// -- Manufacturing upserts (the assertions target these) --------------------

func (d *recordingDB) UpsertPurchaseOrder(_ context.Context, po *domain.PurchaseOrder) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.poUpserts++
	d.pos[po.ID] = po
	return nil
}

func (d *recordingDB) GetPurchaseOrderByID(_ context.Context, id, tenantID string) (*domain.PurchaseOrder, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	po, ok := d.pos[id]
	if !ok || po.TenantID != tenantID {
		return nil, fmt.Errorf("po %s not found", id)
	}
	return po, nil
}

func (d *recordingDB) ListPurchaseOrders(_ context.Context, _ string, _, _ int) ([]*domain.PurchaseOrder, error) {
	return nil, nil
}

func (d *recordingDB) UpsertGoodsReceipt(_ context.Context, gr *domain.GoodsReceipt) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.grUpserts++
	d.grs[gr.ID] = gr
	return nil
}

func (d *recordingDB) GetGoodsReceiptByID(_ context.Context, id, tenantID string) (*domain.GoodsReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	gr, ok := d.grs[id]
	if !ok || gr.TenantID != tenantID {
		return nil, fmt.Errorf("gr %s not found", id)
	}
	return gr, nil
}

func (d *recordingDB) ListGoodsReceipts(_ context.Context, _, _ string, _, _ int) ([]*domain.GoodsReceipt, error) {
	return nil, nil
}

func (d *recordingDB) UpsertInvoice(_ context.Context, inv *domain.Invoice) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.invUpserts++
	d.invs[inv.ID] = inv
	return nil
}

func (d *recordingDB) GetInvoiceByID(_ context.Context, id, tenantID string) (*domain.Invoice, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	inv, ok := d.invs[id]
	if !ok || inv.TenantID != tenantID {
		return nil, fmt.Errorf("inv %s not found", id)
	}
	return inv, nil
}

func (d *recordingDB) ListInvoices(_ context.Context, _, _ string, _, _ int) ([]*domain.Invoice, error) {
	return nil, nil
}

func (d *recordingDB) UpsertExceptionCase(_ context.Context, ec *domain.ExceptionCase) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.excUpserts++
	d.excs[ec.ID] = ec
	return nil
}

func (d *recordingDB) GetExceptionCaseByID(_ context.Context, id, tenantID string) (*domain.ExceptionCase, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ec, ok := d.excs[id]
	if !ok || ec.TenantID != tenantID {
		return nil, fmt.Errorf("exc %s not found", id)
	}
	return ec, nil
}

func (d *recordingDB) ListExceptionCases(_ context.Context, _, _, _ string, _, _ int) ([]*domain.ExceptionCase, error) {
	return nil, nil
}

func (d *recordingDB) UpdateExceptionCaseStatus(_ context.Context, _, _, _ string) error {
	return nil
}

// -- Job / audit (stubbed to satisfy interface; job record still the summary) -

func (d *recordingDB) UpsertJob(_ context.Context, job *domain.Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jobs[job.ID] = job
	return nil
}

func (d *recordingDB) GetJob(_ context.Context, id, tenantID string) (*domain.Job, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[id]
	if !ok || j.TenantID != tenantID {
		return nil, fmt.Errorf("job %s not found", id)
	}
	return j, nil
}

func (d *recordingDB) ListJobs(_ context.Context, _ string, _ domain.WorkflowType, _ domain.JobStatus) ([]*domain.Job, error) {
	return nil, nil
}

func (d *recordingDB) AppendAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.audit = append(d.audit, e)
	return nil
}

func (d *recordingDB) ListAuditEvents(_ context.Context, _, _, _ string, _ int) ([]*domain.AuditEvent, error) {
	return nil, nil
}

// -- Vendor / Document / HITL / Ops / Tenant / Usage (stubs) ----------------

func (d *recordingDB) UpsertVendor(_ context.Context, _ *domain.Vendor) error { return nil }
func (d *recordingDB) GetVendor(_ context.Context, _, _ string) (*domain.Vendor, error) {
	return nil, fmt.Errorf("not found")
}
func (d *recordingDB) ListVendors(_ context.Context, _ string) ([]*domain.Vendor, error) { return nil, nil }

func (d *recordingDB) UpsertDocument(_ context.Context, _ *domain.Document) error { return nil }
func (d *recordingDB) GetDocument(_ context.Context, _, _ string) (*domain.Document, error) {
	return nil, fmt.Errorf("not found")
}
func (d *recordingDB) FindBySHA256(_ context.Context, _, _ string) (*domain.Document, error) { return nil, nil }

func (d *recordingDB) UpsertHITLRequest(_ context.Context, _ *domain.HITLRequest) error { return nil }
func (d *recordingDB) GetHITLRequest(_ context.Context, _, _ string) (*domain.HITLRequest, error) {
	return nil, fmt.Errorf("not found")
}
func (d *recordingDB) ListPendingHITL(_ context.Context, _ string) ([]*domain.HITLRequest, error) {
	return nil, nil
}
func (d *recordingDB) ListHITLRequests(_ context.Context, _, _ string, _, _ int) ([]*domain.HITLRequest, error) {
	return nil, nil
}

func (d *recordingDB) GetRecentJobs(_ context.Context, _ string, _ int) ([]*domain.Job, error) {
	return nil, nil
}
func (d *recordingDB) GetRiskyVendors(_ context.Context, _ string) ([]*domain.Vendor, error) {
	return nil, nil
}
func (d *recordingDB) GetRecentCompliance(_ context.Context, _ string, _ int) ([]*domain.ComplianceRecord, error) {
	return nil, nil
}

func (d *recordingDB) IncrementUsage(_ context.Context, _ string, _ domain.Metric, _ int64) error { return nil }
func (d *recordingDB) GetUsage(_ context.Context, _ string, _ domain.Metric) (int64, error) {
	return 0, nil
}
func (d *recordingDB) GetCurrentPeriodUsage(_ context.Context, _ string) (map[domain.Metric]int64, error) {
	return nil, nil
}
func (d *recordingDB) CheckLimit(_ context.Context, _ string, _ domain.Metric) (bool, int64, int64, error) {
	return true, 0, 0, nil
}

func (d *recordingDB) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	return domain.NewTenant(id, id, id), nil
}
func (d *recordingDB) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	return domain.NewTenant(slug, slug, slug), nil
}
func (d *recordingDB) CreateTenant(_ context.Context, _ *domain.Tenant) error { return nil }
func (d *recordingDB) ListTenants(_ context.Context) ([]*domain.Tenant, error) { return nil, nil }
func (d *recordingDB) UpdateTenantStatus(_ context.Context, _, _ string) error { return nil }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSignalPersistence_PurchaseOrder(t *testing.T) {
	db := newRecordingDB()
	ctx := context.Background()

	tenantID := "tenant-po"
	jobID := "job-po-1"

	po := &domain.PurchaseOrder{
		ID:          "po_tenant-po_job-po-1",
		TenantID:    tenantID,
		DocumentNo:  "PO-2026-001",
		VendorGSTIN: "22AAAAA0000A1Z5",
	}
	result := &agents.SignalResult{
		DocumentType: string(domain.DocumentTypePurchaseOrder),
		Mapped:       po,
	}

	job := &agents.SignalJob{TenantID: tenantID, JobID: jobID}
	if err := agents.PersistSignalResult(ctx, db, job, result); err != nil {
		t.Fatalf("PersistSignalResult returned error: %v", err)
	}

	if db.poUpserts != 1 {
		t.Errorf("expected 1 UpsertPurchaseOrder call, got %d", db.poUpserts)
	}
	got, ok := db.pos[po.ID]
	if !ok {
		t.Fatalf("purchase order %s was not persisted", po.ID)
	}
	if got.TenantID != tenantID {
		t.Errorf("persisted PO tenant = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "PO-2026-001" {
		t.Errorf("persisted PO document_no = %q, want PO-2026-001", got.DocumentNo)
	}
	if db.grUpserts != 0 || db.invUpserts != 0 {
		t.Errorf("expected no GRN/Invoice upserts for a PO signal, got gr=%d inv=%d", db.grUpserts, db.invUpserts)
	}
	if db.excUpserts != 0 {
		t.Errorf("expected 0 exception upserts, got %d", db.excUpserts)
	}
}

func TestSignalPersistence_GoodsReceipt(t *testing.T) {
	db := newRecordingDB()
	ctx := context.Background()

	tenantID := "tenant-gr"
	jobID := "job-gr-1"

	gr := &domain.GoodsReceipt{
		ID:          "grn_tenant-gr_job-gr-1",
		TenantID:    tenantID,
		DocumentNo:  "GRN-2026-001",
		VendorGSTIN: "22AAAAA0000A1Z5",
	}
	result := &agents.SignalResult{
		DocumentType: string(domain.DocumentTypeGoodsReceipt),
		Mapped:       gr,
	}

	job := &agents.SignalJob{TenantID: tenantID, JobID: jobID}
	if err := agents.PersistSignalResult(ctx, db, job, result); err != nil {
		t.Fatalf("PersistSignalResult returned error: %v", err)
	}

	if db.grUpserts != 1 {
		t.Errorf("expected 1 UpsertGoodsReceipt call, got %d", db.grUpserts)
	}
	got, ok := db.grs[gr.ID]
	if !ok {
		t.Fatalf("goods receipt %s was not persisted", gr.ID)
	}
	if got.TenantID != tenantID {
		t.Errorf("persisted GRN tenant = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "GRN-2026-001" {
		t.Errorf("persisted GRN document_no = %q, want GRN-2026-001", got.DocumentNo)
	}
	if db.poUpserts != 0 || db.invUpserts != 0 {
		t.Errorf("expected no PO/Invoice upserts for a GRN signal, got po=%d inv=%d", db.poUpserts, db.invUpserts)
	}
}

func TestSignalPersistence_Invoice(t *testing.T) {
	db := newRecordingDB()
	ctx := context.Background()

	tenantID := "tenant-inv"
	jobID := "job-inv-1"

	inv := &domain.Invoice{
		ID:          "inv_tenant-inv_job-inv-1",
		TenantID:    tenantID,
		DocumentNo:  "INV-2026-001",
		VendorGSTIN: "22AAAAA0000A1Z5",
	}
	result := &agents.SignalResult{
		DocumentType: string(domain.DocumentTypeInvoice),
		Mapped:       inv,
	}

	job := &agents.SignalJob{TenantID: tenantID, JobID: jobID}
	if err := agents.PersistSignalResult(ctx, db, job, result); err != nil {
		t.Fatalf("PersistSignalResult returned error: %v", err)
	}

	if db.invUpserts != 1 {
		t.Errorf("expected 1 UpsertInvoice call, got %d", db.invUpserts)
	}
	got, ok := db.invs[inv.ID]
	if !ok {
		t.Fatalf("invoice %s was not persisted", inv.ID)
	}
	if got.TenantID != tenantID {
		t.Errorf("persisted Invoice tenant = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "INV-2026-001" {
		t.Errorf("persisted Invoice document_no = %q, want INV-2026-001", got.DocumentNo)
	}
	if db.poUpserts != 0 || db.grUpserts != 0 {
		t.Errorf("expected no PO/GRN upserts for an Invoice signal, got po=%d gr=%d", db.poUpserts, db.grUpserts)
	}
}

func TestSignalPersistence_ExceptionsPersisted(t *testing.T) {
	db := newRecordingDB()
	ctx := context.Background()

	tenantID := "tenant-exc"
	jobID := "job-exc-1"

	po := &domain.PurchaseOrder{
		ID:          "po_tenant-exc_job-exc-1",
		TenantID:    tenantID,
		DocumentNo:  "PO-2026-EXC",
		VendorGSTIN: "22AAAAA0000A1Z5",
	}
	// Exception with NO TenantID and NO Status — the helper must backfill both
	// from the job tenant and the OPEN default.
	exc := domain.ExceptionCase{
		ID:           "exc-1",
		Type:         domain.MismatchMissingGRN,
		Severity:     domain.SeverityMedium,
		VendorGSTIN:  "22AAAAA0000A1Z5",
		Description:  "No goods receipt found for PO PO-2026-EXC",
	}
	result := &agents.SignalResult{
		DocumentType: string(domain.DocumentTypePurchaseOrder),
		Mapped:       po,
		Exceptions:   []domain.ExceptionCase{exc},
	}

	job := &agents.SignalJob{TenantID: tenantID, JobID: jobID}
	if err := agents.PersistSignalResult(ctx, db, job, result); err != nil {
		t.Fatalf("PersistSignalResult returned error: %v", err)
	}

	if db.poUpserts != 1 {
		t.Errorf("expected 1 UpsertPurchaseOrder call, got %d", db.poUpserts)
	}
	if db.excUpserts != 1 {
		t.Errorf("expected 1 UpsertExceptionCase call, got %d", db.excUpserts)
	}
	got, ok := db.excs["exc-1"]
	if !ok {
		t.Fatalf("exception exc-1 was not persisted")
	}
	if got.TenantID != tenantID {
		t.Errorf("exception TenantID = %q, want %q (must be backfilled from job)", got.TenantID, tenantID)
	}
	if got.Status != domain.ExceptionStatusOpen {
		t.Errorf("exception Status = %q, want %q (must default to OPEN)", got.Status, domain.ExceptionStatusOpen)
	}
}

func TestSignalPersistence_NoMappedEntityNoWrite(t *testing.T) {
	db := newRecordingDB()
	ctx := context.Background()

	// A result with no mapped entity (e.g. unsupported doc type) must not write
	// any manufacturing row, but must also not error.
	result := &agents.SignalResult{
		DocumentType: string(domain.DocumentTypeUnknown),
		Mapped:       nil,
	}
	job := &agents.SignalJob{TenantID: "tenant-x", JobID: "job-x"}
	if err := agents.PersistSignalResult(ctx, db, job, result); err != nil {
		t.Fatalf("PersistSignalResult returned error for empty result: %v", err)
	}
	if db.poUpserts != 0 || db.grUpserts != 0 || db.invUpserts != 0 || db.excUpserts != 0 {
		t.Errorf("expected no manufacturing writes for empty result, got po=%d gr=%d inv=%d exc=%d",
			db.poUpserts, db.grUpserts, db.invUpserts, db.excUpserts)
	}
}
