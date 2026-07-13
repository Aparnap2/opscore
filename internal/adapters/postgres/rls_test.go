package postgres

import (
	"context"
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

	// Set a tenant context and verify it's returned within the same implicit transaction.
	var tenantID string
	err = pool.QueryRow(ctx,
		"SELECT set_config('app.tenant_id', 'test-tenant-123', TRUE); SELECT app.current_tenant_id()",
	).Scan(&tenantID)
	if err != nil {
		t.Fatalf("Failed to set and read tenant context: %v", err)
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
			// Check RLS is enabled.
			var rlsEnabled string
			err := pool.QueryRow(ctx, `
				SELECT relrowsecurity
				FROM pg_class
				WHERE relname = $1
			`, tt.name).Scan(&rlsEnabled)
			if err != nil {
				t.Fatalf("Failed to check RLS status for %s: %v", tt.name, err)
			}
			if rlsEnabled != "t" {
				t.Errorf("RLS is not enabled on table %s (got %q)", tt.name, rlsEnabled)
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
func TestRLSIsolation(t *testing.T) {
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping RLS isolation test: no database URL configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Use a transaction to test RLS isolation within a single transaction.
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

	// Insert test data for two different tenants.
	_, err = tx.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, plan, status)
		VALUES ('rls-test-tenant', 'RLS Test', 'rls-test', 'starter', 'active')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert test tenant: %v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, plan, status)
		VALUES ('rls-other-tenant', 'RLS Other', 'rls-other', 'starter', 'active')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert other tenant: %v", err)
	}

	// Insert a job for the current tenant.
	_, err = tx.Exec(ctx, `
		INSERT INTO jobs (id, tenant_id, workflow_type, status)
		VALUES ('rls-job-1', 'rls-test-tenant', 'test', 'pending')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert job for current tenant: %v", err)
	}

	// Insert a job for a different tenant.
	_, err = tx.Exec(ctx, `
		INSERT INTO jobs (id, tenant_id, workflow_type, status)
		VALUES ('rls-job-2', 'rls-other-tenant', 'test', 'pending')
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("Failed to insert job for other tenant: %v", err)
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
