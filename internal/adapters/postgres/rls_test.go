package postgres

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRLSFunctionExists verifies the app.current_tenant_id() function exists
// and returns 'default' when no session variable is set.
// This test requires a real DB; skip if GetTestDatabaseURL() returns "".
func TestRLSFunctionExists(t *testing.T) {
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping RLS function test: no database URL configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Verify the function exists by calling it without setting app.tenant_id.
	var tenantID string
	err = pool.QueryRow(ctx, "SELECT app.current_tenant_id()").Scan(&tenantID)
	if err != nil {
		t.Fatalf("app.current_tenant_id() function not found or failed: %v", err)
	}

	// Without any session variable, it should return 'default'.
	if tenantID != "default" {
		t.Errorf("Expected 'default' when no tenant context set, got %q", tenantID)
	}
}

// TestRLSFunctionWithContext verifies that app.current_tenant_id() returns
// the correct value when app.tenant_id is set via set_config.
func TestRLSFunctionWithContext(t *testing.T) {
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping RLS function test: no database URL configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Set a tenant context and verify it's returned. pgx v5 sends a prepared
	// statement and rejects multi-statement Exec/Query, so we issue the SET and
	// the SELECT as two separate statements inside ONE transaction. (set_config
	// with the third arg TRUE only persists for the current transaction, so the
	// two statements must share a transaction.)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', 'test-tenant-123', TRUE)"); err != nil {
		t.Fatalf("Failed to set tenant context: %v", err)
	}
	var tenantID string
	err = tx.QueryRow(ctx, "SELECT app.current_tenant_id()").Scan(&tenantID)
	if err != nil {
		t.Fatalf("Failed to read tenant context: %v", err)
	}

	if tenantID != "test-tenant-123" {
		t.Errorf("Expected 'test-tenant-123', got %q", tenantID)
	}

	// After the implicit transaction ends, the setting should be gone.
	// A new QueryRow is a new implicit transaction.
	var afterReset string
	err = pool.QueryRow(ctx, "SELECT app.current_tenant_id()").Scan(&afterReset)
	if err != nil {
		t.Fatalf("Failed to read tenant context after reset: %v", err)
	}

	if afterReset != "default" {
		t.Errorf("Expected 'default' after implicit transaction reset, got %q", afterReset)
	}
}

