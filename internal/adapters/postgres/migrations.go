package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migration represents an ordered database migration with version tracking.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations contains all database migrations in ascending version order.
// Each migration is applied exactly once and recorded in the schema_migrations table.
var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql: `
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	workflow_type TEXT,
	status TEXT,
	blob_url TEXT,
	document_type TEXT,
	confidence FLOAT,
	extracted_data JSONB,
	risk_flags TEXT[],
	hitl_reason TEXT,
	input JSONB,
	output JSONB,
	error TEXT,
	parent_batch_id TEXT,
	is_child_job BOOL DEFAULT FALSE,
	trace_id TEXT,
	correlation_id TEXT,
	created_at TIMESTAMPTZ DEFAULT NOW(),
	updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS vendors (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT,
	gst_number TEXT,
	pan_number TEXT,
	ifsc_code TEXT,
	bank_account TEXT,
	risk_tier TEXT,
	risk_score INT DEFAULT 0,
	approved BOOL DEFAULT FALSE,
	risk_flags TEXT[],
	status TEXT,
	last_transaction_at TIMESTAMPTZ,
	trust_battery JSONB,
	created_at TIMESTAMPTZ DEFAULT NOW(),
	updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS documents (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	job_id TEXT,
	file_name TEXT,
	storage_path TEXT,
	type TEXT,
	status TEXT,
	content_hash TEXT,
	extracted JSONB,
	created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS audit_events (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	actor TEXT,
	action TEXT,
	target_type TEXT,
	target_id TEXT,
	old_state TEXT,
	new_state TEXT,
	trace_id TEXT,
	correlation_id TEXT,
	timestamp TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS hitl_requests (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	job_id TEXT,
	reason TEXT,
	status TEXT,
	sent_at TIMESTAMPTZ,
	responded_at TIMESTAMPTZ,
	responder TEXT,
	decision TEXT,
	slack_ts TEXT,
	created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS compliance_chunks (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	source_url TEXT,
	source_hash TEXT,
	content TEXT,
	chunk_index INT,
	severity TEXT,
	document_type TEXT,
	page_number INT,
	created_at TIMESTAMPTZ DEFAULT NOW()
);
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS version INT DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_jobs_tenant_id ON jobs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_vendors_tenant_id ON vendors(tenant_id);
CREATE INDEX IF NOT EXISTS idx_documents_tenant_id ON documents(tenant_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_id ON audit_events(tenant_id);
CREATE INDEX IF NOT EXISTS idx_hitl_requests_tenant_id ON hitl_requests(tenant_id);
CREATE INDEX IF NOT EXISTS idx_compliance_chunks_tenant_id ON compliance_chunks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_jobs_tenant_status ON jobs(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(tenant_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_hitl_requests_pending ON hitl_requests(tenant_id, status) WHERE status = 'pending';
`,
	},
	{
		version: 2,
		name:    "add_jobs_version_column",
		sql:     `ALTER TABLE jobs ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 3,
		name:    "create_tenants_table",
		sql: `
CREATE TABLE IF NOT EXISTS tenants (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	slug TEXT NOT NULL UNIQUE,
	plan TEXT NOT NULL DEFAULT 'starter',
	status TEXT NOT NULL DEFAULT 'active',
	config JSONB DEFAULT '{}',
	created_at TIMESTAMPTZ DEFAULT NOW(),
	updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_slug ON tenants(slug);
`,
	},
	{
		version: 4,
		name:    "create_usage_records_table",
		sql: `
CREATE TABLE IF NOT EXISTS usage_records (
	tenant_id TEXT NOT NULL,
	metric TEXT NOT NULL,
	count BIGINT NOT NULL DEFAULT 0,
	period_start TIMESTAMPTZ NOT NULL,
	period_end TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ DEFAULT NOW(),
	PRIMARY KEY (tenant_id, metric, period_start)
);
CREATE INDEX IF NOT EXISTS idx_usage_tenant_period ON usage_records(tenant_id, period_start);
`,
	},
	{
		version: 5,
		name:    "enable_row_level_security",
		sql: `
-- Create the app schema for helper functions.
CREATE SCHEMA IF NOT EXISTS app;

-- Create a helper function that returns the current tenant_id from session settings.
-- Falls back to 'default' if not set, so existing clients without RLS context work.
CREATE OR REPLACE FUNCTION app.current_tenant_id() RETURNS TEXT
    LANGUAGE SQL STABLE
AS $$
    SELECT COALESCE(
        NULLIF(current_setting('app.tenant_id', TRUE), ''),
        'default'
    );
$$;

-- Jobs: RLS with USING + WITH CHECK
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE jobs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON jobs;
CREATE POLICY tenant_isolation ON jobs
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Vendors
ALTER TABLE vendors ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendors FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vendors;
CREATE POLICY tenant_isolation ON vendors
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Documents
ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON documents;
CREATE POLICY tenant_isolation ON documents
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Audit events
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_events;
CREATE POLICY tenant_isolation ON audit_events
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- HITL requests
ALTER TABLE hitl_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE hitl_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON hitl_requests;
CREATE POLICY tenant_isolation ON hitl_requests
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Compliance chunks
ALTER TABLE compliance_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_chunks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON compliance_chunks;
CREATE POLICY tenant_isolation ON compliance_chunks
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Usage records
ALTER TABLE usage_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_records FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON usage_records;
CREATE POLICY tenant_isolation ON usage_records
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- Tenants: NO RLS here. The tenants table is the bootstrap table — RLS
-- can't protect the table you query to find tenants before you know
-- the tenant context. Application-layer authorization handles this.
-- (RLS was previously enabled but the policy broke slug-based routing
-- for non-default tenants because current_tenant_id() returns 'default'
-- before any tenant context is set.)

-- Performance indexes for multi-tenant queries
CREATE INDEX IF NOT EXISTS idx_jobs_tenant_created ON jobs(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_vendors_trust_tier ON vendors(tenant_id, (trust_battery->>'tier'));
`,
	},
}

// RunMigrations applies all pending migrations in order, using a connection-scoped
// advisory lock to prevent concurrent migration runners from conflicting.
//
// It creates the schema_migrations tracking table, determines the current version,
// and applies any unapplied migrations within individual transactions.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Acquire a dedicated connection — advisory locks are connection-scoped in PostgreSQL.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection for migration: %w", err)
	}
	defer conn.Release()

	// Acquire advisory lock to serialise concurrent migration attempts.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(12345)"); err != nil {
		return fmt.Errorf("acquiring advisory lock: %w", err)
	}

	// Ensure the lock is released on return.
	defer func() {
		if _, unlockErr := conn.Exec(ctx, "SELECT pg_advisory_unlock(12345)"); unlockErr != nil {
			fmt.Printf("warning: failed to release advisory lock: %v\n", unlockErr)
		}
	}()

	// Create the schema_migrations tracking table if it does not exist.
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	// Determine the current maximum applied version.
	var currentVersion int
	if err := conn.QueryRow(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion); err != nil {
		return fmt.Errorf("reading current migration version: %w", err)
	}

	// Apply each pending migration in order within its own transaction.
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(ctx, m.sql); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("running migration %d (%s): %w", m.version, m.name, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.version, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing migration %d: %w", m.version, err)
		}
	}

	return nil
}
