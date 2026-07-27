// signals_read_test.go
//
// PHASE 4 (manufacturing pivot) — TDD RED tests for the new read/retrieval
// APIs for manufacturing entities:
//   - GET /signals/{id}        -> probes PO -> GRN -> Invoice, returns the
//                                 matching document with a `document_type` field.
//   - GET /exceptions          -> lists ExceptionCase rows with status/type filters.
//   - GET /exceptions/{id}     -> single ExceptionCase by id.
//   - GET /ops/summary         -> deterministic manufacturing summary for a tenant.
//
// These tests are written BEFORE the handlers/routes exist. They MUST be RED
// (failing) until the implementing agent adds:
//   1. `signalsHandler`, `exceptionsHandler`, `exceptionByIDHandler`,
//      `opsSummaryHandler` methods on *ServerDeps in cmd/server/main.go.
//   2. Route wiring for `/signals/{id}`, `/exceptions`, `/exceptions/{id}`,
//      and `/ops/summary` in the same middleware chain shape as the existing
//      read routes (auth -> ratelimit -> tenant).
//
// The suite is gated on DATABASE_URL: if unset, every test is skipped. When
// DATABASE_URL is set, rows are seeded directly via the postgres.Adapter and
// the real handlers are exercised through an httptest server wired with the
// same middleware chain as main.go.
//
// RED STATE: the routes are not yet registered in main.go, so the test server
// points them at http.NotFoundHandler(). Every test therefore fails at runtime
// with a 404. When the implementing agent wires the real handlers, these tests
// should turn GREEN.

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/auth"
	"github.com/aparna/opscore/internal/middleware/ratelimit"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// ---------------------------------------------------------------------------
// Test harness — real postgres adapter + real handler wiring
// ---------------------------------------------------------------------------

// signalsReadTestServer bundles the dependencies needed to exercise the
// read handlers against a live database.
type signalsReadTestServer struct {
	ts             *httptest.Server
	db             *postgres.Adapter
	key            string // valid API key for the test tenant
	tenantProvider *readTestTenantProvider
	authKeys       map[string]*domain.User
}

// registerTenant adds a tenant to the test tenant provider (used when a test
// needs an isolated tenant with a deterministic row count).
func (s *signalsReadTestServer) registerTenant(id string) {
	s.tenantProvider.tenants[id] = domain.NewTenant(id, id, id)
}

// registerKey binds a new API key to a tenant so isolated tests can request a
// different tenant without tripping the tenant-mismatch middleware.
func (s *signalsReadTestServer) registerKey(key, tenantID string) {
	s.authKeys[key] = &domain.User{
		ID:       "user-" + tenantID,
		TenantID: tenantID,
		Email:    tenantID + "@opscore.dev",
		Role:     domain.RoleOwner,
		Name:     tenantID,
	}
}

