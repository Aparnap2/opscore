package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("connecting to postgres: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("pinging postgres: %v", err)
	}
	log.Println("Connected to PostgreSQL")

	tables := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
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
		)`,
		`CREATE TABLE IF NOT EXISTS vendors (
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
		)`,
		`CREATE TABLE IF NOT EXISTS documents (
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
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
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
		)`,
		`CREATE TABLE IF NOT EXISTS hitl_requests (
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
		)`,
		`CREATE TABLE IF NOT EXISTS compliance_chunks (
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
		)`,
	}

	for _, ddl := range tables {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			log.Fatalf("executing migration: %v", err)
		}
		fmt.Printf("Executed: %.80s...\n", ddl)
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_jobs_tenant_id ON jobs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vendors_tenant_id ON vendors(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_tenant_id ON documents(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_id ON audit_events(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_hitl_requests_tenant_id ON hitl_requests(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_compliance_chunks_tenant_id ON compliance_chunks(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_tenant_status ON jobs(tenant_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(tenant_id, content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_hitl_requests_pending ON hitl_requests(tenant_id, status) WHERE status = 'pending'`,
	}

	for _, idx := range indexes {
		if _, err := pool.Exec(ctx, idx); err != nil {
			log.Printf("warning: creating index (may already exist): %v", err)
		} else {
			fmt.Printf("Created index: %s\n", idx)
		}
	}

	log.Println("Migration completed successfully")
}