// TestRLSPolicyExists verifies each tenant-scoped table has RLS enabled
// and has the tenant_isolation policy.
// This test requires a real DB; skip if GetTestDatabaseURL() returns "".
func TestRLSPolicyExists(t *testing.T) {
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping RLS policy test: no database URL configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	tables := []struct {
		name       string
		allowEmpty bool // tenants table allows reading when no tenant context is set
	}{
		{name: "jobs"},
		{name: "vendors"},
		{name: "documents"},
		{name: "audit_events"},
		{name: "hitl_requests"},
		{name: "compliance_chunks"},
		{name: "usage_records"},
		{name: "tenants", allowEmpty: true},
	}

	for _, tt := range tables {
		t.Run(tt.name, func(t *testing.T) {
			// Tables marked allowEmpty (e.g. tenants) are intentionally exempt
			// from RLS — they are the bootstrap table queried before any tenant
			// context exists. Skip the RLS-enabled / policy assertions for them.
			if tt.allowEmpty {
				return
			}

			// Check RLS is enabled. relrowsecurity is a boolean column; scan it
			// into a bool (pgx rejects scanning bool into *string in binary format).
			var rlsEnabled bool
			err := pool.QueryRow(ctx, `
				SELECT relrowsecurity
				FROM pg_class
				WHERE relname = $1
			`, tt.name).Scan(&rlsEnabled)
			if err != nil {
				t.Fatalf("Failed to check RLS status for %s: %v", tt.name, err)
			}
			if !rlsEnabled {
				t.Errorf("RLS is not enabled on table %s", tt.name)
			}

			// Check the tenant_isolation policy exists.
			var policyCount int
			err = pool.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM pg_policies
				WHERE tablename = $1 AND policyname = 'tenant_isolation'
			`, tt.name).Scan(&policyCount)
			if err != nil {
				t.Fatalf("Failed to check policy for %s: %v", tt.name, err)
			}
			if policyCount != 1 {
				t.Errorf("Expected 1 tenant_isolation policy on %s, got %d", tt.name, policyCount)
			}
		})
	}
}

// TestRLSIsolation verifies that RLS actually prevents cross-tenant access
// within a transaction where tenant context is set.
//
// The application connects as a superuser, which bypasses RLS even with FORCE
// ROW LEVEL SECURITY. To genuinely exercise RLS we connect as a dedicated
// non-superuser role (rls_iso_test) that is subject to RLS and has SELECT/INSERT
// on the relevant tables. With app.tenant_id set, the tenant_isolation policy
// must filter the plain SELECT to only the current tenant's rows.
func TestRLSIsolation(t *testing.T) {
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping RLS isolation test: no database URL configured")
	}

	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer adminPool.Close()

	// Create (idempotently) a non-superuser role subject to RLS, and grant it
	// access to the tables we touch. The tenants table has no RLS, so the role
	// can insert the tenant rows; jobs has RLS and must be filtered.
	const isoRole = "rls_iso_test"
	setupStmts := []string{
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '" + isoRole + "') THEN CREATE ROLE " + isoRole + " NOSUPERUSER NOBYPASSRLS LOGIN PASSWORD '" + isoRole + "'; END IF; END $$;",
		"GRANT CONNECT ON DATABASE opscore TO " + isoRole,
		"GRANT USAGE ON SCHEMA public TO " + isoRole,
		"GRANT SELECT, INSERT ON tenants, jobs TO " + isoRole,
	}
	for _, s := range setupStmts {
		if _, err := adminPool.Exec(ctx, s); err != nil {
			t.Fatalf("Failed to set up RLS isolation role: %v", err)
		}
	}

	// Build a pool as the non-superuser RLS role.
	u, err := url.Parse(connStr)
	if err != nil {
		t.Fatalf("Failed to parse connStr: %v", err)
	}
	u.User = url.UserPassword(isoRole, isoRole)
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("Failed to connect as RLS isolation role: %v", err)
	}
	defer pool.Close()

	// Seed the data. The jobs RLS policy (tenant_id = app.current_tenant_id())
	// applies to INSERT as well as SELECT, so each job must be inserted inside
	// a transaction whose tenant context matches the row's tenant_id. We insert
	// the two tenant rows (tenants has no RLS) in one transaction, then each
	// job in its own context-matched transaction.
	seedTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin seed transaction: %v", err)
	}
	defer seedTx.Rollback(ctx)

	_, err = seedTx.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, plan, status)
		VALUES ('rls-test-tenant', 'RLS Test', 'rls-test', 'starter', 'active')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert test tenant: %v", err)
	}

	_, err = seedTx.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, plan, status)
		VALUES ('rls-other-tenant', 'RLS Other', 'rls-other', 'starter', 'active')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert other tenant: %v", err)
	}

	if err := seedTx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit seed transaction: %v", err)
	}

	// Insert the current-tenant job with the matching tenant context.
	jobTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin job transaction: %v", err)
	}
	defer jobTx.Rollback(ctx)
	if _, err = jobTx.Exec(ctx, "SELECT set_config('app.tenant_id', 'rls-test-tenant', TRUE)"); err != nil {
		t.Fatalf("Failed to set tenant context for current job: %v", err)
	}
	_, err = jobTx.Exec(ctx, `
		INSERT INTO jobs (id, tenant_id, workflow_type, status)
		VALUES ('rls-job-1', 'rls-test-tenant', 'test', 'pending')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert job for current tenant: %v", err)
	}
	if err := jobTx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit current job: %v", err)
	}

	// Insert the other-tenant job with its own matching tenant context.
	otherTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin other job transaction: %v", err)
	}
	defer otherTx.Rollback(ctx)
	if _, err = otherTx.Exec(ctx, "SELECT set_config('app.tenant_id', 'rls-other-tenant', TRUE)"); err != nil {
		t.Fatalf("Failed to set tenant context for other job: %v", err)
	}
	_, err = otherTx.Exec(ctx, `
		INSERT INTO jobs (id, tenant_id, workflow_type, status)
		VALUES ('rls-job-2', 'rls-other-tenant', 'test', 'pending')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert job for other tenant: %v", err)
	}
	if err := otherTx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit other job: %v", err)
	}

	// Now open a transaction WITH the current tenant context and verify RLS
	// filters the plain SELECT to only the current tenant's jobs.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	// Set tenant context within the transaction.
	_, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', 'rls-test-tenant', TRUE)")
	if err != nil {
		t.Fatalf("Failed to set tenant context: %v", err)
	}

	// Query jobs — RLS should filter to only the current tenant's jobs.
	var count int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count jobs: %v", err)
	}

	// We should only see the job for 'rls-test-tenant', not 'rls-other-tenant'.
	if count != 1 {
		t.Errorf("Expected 1 job visible (only current tenant), got %d", count)
	}

	// Verify the visible job belongs to our tenant.
	var visibleID, visibleTenant string
	err = tx.QueryRow(ctx, "SELECT id, tenant_id FROM jobs").Scan(&visibleID, &visibleTenant)
	if err != nil {
		t.Fatalf("Failed to query visible job: %v", err)
	}
	if visibleTenant != "rls-test-tenant" {
		t.Errorf("Visible job should belong to 'rls-test-tenant', got %q", visibleTenant)
	}
}