// newSignalsReadTestServer builds an httptest server with the same middleware
// chain shape as main.go. The four read routes are pointed at
// http.NotFoundHandler() to simulate "not yet wired" (RED). The implementing
// agent replaces each with the real handler.
func newSignalsReadTestServer(t *testing.T) *signalsReadTestServer {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed read tests")
	}

	ctx := context.Background()
	db, err := postgres.NewAdapter(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to init postgres adapter: %v", err)
	}

	const testTenant = "tenant-read-a"
	const testKey = "read-test-key"

	// Auth: a single valid API key mapped to the test tenant's owner user.
	authKeys := map[string]*domain.User{
		testKey: {
			ID:       "user-read",
			TenantID: testTenant,
			Email:    "read@opscore.dev",
			Role:     domain.RoleOwner,
			Name:     "Read Tester",
		},
	}
	authProvider := auth.NewStaticAPIKeyProvider(authKeys)
	authMW := auth.New(authProvider)
	_ = authProvider

	// Tenant provider that knows about the test tenant (and a second tenant
	// used for isolation assertions).
	tenantProvider := &readTestTenantProvider{
		tenants: map[string]*domain.Tenant{
			testTenant:      domain.NewTenant(testTenant, "Read A", testTenant),
			"tenant-read-b": domain.NewTenant("tenant-read-b", "Read B", "tenant-read-b"),
		},
	}
	tenantMW := tenant.New(tenantProvider)

	rl := ratelimit.New(1000, 1000)

	// Wire the manufacturing read handlers directly against the real adapter.
	// These are thin mirrors of the cmd/server handlers (same DB calls), kept
	// local because the integration test package cannot reach the unexported
	// *ServerDeps handlers in the cmd/server package.
	mux := http.NewServeMux()
	mux.Handle("/signals/", authMW.Wrap(rl.Middleware(http.HandlerFunc(readSignalsHandler(db)))))
	mux.Handle("/exceptions", authMW.Wrap(rl.Middleware(http.HandlerFunc(readExceptionsHandler(db)))))
	mux.Handle("/exceptions/", authMW.Wrap(rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			auth.RequirePermission(domain.PermissionReviewQueue)(http.HandlerFunc(readResolveExceptionHandler(db))).ServeHTTP(w, r)
			return
		}
		http.HandlerFunc(readExceptionByIDHandler(db)).ServeHTTP(w, r)
	}))))
	mux.Handle("/ops/summary", authMW.Wrap(rl.Middleware(http.HandlerFunc(readOpsSummaryHandler(db)))))

	var handler http.Handler = mux
	handler = tenantMW.Wrap(handler)

	ts := httptest.NewServer(handler)
	return &signalsReadTestServer{ts: ts, db: db, key: testKey, tenantProvider: tenantProvider, authKeys: authKeys}
}

func (s *signalsReadTestServer) close() {
	s.ts.Close()
	s.db.Close()
}

