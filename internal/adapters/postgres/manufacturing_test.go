package postgres

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestAdapter builds a migrated postgres adapter for integration tests.
// It skips the test when DATABASE_URL is unset (via GetTestDatabaseURL).
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	connStr := GetTestDatabaseURL(t) // skips when unset
	ctx := context.Background()
	adapter, err := NewAdapter(ctx, connStr)
	if err != nil {
		t.Fatalf("NewAdapter failed: %v", err)
	}
	t.Cleanup(func() { adapter.Close() })
	return adapter
}

// withTenantTx opens a transaction, sets the tenant RLS context, and returns
// the transaction plus a context carrying it. The caller must commit/rollback.
// This mirrors the pattern in rls_test.go / tenant_test.go.
func withTenantTx(t *testing.T, adapter *Adapter, tenantID string) (context.Context, pgx.Tx) {
	t.Helper()
	ctx := context.Background()
	tx, err := adapter.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if err := SetTenantContextTx(ctx, tx, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("SetTenantContextTx failed: %v", err)
	}
	return WithTx(ctx, tx), tx
}

// ensureTenant inserts a tenant row (tenants table has NO RLS, so this works
// without tenant context). It is idempotent via ON CONFLICT.
func ensureTenant(t *testing.T, adapter *Adapter, tenantID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := adapter.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin (ensureTenant) failed: %v", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, plan, status)
		VALUES ($1, $1, $1, 'starter', 'active')
		ON CONFLICT (id) DO NOTHING`,
		tenantID)
	if err != nil {
		t.Fatalf("insert tenant %s failed: %v", tenantID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit (ensureTenant) failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 1: Tenant isolation (RLS) — tenant B cannot read tenant A's PO
// ---------------------------------------------------------------------------

func TestManufacturingTenantIsolation(t *testing.T) {
	adapter := newTestAdapter(t)

	tenantA := "mfg-iso-a-" + uuid.New().String()[:8]
	tenantB := "mfg-iso-b-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantA)
	ensureTenant(t, adapter, tenantB)

	poID := "po-iso-" + uuid.New().String()[:8]

	// Insert a PO under tenant A.
	{
		ctx, tx := withTenantTx(t, adapter, tenantA)
		po := &domain.PurchaseOrder{
			ID:          poID,
			TenantID:    tenantA,
			DocumentNo:  "PO-ISO-001",
			VendorGSTIN: "29ABCDE1234F1Z5",
			OrderDate:   time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
			Version:     0,
			Lines: []domain.PurchaseOrderLine{
				{LineRef: "L1", ItemCode: "WIDGET", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
			},
		}
		if err := adapter.UpsertPurchaseOrder(ctx, po); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("UpsertPurchaseOrder (tenant A) failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit (tenant A insert) failed: %v", err)
		}
	}

	// Tenant A can read its own PO.
	{
		ctx, tx := withTenantTx(t, adapter, tenantA)
		got, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantA)
		_ = tx.Rollback(ctx)
		if err != nil {
			t.Fatalf("GetPurchaseOrderByID (tenant A) unexpected error: %v", err)
		}
		if got == nil || got.ID != poID {
			t.Fatalf("tenant A expected to read its PO %s, got %v", poID, got)
		}
	}

	// Tenant B MUST NOT be able to read tenant A's PO (RLS enforcement).
	{
		ctx, tx := withTenantTx(t, adapter, tenantB)
		got, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantB)
		_ = tx.Rollback(ctx)
		if err == nil && got != nil {
			t.Fatalf("RLS violation: tenant B read tenant A's PO %s", poID)
		}
		if got != nil {
			t.Errorf("tenant B GetPurchaseOrderByID returned non-nil entity; want nil")
		}
		// Either a not-found error (pgx.ErrNoRows wrapped) or any error is acceptable;
		// the key assertion is that the entity is not returned.
		if err == nil {
			t.Errorf("expected an error (not found) for tenant B reading tenant A's PO")
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2: Purchase Order roundtrip (header + line items)
// ---------------------------------------------------------------------------

func TestPurchaseOrderRoundtrip(t *testing.T) {
	adapter := newTestAdapter(t)
	tenantID := "mfg-po-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantID)

	poID := "po-rt-" + uuid.New().String()[:8]
	orderDate := time.Date(2026, 2, 1, 9, 30, 0, 0, time.UTC)

	inserted := &domain.PurchaseOrder{
		ID:          poID,
		TenantID:    tenantID,
		DocumentNo:  "PO-RT-001",
		VendorGSTIN: "29ABCDE1234F1Z5",
		OrderDate:   orderDate,
		Version:     0,
		Lines: []domain.PurchaseOrderLine{
			{LineRef: "L1", ItemCode: "WIDGET-A", ItemDesc: "Alpha widget", UoM: "NOS", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
			{LineRef: "L2", ItemCode: "WIDGET-B", ItemDesc: "Beta widget", UoM: "NOS", Quantity: 5, UnitRate: 200, TaxRatePct: 12},
		},
	}

	{
		ctx, tx := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertPurchaseOrder(ctx, inserted); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("UpsertPurchaseOrder failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}

	ctx, tx := withTenantTx(t, adapter, tenantID)
	got, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetPurchaseOrderByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetPurchaseOrderByID returned nil")
	}

	if got.ID != poID {
		t.Errorf("ID = %q, want %q", got.ID, poID)
	}
	if got.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "PO-RT-001" {
		t.Errorf("DocumentNo = %q, want PO-RT-001", got.DocumentNo)
	}
	if got.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want 29ABCDE1234F1Z5", got.VendorGSTIN)
	}
	if !got.OrderDate.Equal(orderDate) {
		t.Errorf("OrderDate = %v, want %v", got.OrderDate, orderDate)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(got.Lines))
	}

	byRef := map[string]domain.PurchaseOrderLine{}
	for _, l := range got.Lines {
		byRef[l.LineRef] = l
	}
	l1, ok := byRef["L1"]
	if !ok {
		t.Fatal("line L1 missing")
	}
	if l1.ItemCode != "WIDGET-A" || l1.Quantity != 10 || l1.UnitRate != 100 || l1.TaxRatePct != 18 {
		t.Errorf("L1 = %+v, unexpected values", l1)
	}
	l2, ok := byRef["L2"]
	if !ok {
		t.Fatal("line L2 missing")
	}
	if l2.ItemCode != "WIDGET-B" || l2.Quantity != 5 || l2.UnitRate != 200 || l2.TaxRatePct != 12 {
		t.Errorf("L2 = %+v, unexpected values", l2)
	}
}

// ---------------------------------------------------------------------------
// Test 3: Goods Receipt roundtrip
// ---------------------------------------------------------------------------

func TestGoodsReceiptRoundtrip(t *testing.T) {
	adapter := newTestAdapter(t)
	tenantID := "mfg-gr-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantID)

	grnID := "grn-rt-" + uuid.New().String()[:8]
	receiptDate := time.Date(2026, 2, 5, 14, 0, 0, 0, time.UTC)

	inserted := &domain.GoodsReceipt{
		ID:          grnID,
		TenantID:    tenantID,
		DocumentNo:  "GRN-RT-001",
		VendorGSTIN: "29ABCDE1234F1Z5",
		PONumber:    "PO-PARENT-001",
		ReceiptDate: receiptDate,
		Version:     0,
		Lines: []domain.GoodsReceiptLine{
			{LineRef: "G1", POLineRef: "L1", ItemCode: "WIDGET-A", ReceivedQty: 10, UoM: "NOS"},
			{LineRef: "G2", POLineRef: "L2", ItemCode: "WIDGET-B", ReceivedQty: 3, UoM: "NOS"},
		},
	}

	{
		ctx, tx := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertGoodsReceipt(ctx, inserted); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("UpsertGoodsReceipt failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}

	ctx, tx := withTenantTx(t, adapter, tenantID)
	got, err := adapter.GetGoodsReceiptByID(ctx, grnID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetGoodsReceiptByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetGoodsReceiptByID returned nil")
	}

	if got.ID != grnID {
		t.Errorf("ID = %q, want %q", got.ID, grnID)
	}
	if got.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "GRN-RT-001" {
		t.Errorf("DocumentNo = %q, want GRN-RT-001", got.DocumentNo)
	}
	if got.PONumber != "PO-PARENT-001" {
		t.Errorf("PONumber = %q, want PO-PARENT-001", got.PONumber)
	}
	if got.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want 29ABCDE1234F1Z5", got.VendorGSTIN)
	}
	if !got.ReceiptDate.Equal(receiptDate) {
		t.Errorf("ReceiptDate = %v, want %v", got.ReceiptDate, receiptDate)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(got.Lines))
	}

	byRef := map[string]domain.GoodsReceiptLine{}
	for _, l := range got.Lines {
		byRef[l.LineRef] = l
	}
	g1, ok := byRef["G1"]
	if !ok {
		t.Fatal("line G1 missing")
	}
	if g1.POLineRef != "L1" || g1.ItemCode != "WIDGET-A" || g1.ReceivedQty != 10 {
		t.Errorf("G1 = %+v, unexpected values", g1)
	}
	g2, ok := byRef["G2"]
	if !ok {
		t.Fatal("line G2 missing")
	}
	if g2.POLineRef != "L2" || g2.ItemCode != "WIDGET-B" || g2.ReceivedQty != 3 {
		t.Errorf("G2 = %+v, unexpected values", g2)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Invoice roundtrip
// ---------------------------------------------------------------------------

func TestInvoiceRoundtrip(t *testing.T) {
	adapter := newTestAdapter(t)
	tenantID := "mfg-inv-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantID)

	invID := "inv-rt-" + uuid.New().String()[:8]
	invoiceDate := time.Date(2026, 2, 8, 11, 0, 0, 0, time.UTC)

	inserted := &domain.Invoice{
		ID:          invID,
		TenantID:    tenantID,
		DocumentNo:  "INV-RT-001",
		VendorGSTIN: "29ABCDE1234F1Z5",
		PONumber:    "PO-PARENT-001",
		InvoiceDate: invoiceDate,
		Version:     0,
		Lines: []domain.InvoiceLine{
			{LineRef: "I1", POLineRef: "L1", ItemCode: "WIDGET-A", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
			{LineRef: "I2", POLineRef: "L2", ItemCode: "WIDGET-B", Quantity: 5, UnitRate: 200, TaxRatePct: 12},
		},
	}

	{
		ctx, tx := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertInvoice(ctx, inserted); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("UpsertInvoice failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}

	ctx, tx := withTenantTx(t, adapter, tenantID)
	got, err := adapter.GetInvoiceByID(ctx, invID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetInvoiceByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetInvoiceByID returned nil")
	}

	if got.ID != invID {
		t.Errorf("ID = %q, want %q", got.ID, invID)
	}
	if got.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenantID)
	}
	if got.DocumentNo != "INV-RT-001" {
		t.Errorf("DocumentNo = %q, want INV-RT-001", got.DocumentNo)
	}
	if got.PONumber != "PO-PARENT-001" {
		t.Errorf("PONumber = %q, want PO-PARENT-001", got.PONumber)
	}
	if got.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want 29ABCDE1234F1Z5", got.VendorGSTIN)
	}
	if !got.InvoiceDate.Equal(invoiceDate) {
		t.Errorf("InvoiceDate = %v, want %v", got.InvoiceDate, invoiceDate)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(got.Lines))
	}

	byRef := map[string]domain.InvoiceLine{}
	for _, l := range got.Lines {
		byRef[l.LineRef] = l
	}
	i1, ok := byRef["I1"]
	if !ok {
		t.Fatal("line I1 missing")
	}
	if i1.POLineRef != "L1" || i1.ItemCode != "WIDGET-A" || i1.Quantity != 10 || i1.UnitRate != 100 || i1.TaxRatePct != 18 {
		t.Errorf("I1 = %+v, unexpected values", i1)
	}
	i2, ok := byRef["I2"]
	if !ok {
		t.Fatal("line I2 missing")
	}
	if i2.POLineRef != "L2" || i2.ItemCode != "WIDGET-B" || i2.Quantity != 5 || i2.UnitRate != 200 || i2.TaxRatePct != 12 {
		t.Errorf("I2 = %+v, unexpected values", i2)
	}
}

// ---------------------------------------------------------------------------
// Test 5: Optimistic locking (version conflict)
// ---------------------------------------------------------------------------

func TestOptimisticLocking(t *testing.T) {
	adapter := newTestAdapter(t)
	tenantID := "mfg-ol-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantID)

	poID := "po-ol-" + uuid.New().String()[:8]

	base := func(version int) *domain.PurchaseOrder {
		return &domain.PurchaseOrder{
			ID:          poID,
			TenantID:    tenantID,
			DocumentNo:  "PO-OL-001",
			VendorGSTIN: "29ABCDE1234F1Z5",
			OrderDate:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			Version:     version,
			Lines: []domain.PurchaseOrderLine{
				{LineRef: "L1", ItemCode: "WIDGET", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
			},
		}
	}

	// Initial insert (version 0).
	{
		ctx, tx := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertPurchaseOrder(ctx, base(0)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("initial UpsertPurchaseOrder failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}

	// Read current version.
	ctx, tx := withTenantTx(t, adapter, tenantID)
	current, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetPurchaseOrderByID (read) failed: %v", err)
	}
	if current == nil {
		t.Fatal("current PO is nil")
	}
	v0 := current.Version

	// Real update using the current version -> should succeed and bump version.
	{
		updated := base(v0)
		updated.DocumentNo = "PO-OL-001-REV"
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertPurchaseOrder(ctx2, updated); err != nil {
			_ = tx2.Rollback(ctx2)
			t.Fatalf("UpsertPurchaseOrder (real update) failed: %v", err)
		}
		if err := tx2.Commit(ctx2); err != nil {
			t.Fatalf("commit (real update) failed: %v", err)
		}
	}

	// Confirm version incremented.
	ctx, tx = withTenantTx(t, adapter, tenantID)
	bumped, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetPurchaseOrderByID (after update) failed: %v", err)
	}
	if bumped.Version != v0+1 {
		t.Fatalf("version after real update = %d, want %d", bumped.Version, v0+1)
	}
	if bumped.DocumentNo != "PO-OL-001-REV" {
		t.Errorf("DocumentNo after update = %q, want PO-OL-001-REV", bumped.DocumentNo)
	}
	v1 := bumped.Version

	// Stale update using the OLD version (v0) -> must return ErrVersionConflict.
	{
		stale := base(v0) // uses the now-stale version
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		err := adapter.UpsertPurchaseOrder(ctx2, stale)
		_ = tx2.Rollback(ctx2)
		if !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("stale update: expected ErrVersionConflict, got %v", err)
		}
	}

	// Correct update using the CURRENT version (v1) -> must succeed and bump again.
	{
		correct := base(v1)
		correct.DocumentNo = "PO-OL-001-REV2"
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertPurchaseOrder(ctx2, correct); err != nil {
			_ = tx2.Rollback(ctx2)
			t.Fatalf("UpsertPurchaseOrder (correct update) failed: %v", err)
		}
		if err := tx2.Commit(ctx2); err != nil {
			t.Fatalf("commit (correct update) failed: %v", err)
		}
	}

	ctx, tx = withTenantTx(t, adapter, tenantID)
	final, err := adapter.GetPurchaseOrderByID(ctx, poID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetPurchaseOrderByID (final) failed: %v", err)
	}
	if final.Version != v1+1 {
		t.Errorf("final version = %d, want %d", final.Version, v1+1)
	}
	if final.DocumentNo != "PO-OL-001-REV2" {
		t.Errorf("final DocumentNo = %q, want PO-OL-001-REV2", final.DocumentNo)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Exception Case persistence + listing by status/tenant
// ---------------------------------------------------------------------------

func TestExceptionCasePersistence(t *testing.T) {
	adapter := newTestAdapter(t)
	tenantID := "mfg-exc-" + uuid.New().String()[:8]
	ensureTenant(t, adapter, tenantID)

	ecID := "exc-rt-" + uuid.New().String()[:8]

	inserted := &domain.ExceptionCase{
		ID:             ecID,
		TenantID:       tenantID,
		Type:           domain.MismatchQtyVariance,
		Severity:       domain.SeverityHigh,
		VendorGSTIN:    "29ABCDE1234F1Z5",
		POLineRef:      "L1",
		InvoiceLineRef: "I1",
		Description:    "Invoice qty 12 vs PO qty 10 on item WIDGET-A (20.00%)",
		Status:         "OPEN",
		Metadata:       map[string]any{"po_qty": 10, "invoice_qty": 12, "variance_pct": 20.0},
	}

	{
		ctx, tx := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpsertExceptionCase(ctx, inserted); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("UpsertExceptionCase failed: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}

	// Get by ID.
	ctx, tx := withTenantTx(t, adapter, tenantID)
	got, err := adapter.GetExceptionCaseByID(ctx, ecID, tenantID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetExceptionCaseByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetExceptionCaseByID returned nil")
	}
	if got.ID != ecID {
		t.Errorf("ID = %q, want %q", got.ID, ecID)
	}
	if got.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenantID)
	}
	if got.Type != domain.MismatchQtyVariance {
		t.Errorf("Type = %q, want %q", got.Type, domain.MismatchQtyVariance)
	}
	if got.Severity != domain.SeverityHigh {
		t.Errorf("Severity = %q, want %q", got.Severity, domain.SeverityHigh)
	}
	if got.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want 29ABCDE1234F1Z5", got.VendorGSTIN)
	}
	if got.POLineRef != "L1" {
		t.Errorf("POLineRef = %q, want L1", got.POLineRef)
	}
	if got.InvoiceLineRef != "I1" {
		t.Errorf("InvoiceLineRef = %q, want I1", got.InvoiceLineRef)
	}
	if got.Status != "OPEN" {
		t.Errorf("Status = %q, want OPEN", got.Status)
	}
	if got.Description == "" {
		t.Error("Description should not be empty")
	}

	// List by status=OPEN for this tenant -> should include our case.
	{
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		list, err := adapter.ListExceptionCases(ctx2, tenantID, "OPEN", "", 50, 0)
		_ = tx2.Rollback(ctx2)
		if err != nil {
			t.Fatalf("ListExceptionCases (OPEN) failed: %v", err)
		}
		found := false
		for _, e := range list {
			if e.ID == ecID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exception case %s not found in OPEN list", ecID)
		}
	}

	// List by a different status -> should NOT include our OPEN case.
	{
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		list, err := adapter.ListExceptionCases(ctx2, tenantID, "RESOLVED", "", 50, 0)
		_ = tx2.Rollback(ctx2)
		if err != nil {
			t.Fatalf("ListExceptionCases (RESOLVED) failed: %v", err)
		}
		for _, e := range list {
			if e.ID == ecID {
				t.Errorf("OPEN exception case %s leaked into RESOLVED list", ecID)
			}
		}
	}

	// Update status to RESOLVED, then confirm it appears under RESOLVED.
	{
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		if err := adapter.UpdateExceptionCaseStatus(ctx2, ecID, tenantID, "RESOLVED"); err != nil {
			_ = tx2.Rollback(ctx2)
			t.Fatalf("UpdateExceptionCaseStatus failed: %v", err)
		}
		if err := tx2.Commit(ctx2); err != nil {
			t.Fatalf("commit (status update) failed: %v", err)
		}
	}

	{
		ctx2, tx2 := withTenantTx(t, adapter, tenantID)
		list, err := adapter.ListExceptionCases(ctx2, tenantID, "RESOLVED", "", 50, 0)
		_ = tx2.Rollback(ctx2)
		if err != nil {
			t.Fatalf("ListExceptionCases (RESOLVED after update) failed: %v", err)
		}
		found := false
		for _, e := range list {
			if e.ID == ecID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exception case %s not found in RESOLVED list after update", ecID)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 7: RLS enforced without tenant context (FORCE RLS)
//
// The application connects as a superuser, which bypasses RLS. To validate
// that FORCE RLS actually blocks cross-tenant reads at the database layer, we
// connect as a dedicated non-superuser role (mfg_rls_test) that has SELECT on
// the manufacturing tables but is subject to RLS. With no tenant context set,
// app.current_tenant_id() resolves to 'default' and no real row matches, so
// every tenant-scoped table must return 0 rows.
// ---------------------------------------------------------------------------

const rlsTestRole = "mfg_rls_test"

// ensureRLSTestRole creates (idempotently) a non-superuser role and grants it
// SELECT on the manufacturing tables so RLS is enforced for it.
func ensureRLSTestRole(t *testing.T, adapter *Adapter) {
	t.Helper()
	ctx := context.Background()
	tx, err := adapter.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin (ensureRLSTestRole) failed: %v", err)
	}
	defer tx.Rollback(ctx)

	stmts := []string{
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '" + rlsTestRole + "') THEN CREATE ROLE " + rlsTestRole + " NOSUPERUSER NOBYPASSRLS LOGIN PASSWORD 'mfg_rls_test'; END IF; END $$;",
		"GRANT CONNECT ON DATABASE opscore TO " + rlsTestRole,
		"GRANT USAGE ON SCHEMA public TO " + rlsTestRole,
		"GRANT SELECT ON purchase_orders, purchase_order_lines, goods_receipts, goods_receipt_lines, invoices, invoice_lines, exception_cases TO " + rlsTestRole,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatalf("ensureRLSTestRole statement failed: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit (ensureRLSTestRole) failed: %v", err)
	}
}

func TestRLSEnforcedWithoutContext(t *testing.T) {
	adapter := newTestAdapter(t)
	ensureRLSTestRole(t, adapter)

	// Build a separate pool as the non-superuser RLS role.
	connStr := GetTestDatabaseURL(t)
	// Swap the superuser credentials for the RLS-test role credentials.
	rlsConnStr := swapConnUser(connStr, rlsTestRole, "mfg_rls_test")
	pool, err := pgxpool.New(context.Background(), rlsConnStr)
	if err != nil {
		t.Fatalf("creating RLS-test pool failed: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pinging RLS-test pool failed: %v", err)
	}

	// No tenant context on this connection: FORCE RLS must block all rows.
	tables := []string{
		"purchase_orders", "goods_receipts", "invoices", "exception_cases",
	}
	for _, tbl := range tables {
		var count int
		if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+tbl).Scan(&count); err != nil {
			t.Fatalf("count query on %s failed: %v", tbl, err)
		}
		if count != 0 {
			t.Errorf("FORCE RLS not enforced on %s: expected 0 rows without tenant context, got %d", tbl, count)
		}
	}
}

// swapConnUser rewrites a postgres:// DSN, replacing the user and password.
func swapConnUser(connStr, user, password string) string {
	u, err := url.Parse(connStr)
	if err != nil {
		return connStr
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
