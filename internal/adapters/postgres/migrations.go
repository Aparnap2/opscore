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