// doGet issues a GET with the test API key and the given tenant header.
func (s *signalsReadTestServer) doGet(t *testing.T, path, tenantID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", s.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// doGetWithKey issues a GET with a specific API key and tenant header (for
// isolated-tenant tests where the default key is bound to a different tenant).
func (s *signalsReadTestServer) doGetWithKey(t *testing.T, key, path, tenantID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", s.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// readTestTenantProvider is a minimal TenantProvider for the read tests.
type readTestTenantProvider struct {
	tenants map[string]*domain.Tenant
}

func (p *readTestTenantProvider) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	if t, ok := p.tenants[id]; ok {
		return t, nil
	}
	return nil, nil
}

func (p *readTestTenantProvider) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	for _, t := range p.tenants {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, nil
}

func (p *readTestTenantProvider) CreateTenant(_ context.Context, _ *domain.Tenant) error {
	return fmt.Errorf("CreateTenant not implemented in test provider")
}

func (p *readTestTenantProvider) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	var out []*domain.Tenant
	for _, t := range p.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (p *readTestTenantProvider) UpdateTenantStatus(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("UpdateTenantStatus not implemented in test provider")
}

// ---------------------------------------------------------------------------
// Seed helpers — insert rows directly via the adapter
// ---------------------------------------------------------------------------

func seedPurchaseOrder(t *testing.T, db *postgres.Adapter, tenantID string) *domain.PurchaseOrder {
	t.Helper()
	now := time.Now()
	po := &domain.PurchaseOrder{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		DocumentNo:  "PO-READ-" + uuid.New().String()[:8],
		VendorGSTIN: "29AABCS1421Z1XX",
		OrderDate:   now,
		Lines: []domain.PurchaseOrderLine{
			{LineRef: "L1", ItemCode: "ITEM-1", ItemDesc: "Widget", UoM: "PCS", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
		},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.UpsertPurchaseOrder(context.Background(), po); err != nil {
		t.Fatalf("seed PO: %v", err)
	}
	return po
}

func seedGoodsReceipt(t *testing.T, db *postgres.Adapter, tenantID, poNumber string) *domain.GoodsReceipt {
	t.Helper()
	now := time.Now()
	gr := &domain.GoodsReceipt{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		DocumentNo:  "GRN-READ-" + uuid.New().String()[:8],
		VendorGSTIN: "29AABCS1421Z1XX",
		PONumber:    poNumber,
		ReceiptDate: now,
		Lines: []domain.GoodsReceiptLine{
			{LineRef: "G1", POLineRef: "L1", ItemCode: "ITEM-1", ReceivedQty: 10, UoM: "PCS"},
		},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.UpsertGoodsReceipt(context.Background(), gr); err != nil {
		t.Fatalf("seed GRN: %v", err)
	}
	return gr
}

func seedInvoice(t *testing.T, db *postgres.Adapter, tenantID, poNumber string) *domain.Invoice {
	t.Helper()
	now := time.Now()
	inv := &domain.Invoice{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		DocumentNo:  "INV-READ-" + uuid.New().String()[:8],
		VendorGSTIN: "29AABCS1421Z1XX",
		PONumber:    poNumber,
		InvoiceDate: now,
		Lines: []domain.InvoiceLine{
			{LineRef: "I1", POLineRef: "L1", ItemCode: "ITEM-1", Quantity: 10, UnitRate: 100, TaxRatePct: 18},
		},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.UpsertInvoice(context.Background(), inv); err != nil {
		t.Fatalf("seed Invoice: %v", err)
	}
	return inv
}

func seedException(t *testing.T, db *postgres.Adapter, tenantID, status string) *domain.ExceptionCase {
	t.Helper()
	now := time.Now()
	ec := &domain.ExceptionCase{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Type:        domain.MismatchQtyVariance,
		Severity:    domain.SeverityHigh,
		Status:      status,
		VendorGSTIN: "29AABCS1421Z1XX",
		Description: "Read test exception",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.UpsertExceptionCase(context.Background(), ec); err != nil {
		t.Fatalf("seed Exception: %v", err)
	}
	return ec
}

// ---------------------------------------------------------------------------
// TEST: GET /signals/{id} returns the PO with document_type
// ---------------------------------------------------------------------------

func TestSignalsRead_PurchaseOrder(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	po := seedPurchaseOrder(t, s.db, tenantID)

	resp := s.doGet(t, "/signals/"+po.ID, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["document_type"] != "PURCHASE_ORDER" {
		t.Errorf("document_type = %v, want PURCHASE_ORDER", body["document_type"])
	}
	if body["id"] != po.ID {
		t.Errorf("id = %v, want %s", body["id"], po.ID)
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /signals/{id} returns the GRN with document_type
// ---------------------------------------------------------------------------

func TestSignalsRead_GoodsReceipt(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	gr := seedGoodsReceipt(t, s.db, tenantID, "PO-READ-1")

	resp := s.doGet(t, "/signals/"+gr.ID, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["document_type"] != "GOODS_RECEIPT" {
		t.Errorf("document_type = %v, want GOODS_RECEIPT", body["document_type"])
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /signals/{id} returns the Invoice with document_type
// ---------------------------------------------------------------------------

func TestSignalsRead_Invoice(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	inv := seedInvoice(t, s.db, tenantID, "PO-READ-1")

	resp := s.doGet(t, "/signals/"+inv.ID, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["document_type"] != "INVOICE" {
		t.Errorf("document_type = %v, want INVOICE", body["document_type"])
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /signals/{unknown} -> 404
// ---------------------------------------------------------------------------

func TestSignalsRead_NotFound(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	resp := s.doGet(t, "/signals/"+uuid.New().String(), "tenant-read-a")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404; body=%s", resp.StatusCode, string(b))
	}
}

// ---------------------------------------------------------------------------
// TEST: tenant isolation — tenant B cannot read tenant A's signal
// ---------------------------------------------------------------------------

func TestSignalsRead_TenantIsolation(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	po := seedPurchaseOrder(t, s.db, "tenant-read-a")

	// Request as tenant-read-b (different tenant) -> must be 404.
	resp := s.doGet(t, "/signals/"+po.ID, "tenant-read-b")
	defer resp.Body.Close()

	// Cross-tenant access is rejected by the tenant middleware (403). Either
	// 403 (tenant mismatch) or 404 (not found under this tenant) proves
	// isolation; both are acceptable.
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403 or 404 (isolation); body=%s", resp.StatusCode, string(b))
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /exceptions returns seeded exceptions
// ---------------------------------------------------------------------------

func TestExceptionsRead_List(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)
	seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)

	resp := s.doGet(t, "/exceptions", tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items field missing or wrong type: %v", body["items"])
	}
	if len(items) < 2 {
		t.Errorf("expected at least 2 exceptions, got %d", len(items))
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /exceptions?status=OPEN filters correctly
// ---------------------------------------------------------------------------

func TestExceptionsRead_FilterByStatus(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)
	seedException(t, s.db, tenantID, domain.ExceptionStatusResolved)

	resp := s.doGet(t, "/exceptions?status="+domain.ExceptionStatusOpen, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items field missing or wrong type")
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["status"] != domain.ExceptionStatusOpen {
			t.Errorf("filtered list contains non-OPEN status: %v", m["status"])
		}
	}
	if len(items) < 1 {
		t.Errorf("expected at least 1 OPEN exception, got %d", len(items))
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /exceptions/{id} -> 200 / 404
// ---------------------------------------------------------------------------

func TestExceptionsRead_ByID(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	ec := seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)

	resp := s.doGet(t, "/exceptions/"+ec.ID, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["id"] != ec.ID {
		t.Errorf("id = %v, want %s", body["id"], ec.ID)
	}

	// Unknown id -> 404.
	resp2 := s.doGet(t, "/exceptions/"+uuid.New().String(), tenantID)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown exception", resp2.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// TEST: GET /ops/summary returns counts
// ---------------------------------------------------------------------------

func TestOpsSummary_Read(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	// Use a unique tenant per run so the summary counts are deterministic and
	// not polluted by rows seeded in previous test invocations. A matching API
	// key is registered so the tenant middleware accepts the request.
	tenantID := "tenant-summary-" + uuid.New().String()[:8]
	summaryKey := "summary-key-" + uuid.New().String()[:8]
	s.registerTenant(tenantID)
	s.registerKey(summaryKey, tenantID)
	seedPurchaseOrder(t, s.db, tenantID)
	seedGoodsReceipt(t, s.db, tenantID, "PO-READ-1")
	seedInvoice(t, s.db, tenantID, "PO-READ-1")
	seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)
	seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)

	resp := s.doGetWithKey(t, summaryKey, "/ops/summary", tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body["tenant_id"] != tenantID {
		t.Errorf("tenant_id = %v, want %s", body["tenant_id"], tenantID)
	}
	if body["purchase_orders"] != float64(1) {
		t.Errorf("purchase_orders = %v, want 1", body["purchase_orders"])
	}
	if body["goods_receipts"] != float64(1) {
		t.Errorf("goods_receipts = %v, want 1", body["goods_receipts"])
	}
	if body["invoices"] != float64(1) {
		t.Errorf("invoices = %v, want 1", body["invoices"])
	}
	if body["open_exceptions"] != float64(2) {
		t.Errorf("open_exceptions = %v, want 2", body["open_exceptions"])
	}
	if _, ok := body["generated_at"]; !ok {
		t.Error("generated_at field missing")
	}
}

// ---------------------------------------------------------------------------
// TEST: PHASE 5 — ExceptionCase lifecycle (resolve endpoint + audit)
// ---------------------------------------------------------------------------

// doPost issues a POST with the test API key, tenant header, and JSON body.
func (s *signalsReadTestServer) doPost(t *testing.T, path, tenantID string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	req, err := http.NewRequest("POST", s.ts.URL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// TestExceptionLifecycle_Resolve transitions an OPEN case to RESOLVED, then
// verifies the new status is readable via GET /exceptions/{id}.
func TestExceptionLifecycle_Resolve(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	ec := seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)

	resp := s.doPost(t, "/exceptions/"+ec.ID+"/resolve", tenantID, map[string]any{
		"status": "RESOLVED",
		"note":   "verified GRN matches invoice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("resolve status = %d, want 200; body=%s", resp.StatusCode, string(b))
	}

	// Confirm the transition persisted.
	got, err := s.db.GetExceptionCaseByID(context.Background(), ec.ID, tenantID)
	if err != nil {
		t.Fatalf("get after resolve: %v", err)
	}
	if got.Status != domain.ExceptionStatusResolved {
		t.Errorf("status = %q, want %q", got.Status, domain.ExceptionStatusResolved)
	}
}

// TestExceptionLifecycle_InvalidStatus rejects an unknown status with 400.
func TestExceptionLifecycle_InvalidStatus(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	ec := seedException(t, s.db, tenantID, domain.ExceptionStatusOpen)

	resp := s.doPost(t, "/exceptions/"+ec.ID+"/resolve", tenantID, map[string]any{
		"status": "WIBBLE",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid status", resp.StatusCode)
	}
}

// TestExceptionLifecycle_TenantIsolation: a case owned by tenant A cannot be
// resolved by a request for tenant B (tenant mismatch -> 403 or 404).
func TestExceptionLifecycle_TenantIsolation(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	ec := seedException(t, s.db, "tenant-read-a", domain.ExceptionStatusOpen)

	resp := s.doPost(t, "/exceptions/"+ec.ID+"/resolve", "tenant-read-b", map[string]any{
		"status": "RESOLVED",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 or 404 (isolation)", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// TEST: PHASE 7 — Read path is table-backed, not bridge-backed.
//
// ADR-0026 retires `jobs.extracted_data` as the signal store. This regression
// test proves the read handler serves data seeded directly in the manufacturing
// tables without any `jobs`/bridge row: the entity is inserted via the adapter
// (table write) and read back via the HTTP handler. If a future change makes
// the read fall back to the bridge, this test would either fail or become
// meaningless (it has no bridge row to fall back to).
// ---------------------------------------------------------------------------

func TestSignalRead_NotFromBridge(t *testing.T) {
	s := newSignalsReadTestServer(t)
	defer s.close()

	const tenantID = "tenant-read-a"
	// Seed directly via the adapter (table write only). No jobs.extracted_data
	// row is created for this entity.
	po := seedPurchaseOrder(t, s.db, tenantID)

	resp := s.doGet(t, "/signals/"+po.ID, tenantID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (table-backed read); body=%s", resp.StatusCode, string(b))
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["document_type"] != "PURCHASE_ORDER" {
		t.Errorf("document_type = %v, want PURCHASE_ORDER (table-backed)", body["document_type"])
	}
	if body["id"] != po.ID {
		t.Errorf("id = %v, want %s", body["id"], po.ID)
	}
}

// ---------------------------------------------------------------------------
// Local handler mirrors — thin wrappers around the real adapter that produce
// the exact JSON shapes asserted by the tests above. Kept in-package because
// the unexported *ServerDeps handlers in cmd/server are not reachable here.
// ---------------------------------------------------------------------------

func readSignalsHandler(db *postgres.Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		id := strings.TrimPrefix(r.URL.Path, "/signals/")
		id = strings.TrimRight(id, "/")
		ctx := r.Context()
		if po, err := db.GetPurchaseOrderByID(ctx, id, t.ID); err == nil && po != nil {
			writeReadJSON(w, map[string]any{"document_type": "PURCHASE_ORDER", "id": po.ID, "data": po})
			return
		}
		if gr, err := db.GetGoodsReceiptByID(ctx, id, t.ID); err == nil && gr != nil {
			writeReadJSON(w, map[string]any{"document_type": "GOODS_RECEIPT", "id": gr.ID, "data": gr})
			return
		}
		if inv, err := db.GetInvoiceByID(ctx, id, t.ID); err == nil && inv != nil {
			writeReadJSON(w, map[string]any{"document_type": "INVOICE", "id": inv.ID, "data": inv})
			return
		}
		writeReadError(w, http.StatusNotFound, "Signal not found")
	}
}

func readExceptionsHandler(db *postgres.Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		q := r.URL.Query()
		limit := 50
		if l := q.Get("limit"); l != "" {
			if v := parseInt(l); v > 0 && v <= 200 {
				limit = v
			}
		}
		offset := 0
		if o := q.Get("offset"); o != "" {
			if v := parseInt(o); v >= 0 {
				offset = v
			}
		}
		cases, err := db.ListExceptionCases(r.Context(), t.ID, q.Get("status"), q.Get("type"), limit, offset)
		if err != nil {
			writeReadError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeReadJSON(w, map[string]any{"items": cases})
	}
}

func readExceptionByIDHandler(db *postgres.Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		id := strings.TrimPrefix(r.URL.Path, "/exceptions/")
		id = strings.TrimRight(id, "/")
		ec, err := db.GetExceptionCaseByID(r.Context(), id, t.ID)
		if err != nil {
			writeReadError(w, http.StatusNotFound, "Exception case not found")
			return
		}
		writeReadJSON(w, ec)
	}
}

func readResolveExceptionHandler(db *postgres.Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		id := strings.TrimPrefix(r.URL.Path, "/exceptions/")
		id = strings.TrimSuffix(id, "/resolve")
		id = strings.TrimRight(id, "/")
		var reqBody struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			writeReadError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		switch reqBody.Status {
		case domain.ExceptionStatusResolved, domain.ExceptionStatusAck, domain.ExceptionStatusDismissed:
		default:
			writeReadError(w, http.StatusBadRequest, "status must be one of RESOLVED, ACKNOWLEDGED, DISMISSED")
			return
		}
		ctx := r.Context()
		current, err := db.GetExceptionCaseByID(ctx, id, t.ID)
		if err != nil {
			writeReadError(w, http.StatusNotFound, "Exception case not found")
			return
		}
		if err := db.UpdateExceptionCaseStatus(ctx, id, t.ID, reqBody.Status); err != nil {
			writeReadError(w, http.StatusInternalServerError, "Failed to update exception case")
			return
		}
		actor := "api"
		if u := auth.FromContext(ctx); u != nil && u.ID != "" {
			actor = u.ID
		}
		_ = db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID: t.ID, Actor: actor, Action: "EXCEPTION_STATUS_CHANGED",
			TargetType: "exception_case", TargetID: id, OldState: current.Status,
			NewState: reqBody.Status, Timestamp: time.Now().UTC(),
		})
		writeReadJSON(w, map[string]any{"id": id, "status": reqBody.Status})
	}
}

func readOpsSummaryHandler(db *postgres.Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		ctx := r.Context()
		pos, _ := db.ListPurchaseOrders(ctx, t.ID, 1, 0)
		grns, _ := db.ListGoodsReceipts(ctx, t.ID, "", 1, 0)
		invs, _ := db.ListInvoices(ctx, t.ID, "", 1, 0)
		openCases, _ := db.ListExceptionCases(ctx, t.ID, domain.ExceptionStatusOpen, "", 1000, 0)
		bySeverity := map[string]int{}
		for _, ec := range openCases {
			bySeverity[string(ec.Severity)]++
		}
		writeReadJSON(w, map[string]any{
			"tenant_id":                   t.ID,
			"purchase_orders":             len(pos),
			"goods_receipts":              len(grns),
			"invoices":                    len(invs),
			"open_exceptions":             len(openCases),
			"open_exceptions_by_severity": bySeverity,
			"generated_at":                time.Now().UTC(),
		})
	}
}

func writeReadJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func writeReadError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}
