package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ErrVersionConflict is returned when an optimistic lock version check fails.
var ErrVersionConflict = errors.New("version conflict: job was modified concurrently")

// ErrNotFound is returned when a requested entity does not exist for the given
// tenant. It wraps pgx.ErrNoRows so callers can use errors.Is to detect it.
var ErrNotFound = errors.New("entity not found")

// Context key for storing a transaction in the context.
type txContextKey string

const txKey txContextKey = "pgx_tx"

// WithTx returns a context with the given transaction attached.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey, tx)
}

// getTx returns the transaction from context, or nil if not set.
func getTx(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txKey).(pgx.Tx)
	return tx
}

// queryExecer is satisfied by both pgx.Tx and pgxpool.Pool, allowing
// transparent transaction-aware query execution.
type queryExecer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// getExec returns the transaction from context if available, else the pool.
func (a *Adapter) getExec(ctx context.Context) queryExecer {
	if tx := getTx(ctx); tx != nil {
		return tx
	}
	return a.pool
}

// Adapter implements providers.DBProvider for PostgreSQL.
type Adapter struct {
	pool *pgxpool.Pool
}

// NewAdapter creates a new PostgreSQL adapter with connection pool and runs migrations.
func NewAdapter(ctx context.Context, connStr string) (*Adapter, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	adapter := &Adapter{pool: pool}

	if err := RunMigrations(ctx, pool); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return adapter, nil
}

// Ping verifies database connectivity.
func (a *Adapter) Ping(ctx context.Context) error {
	return a.pool.Ping(ctx)
}

// Close shuts down the connection pool.
func (a *Adapter) Close() {
	a.pool.Close()
}

// ---------------------------------------------------------------------------
// Job methods
// ---------------------------------------------------------------------------

// UpsertJob inserts or updates a job record with optimistic locking.
// When job.Version > 0, it uses an UPDATE with a version check to prevent
// concurrent overwrites. Returns ErrVersionConflict if the version does not match.
func (a *Adapter) UpsertJob(ctx context.Context, job *domain.Job) error {
	inputJSON, _ := json.Marshal(job.Input)
	outputJSON, _ := json.Marshal(job.Output)
	extractedJSON, _ := json.Marshal(job.Extracted)

	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now

	if job.Version > 0 {
		// Existing job: use UPDATE with optimistic locking.
		query := `UPDATE jobs SET
			tenant_id = $1, workflow_type = $2, status = $3,
			blob_url = $4, document_type = $5, confidence = $6,
			extracted_data = $7, risk_flags = $8, hitl_reason = $9,
			input = $10, output = $11, error = $12,
			parent_batch_id = $13, is_child_job = $14,
			trace_id = $15, correlation_id = $16,
			version = version + 1, updated_at = $17
		WHERE id = $18 AND tenant_id = $19 AND version = $20
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			job.TenantID, string(job.WorkflowType), string(job.Status),
			job.BlobURL, job.DocumentType, job.Confidence,
			extractedJSON, job.RiskFlags, job.HITLReason,
			inputJSON, outputJSON, job.Error,
			job.ParentBatchID, job.IsChildJob,
			job.TraceID, job.CorrelationID,
			job.UpdatedAt,
			job.ID, job.TenantID, job.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Check if the job exists at all (vs. version mismatch)
				_, getErr := a.GetJob(ctx, job.ID, job.TenantID)
				if getErr == nil {
					// Job exists but version didn't match
					return ErrVersionConflict
				}
				// Job doesn't exist — fall through to INSERT below
			} else {
				return fmt.Errorf("updating job: %w", err)
			}
		} else {
			job.Version = newVersion
			return nil
		}
	}

	// New job or job not found: INSERT with ON CONFLICT fallback.
	query := `INSERT INTO jobs (
		id, tenant_id, workflow_type, status, blob_url, document_type, confidence,
		extracted_data, risk_flags, hitl_reason, input, output, error,
		parent_batch_id, is_child_job, trace_id, correlation_id,
		created_at, updated_at, version
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11, $12, $13,
		$14, $15, $16, $17, $18,
		$19, $20
	) ON CONFLICT (id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		workflow_type = EXCLUDED.workflow_type,
		status = EXCLUDED.status,
		blob_url = EXCLUDED.blob_url,
		document_type = EXCLUDED.document_type,
		confidence = EXCLUDED.confidence,
		extracted_data = EXCLUDED.extracted_data,
		risk_flags = EXCLUDED.risk_flags,
		hitl_reason = EXCLUDED.hitl_reason,
		input = EXCLUDED.input,
		output = EXCLUDED.output,
		error = EXCLUDED.error,
		parent_batch_id = EXCLUDED.parent_batch_id,
		is_child_job = EXCLUDED.is_child_job,
		trace_id = EXCLUDED.trace_id,
		correlation_id = EXCLUDED.correlation_id,
		updated_at = EXCLUDED.updated_at,
		version = EXCLUDED.version`

	_, execErr := a.getExec(ctx).Exec(ctx, query,
		job.ID, job.TenantID, string(job.WorkflowType), string(job.Status),
		job.BlobURL, job.DocumentType, job.Confidence,
		extractedJSON, job.RiskFlags, job.HITLReason,
		inputJSON, outputJSON, job.Error,
		job.ParentBatchID, job.IsChildJob, job.TraceID, job.CorrelationID,
		job.CreatedAt, job.UpdatedAt, job.Version,
	)
	if execErr != nil {
		return fmt.Errorf("upserting job: %w", execErr)
	}
	return nil
}

// GetJob retrieves a job by id and tenant_id.
func (a *Adapter) GetJob(ctx context.Context, id, tenantID string) (*domain.Job, error) {
	query := `SELECT
		id, tenant_id, workflow_type, status, blob_url, document_type, confidence,
		extracted_data, risk_flags, hitl_reason, input, output, error,
		parent_batch_id, is_child_job, trace_id, correlation_id,
		created_at, updated_at, version
	FROM jobs WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	job := &domain.Job{}
	var workflowType, status string
	var inputJSON, outputJSON, extractedJSON []byte

	err := row.Scan(
		&job.ID, &job.TenantID, &workflowType, &status,
		&job.BlobURL, &job.DocumentType, &job.Confidence,
		&extractedJSON, &job.RiskFlags, &job.HITLReason,
		&inputJSON, &outputJSON, &job.Error,
		&job.ParentBatchID, &job.IsChildJob, &job.TraceID, &job.CorrelationID,
		&job.CreatedAt, &job.UpdatedAt, &job.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("getting job %s: %w", id, err)
	}

	job.WorkflowType = domain.WorkflowType(workflowType)
	job.Status = domain.JobStatus(status)

	if len(inputJSON) > 0 {
		_ = json.Unmarshal(inputJSON, &job.Input)
	}
	if len(outputJSON) > 0 {
		_ = json.Unmarshal(outputJSON, &job.Output)
	}
	if len(extractedJSON) > 0 {
		_ = json.Unmarshal(extractedJSON, &job.Extracted)
	}

	return job, nil
}

// ListJobs retrieves jobs for a tenant with optional filters.
func (a *Adapter) ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error) {
	query := `SELECT
		id, tenant_id, workflow_type, status, blob_url, document_type, confidence,
		extracted_data, risk_flags, hitl_reason, input, output, error,
		parent_batch_id, is_child_job, trace_id, correlation_id,
		created_at, updated_at, version
	FROM jobs WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if workflowType != "" {
		query += fmt.Sprintf(" AND workflow_type = $%d", argIdx)
		args = append(args, string(workflowType))
		argIdx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(status))
		argIdx++
	}
	query += " ORDER BY created_at DESC"

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.Job
	for rows.Next() {
		job := &domain.Job{}
		var wf, st string
		var inputJSON, outputJSON, extractedJSON []byte

		if err := rows.Scan(
			&job.ID, &job.TenantID, &wf, &st,
			&job.BlobURL, &job.DocumentType, &job.Confidence,
			&extractedJSON, &job.RiskFlags, &job.HITLReason,
			&inputJSON, &outputJSON, &job.Error,
			&job.ParentBatchID, &job.IsChildJob, &job.TraceID, &job.CorrelationID,
			&job.CreatedAt, &job.UpdatedAt, &job.Version,
		); err != nil {
			return nil, fmt.Errorf("scanning job row: %w", err)
		}

		job.WorkflowType = domain.WorkflowType(wf)
		job.Status = domain.JobStatus(st)

		if len(inputJSON) > 0 {
			_ = json.Unmarshal(inputJSON, &job.Input)
		}
		if len(outputJSON) > 0 {
			_ = json.Unmarshal(outputJSON, &job.Output)
		}
		if len(extractedJSON) > 0 {
			_ = json.Unmarshal(extractedJSON, &job.Extracted)
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// ---------------------------------------------------------------------------
// Vendor methods
// ---------------------------------------------------------------------------

// UpsertVendor inserts or updates a vendor record.
func (a *Adapter) UpsertVendor(ctx context.Context, vendor *domain.Vendor) error {
	tbJSON, _ := json.Marshal(vendor.TrustBattery)
	now := time.Now()
	if vendor.CreatedAt.IsZero() {
		vendor.CreatedAt = now
	}
	vendor.UpdatedAt = now

	query := `INSERT INTO vendors (
		id, tenant_id, name, gst_number, pan_number, ifsc_code, bank_account,
		risk_tier, risk_score, approved, risk_flags, status, last_transaction_at,
		trust_battery, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11, $12, $13,
		$14, $15, $16
	) ON CONFLICT (id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		name = EXCLUDED.name,
		gst_number = EXCLUDED.gst_number,
		pan_number = EXCLUDED.pan_number,
		ifsc_code = EXCLUDED.ifsc_code,
		bank_account = EXCLUDED.bank_account,
		risk_tier = EXCLUDED.risk_tier,
		risk_score = EXCLUDED.risk_score,
		approved = EXCLUDED.approved,
		risk_flags = EXCLUDED.risk_flags,
		status = EXCLUDED.status,
		last_transaction_at = EXCLUDED.last_transaction_at,
		trust_battery = EXCLUDED.trust_battery,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		vendor.ID, vendor.TenantID, vendor.Name,
		vendor.GSTNumber, vendor.PANNumber, vendor.IFSCCode, vendor.BankAccount,
		string(vendor.RiskTier), vendor.RiskScore, vendor.Approved,
		vendor.RiskFlags, vendor.Status, vendor.LastTransactionAt,
		tbJSON, vendor.CreatedAt, vendor.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting vendor: %w", err)
	}
	return nil
}

// GetVendor retrieves a vendor by id and tenant_id.
func (a *Adapter) GetVendor(ctx context.Context, id, tenantID string) (*domain.Vendor, error) {
	query := `SELECT
		id, tenant_id, name, gst_number, pan_number, ifsc_code, bank_account,
		risk_tier, risk_score, approved, risk_flags, status, last_transaction_at,
		trust_battery, created_at, updated_at
	FROM vendors WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	v := &domain.Vendor{}
	var riskTier string
	var tbJSON []byte

	err := row.Scan(
		&v.ID, &v.TenantID, &v.Name,
		&v.GSTNumber, &v.PANNumber, &v.IFSCCode, &v.BankAccount,
		&riskTier, &v.RiskScore, &v.Approved,
		&v.RiskFlags, &v.Status, &v.LastTransactionAt,
		&tbJSON, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting vendor %s: %w", id, err)
	}

	v.RiskTier = domain.RiskTier(riskTier)
	if len(tbJSON) > 0 {
		_ = json.Unmarshal(tbJSON, &v.TrustBattery)
	}

	return v, nil
}

// ListVendors retrieves all vendors for a tenant.
func (a *Adapter) ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error) {
	query := `SELECT
		id, tenant_id, name, gst_number, pan_number, ifsc_code, bank_account,
		risk_tier, risk_score, approved, risk_flags, status, last_transaction_at,
		trust_battery, created_at, updated_at
	FROM vendors WHERE tenant_id = $1 ORDER BY created_at DESC`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing vendors: %w", err)
	}
	defer rows.Close()

	var vendors []*domain.Vendor
	for rows.Next() {
		v := &domain.Vendor{}
		var riskTier string
		var tbJSON []byte

		if err := rows.Scan(
			&v.ID, &v.TenantID, &v.Name,
			&v.GSTNumber, &v.PANNumber, &v.IFSCCode, &v.BankAccount,
			&riskTier, &v.RiskScore, &v.Approved,
			&v.RiskFlags, &v.Status, &v.LastTransactionAt,
			&tbJSON, &v.CreatedAt, &v.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning vendor row: %w", err)
		}

		v.RiskTier = domain.RiskTier(riskTier)
		if len(tbJSON) > 0 {
			_ = json.Unmarshal(tbJSON, &v.TrustBattery)
		}

		vendors = append(vendors, v)
	}

	return vendors, nil
}

// ---------------------------------------------------------------------------
// Document methods
// ---------------------------------------------------------------------------

// UpsertDocument inserts or updates a document record.
func (a *Adapter) UpsertDocument(ctx context.Context, doc *domain.Document) error {
	extractedJSON, _ := json.Marshal(doc.Extracted)
	now := time.Now()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}

	query := `INSERT INTO documents (
		id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10
	) ON CONFLICT (id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		job_id = EXCLUDED.job_id,
		file_name = EXCLUDED.file_name,
		storage_path = EXCLUDED.storage_path,
		type = EXCLUDED.type,
		status = EXCLUDED.status,
		content_hash = EXCLUDED.content_hash,
		extracted = EXCLUDED.extracted`

	_, err := a.getExec(ctx).Exec(ctx, query,
		doc.ID, doc.TenantID, doc.JobID, doc.FileName, doc.StoragePath,
		doc.Type, doc.Status, doc.ContentHash, extractedJSON, doc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by id and tenant_id.
func (a *Adapter) GetDocument(ctx context.Context, id, tenantID string) (*domain.Document, error) {
	query := `SELECT id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	FROM documents WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	doc := &domain.Document{}
	var extractedJSON []byte

	err := row.Scan(
		&doc.ID, &doc.TenantID, &doc.JobID, &doc.FileName, &doc.StoragePath,
		&doc.Type, &doc.Status, &doc.ContentHash, &extractedJSON, &doc.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting document %s: %w", id, err)
	}

	if len(extractedJSON) > 0 {
		_ = json.Unmarshal(extractedJSON, &doc.Extracted)
	}

	return doc, nil
}

// FindBySHA256 finds a document by tenant_id and content_hash for duplicate detection.
// Returns (nil, nil) when no matching document is found.
func (a *Adapter) FindBySHA256(ctx context.Context, tenantID, contentHash string) (*domain.Document, error) {
	query := `SELECT id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	FROM documents WHERE tenant_id = $1 AND content_hash = $2
	LIMIT 1`

	row := a.getExec(ctx).QueryRow(ctx, query, tenantID, contentHash)

	doc := &domain.Document{}
	var extractedJSON []byte

	err := row.Scan(
		&doc.ID, &doc.TenantID, &doc.JobID, &doc.FileName, &doc.StoragePath,
		&doc.Type, &doc.Status, &doc.ContentHash, &extractedJSON, &doc.CreatedAt,
	)
	if err != nil {
		return nil, nil
	}

	if len(extractedJSON) > 0 {
		_ = json.Unmarshal(extractedJSON, &doc.Extracted)
	}

	return doc, nil
}

// ---------------------------------------------------------------------------
// HITL methods
// ---------------------------------------------------------------------------

// UpsertHITLRequest inserts or updates a HITL request.
func (a *Adapter) UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error {
	now := time.Now()
	if req.SentAt.IsZero() {
		req.SentAt = now
	}

	query := `INSERT INTO hitl_requests (
		id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, NOW()
	) ON CONFLICT (id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		job_id = EXCLUDED.job_id,
		reason = EXCLUDED.reason,
		status = EXCLUDED.status,
		sent_at = EXCLUDED.sent_at,
		responded_at = EXCLUDED.responded_at,
		responder = EXCLUDED.responder,
		decision = EXCLUDED.decision,
		slack_ts = EXCLUDED.slack_ts`

	_, err := a.getExec(ctx).Exec(ctx, query,
		req.ID, req.TenantID, req.JobID, req.Reason, req.Status,
		req.SentAt, req.RespondedAt, req.Responder, req.Decision, req.SlackTS,
	)
	if err != nil {
		return fmt.Errorf("upserting HITL request: %w", err)
	}
	return nil
}

// GetHITLRequest retrieves a HITL request by id and tenant_id.
func (a *Adapter) GetHITLRequest(ctx context.Context, id, tenantID string) (*domain.HITLRequest, error) {
	query := `SELECT id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts
	FROM hitl_requests WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	req := &domain.HITLRequest{}
	err := row.Scan(
		&req.ID, &req.TenantID, &req.JobID, &req.Reason, &req.Status,
		&req.SentAt, &req.RespondedAt, &req.Responder, &req.Decision, &req.SlackTS,
	)
	if err != nil {
		return nil, fmt.Errorf("getting HITL request %s: %w", id, err)
	}
	return req, nil
}

// ListHITLRequests retrieves HITL requests for a tenant with optional status filtering and pagination.
func (a *Adapter) ListHITLRequests(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts
	FROM hitl_requests WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}

	query += " ORDER BY sent_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing HITL requests: %w", err)
	}
	defer rows.Close()

	var requests []*domain.HITLRequest
	for rows.Next() {
		req := &domain.HITLRequest{}
		if err := rows.Scan(
			&req.ID, &req.TenantID, &req.JobID, &req.Reason, &req.Status,
			&req.SentAt, &req.RespondedAt, &req.Responder, &req.Decision, &req.SlackTS,
		); err != nil {
			return nil, fmt.Errorf("scanning HITL row: %w", err)
		}
		requests = append(requests, req)
	}

	return requests, nil
}

// ListPendingHITL retrieves all pending HITL requests for a tenant.
func (a *Adapter) ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	query := `SELECT id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts
	FROM hitl_requests WHERE tenant_id = $1 AND status = 'pending'
	ORDER BY sent_at DESC`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing pending HITL: %w", err)
	}
	defer rows.Close()

	var requests []*domain.HITLRequest
	for rows.Next() {
		req := &domain.HITLRequest{}
		if err := rows.Scan(
			&req.ID, &req.TenantID, &req.JobID, &req.Reason, &req.Status,
			&req.SentAt, &req.RespondedAt, &req.Responder, &req.Decision, &req.SlackTS,
		); err != nil {
			return nil, fmt.Errorf("scanning HITL row: %w", err)
		}
		requests = append(requests, req)
	}

	return requests, nil
}

// ---------------------------------------------------------------------------
// Audit methods
// ---------------------------------------------------------------------------

// AppendAuditEvent inserts a new audit event.
func (a *Adapter) AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.ID == "" {
		// Use gen_random_uuid() if available, else rely on application-provided ID.
		query := `INSERT INTO audit_events (
			id, tenant_id, actor, action, target_type, target_id,
			old_state, new_state, trace_id, correlation_id, timestamp
		) VALUES (
			gen_random_uuid()::TEXT, $1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10
		)`
		_, err := a.getExec(ctx).Exec(ctx, query,
			event.TenantID, event.Actor, event.Action, event.TargetType, event.TargetID,
			event.OldState, event.NewState, event.TraceID, event.CorrelationID,
			event.Timestamp,
		)
		if err != nil {
			return fmt.Errorf("appending audit event: %w", err)
		}
		return nil
	}

	query := `INSERT INTO audit_events (
		id, tenant_id, actor, action, target_type, target_id,
		old_state, new_state, trace_id, correlation_id, timestamp
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10, $11
	)`
	_, err := a.getExec(ctx).Exec(ctx, query,
		event.ID, event.TenantID, event.Actor, event.Action, event.TargetType, event.TargetID,
		event.OldState, event.NewState, event.TraceID, event.CorrelationID,
		event.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("appending audit event: %w", err)
	}
	return nil
}

// ListAuditEvents retrieves audit events for a target with optional filtering.
func (a *Adapter) ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	query := `SELECT id, tenant_id, actor, action, target_type, target_id,
		old_state, new_state, trace_id, correlation_id, timestamp
	FROM audit_events WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if targetType != "" {
		query += fmt.Sprintf(" AND target_type = $%d", argIdx)
		args = append(args, targetType)
		argIdx++
	}
	if targetID != "" {
		query += fmt.Sprintf(" AND target_id = $%d", argIdx)
		args = append(args, targetID)
		argIdx++
	}

	query += " ORDER BY timestamp DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
		argIdx++
	}

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing audit events: %w", err)
	}
	defer rows.Close()

	var events []*domain.AuditEvent
	for rows.Next() {
		ev := &domain.AuditEvent{}
		if err := rows.Scan(
			&ev.ID, &ev.TenantID, &ev.Actor, &ev.Action,
			&ev.TargetType, &ev.TargetID, &ev.OldState, &ev.NewState,
			&ev.TraceID, &ev.CorrelationID, &ev.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scanning audit event row: %w", err)
		}
		events = append(events, ev)
	}

	return events, nil
}

// ---------------------------------------------------------------------------
// Ops endpoints
// ---------------------------------------------------------------------------

// GetRecentJobs returns the latest N jobs for a tenant.
func (a *Adapter) GetRecentJobs(ctx context.Context, tenantID string, limit int) ([]*domain.Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query := `SELECT id, tenant_id, workflow_type, status, created_at, updated_at,
		blob_url, document_type, confidence, extracted_data, risk_flags, hitl_reason,
		input, output, error, parent_batch_id, is_child_job,
		trace_id, correlation_id, version
	FROM jobs WHERE tenant_id = $1
	ORDER BY created_at DESC
	LIMIT $2`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("getting recent jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.Job
	for rows.Next() {
		job := &domain.Job{}
		var inputJSON, outputJSON, extractedJSON []byte
		if err := rows.Scan(
			&job.ID, &job.TenantID, &job.WorkflowType, &job.Status,
			&job.CreatedAt, &job.UpdatedAt,
			&job.BlobURL, &job.DocumentType, &job.Confidence,
			&extractedJSON, &job.RiskFlags, &job.HITLReason,
			&inputJSON, &outputJSON, &job.Error,
			&job.ParentBatchID, &job.IsChildJob,
			&job.TraceID, &job.CorrelationID, &job.Version,
		); err != nil {
			return nil, fmt.Errorf("scanning job row: %w", err)
		}
		if len(inputJSON) > 0 {
			job.Input = inputJSON
		}
		if len(outputJSON) > 0 {
			job.Output = outputJSON
		}
		if len(extractedJSON) > 0 {
			job.Extracted = extractedJSON
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetRiskyVendors returns vendors with low trust score or risk.
func (a *Adapter) GetRiskyVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error) {
	query := `SELECT id, tenant_id, name, gst_number, pan_number, ifsc_code,
		bank_account, risk_score, risk_tier, approved, trust_battery->>'tier' AS trust_tier,
		created_at, updated_at, last_transaction_at
	FROM vendors WHERE tenant_id = $1
	AND (trust_battery->>'tier' IN ('PROBATION', 'NONE', 'BLOCKED') OR risk_score < 30)
	ORDER BY trust_battery->>'tier', risk_score`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("getting risky vendors: %w", err)
	}
	defer rows.Close()

	var vendors []*domain.Vendor
	for rows.Next() {
		v := &domain.Vendor{}
		var trustTier string
		var lastTxn *time.Time
		if err := rows.Scan(
			&v.ID, &v.TenantID, &v.Name, &v.GSTNumber, &v.PANNumber,
			&v.IFSCCode, &v.BankAccount, &v.RiskScore, &v.RiskTier,
			&v.Approved, &trustTier,
			&v.CreatedAt, &v.UpdatedAt, &lastTxn,
		); err != nil {
			return nil, fmt.Errorf("scanning vendor row: %w", err)
		}
		v.TrustBattery.Tier = domain.TrustTier(trustTier)
		v.LastTransactionAt = lastTxn
		vendors = append(vendors, v)
	}
	return vendors, nil
}

// GetRecentCompliance returns recent compliance gap records.
func (a *Adapter) GetRecentCompliance(ctx context.Context, tenantID string, limit int) ([]*domain.ComplianceRecord, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query := `SELECT id, tenant_id, source_url, severity, created_at
	FROM compliance_chunks WHERE tenant_id = $1
	ORDER BY created_at DESC
	LIMIT $2`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("getting recent compliance: %w", err)
	}
	defer rows.Close()

	var records []*domain.ComplianceRecord
	for rows.Next() {
		r := &domain.ComplianceRecord{}
		if err := rows.Scan(&r.ID, &r.TenantID, &r.SourceURL, &r.Severity, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning compliance row: %w", err)
		}
		// Default gap/score - compliance_chunks may not have these columns
		records = append(records, r)
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// Tenant methods
// ---------------------------------------------------------------------------

// GetTenant retrieves a tenant by id.
func (a *Adapter) GetTenant(ctx context.Context, id string) (*domain.Tenant, error) {
	query := `SELECT id, name, slug, plan, status, config, created_at, updated_at
	FROM tenants WHERE id = $1`

	row := a.getExec(ctx).QueryRow(ctx, query, id)

	t := &domain.Tenant{}
	var configJSON []byte

	err := row.Scan(
		&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status,
		&configJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting tenant %s: %w", id, err)
	}

	if len(configJSON) > 0 {
		_ = json.Unmarshal(configJSON, &t.Config)
	}
	if t.Config == nil {
		t.Config = make(map[string]any)
	}

	return t, nil
}

// GetTenantBySlug retrieves a tenant by slug.
func (a *Adapter) GetTenantBySlug(ctx context.Context, slug string) (*domain.Tenant, error) {
	query := `SELECT id, name, slug, plan, status, config, created_at, updated_at
	FROM tenants WHERE slug = $1`

	row := a.getExec(ctx).QueryRow(ctx, query, slug)

	t := &domain.Tenant{}
	var configJSON []byte

	err := row.Scan(
		&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status,
		&configJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting tenant by slug %s: %w", slug, err)
	}

	if len(configJSON) > 0 {
		_ = json.Unmarshal(configJSON, &t.Config)
	}
	if t.Config == nil {
		t.Config = make(map[string]any)
	}

	return t, nil
}

// CreateTenant inserts a new tenant record.
func (a *Adapter) CreateTenant(ctx context.Context, tenant *domain.Tenant) error {
	configJSON, _ := json.Marshal(tenant.Config)
	now := time.Now()
	if tenant.CreatedAt.IsZero() {
		tenant.CreatedAt = now
	}
	if tenant.UpdatedAt.IsZero() {
		tenant.UpdatedAt = now
	}

	query := `INSERT INTO tenants (
		id, name, slug, plan, status, config, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8
	) ON CONFLICT (id) DO UPDATE SET
		name = EXCLUDED.name,
		slug = EXCLUDED.slug,
		plan = EXCLUDED.plan,
		status = EXCLUDED.status,
		config = EXCLUDED.config,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		tenant.ID, tenant.Name, tenant.Slug,
		tenant.Plan, tenant.Status, configJSON,
		tenant.CreatedAt, tenant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating tenant: %w", err)
	}
	return nil
}

// ListTenants retrieves all tenants.
func (a *Adapter) ListTenants(ctx context.Context) ([]*domain.Tenant, error) {
	query := `SELECT id, name, slug, plan, status, config, created_at, updated_at
	FROM tenants ORDER BY created_at DESC`

	rows, err := a.getExec(ctx).Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*domain.Tenant
	for rows.Next() {
		t := &domain.Tenant{}
		var configJSON []byte

		if err := rows.Scan(
			&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status,
			&configJSON, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning tenant row: %w", err)
		}

		if len(configJSON) > 0 {
			_ = json.Unmarshal(configJSON, &t.Config)
		}
		if t.Config == nil {
			t.Config = make(map[string]any)
		}

		tenants = append(tenants, t)
	}

	return tenants, nil
}

// UpdateTenantStatus updates the status of a tenant.
func (a *Adapter) UpdateTenantStatus(ctx context.Context, id, status string) error {
	query := `UPDATE tenants SET status = $1, updated_at = NOW() WHERE id = $2`

	result, err := a.getExec(ctx).Exec(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating tenant status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant %s not found", id)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Usage metering methods
// ---------------------------------------------------------------------------

// currentPeriodBounds returns the start and end of the current calendar month (UTC).
func currentPeriodBounds() (time.Time, time.Time) {
	now := time.Now().UTC()
	year, month, _ := now.Date()
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return start, end
}

// IncrementUsage upserts a usage record for the current billing period,
// incrementing the count by the given delta.
func (a *Adapter) IncrementUsage(ctx context.Context, tenantID string, metric domain.Metric, delta int64) error {
	start, end := currentPeriodBounds()

	query := `INSERT INTO usage_records (tenant_id, metric, count, period_start, period_end, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (tenant_id, metric, period_start) DO UPDATE SET
			count = usage_records.count + EXCLUDED.count,
			updated_at = NOW()`

	_, err := a.getExec(ctx).Exec(ctx, query, tenantID, string(metric), delta, start, end)
	if err != nil {
		return fmt.Errorf("incrementing usage for %s/%s: %w", tenantID, metric, err)
	}
	return nil
}

// GetUsage returns the current count for a given tenant and metric in the current billing period.
func (a *Adapter) GetUsage(ctx context.Context, tenantID string, metric domain.Metric) (int64, error) {
	start, _ := currentPeriodBounds()

	query := `SELECT count FROM usage_records
		WHERE tenant_id = $1 AND metric = $2 AND period_start = $3`

	var count int64
	err := a.getExec(ctx).QueryRow(ctx, query, tenantID, string(metric), start).Scan(&count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("getting usage for %s/%s: %w", tenantID, metric, err)
	}
	return count, nil
}

// GetCurrentPeriodUsage returns all usage metrics for the current billing period for a tenant.
func (a *Adapter) GetCurrentPeriodUsage(ctx context.Context, tenantID string) (map[domain.Metric]int64, error) {
	start, _ := currentPeriodBounds()

	query := `SELECT metric, count FROM usage_records
		WHERE tenant_id = $1 AND period_start = $2`

	rows, err := a.getExec(ctx).Query(ctx, query, tenantID, start)
	if err != nil {
		return nil, fmt.Errorf("getting current period usage for %s: %w", tenantID, err)
	}
	defer rows.Close()

	usage := make(map[domain.Metric]int64)
	for rows.Next() {
		var metricStr string
		var count int64
		if err := rows.Scan(&metricStr, &count); err != nil {
			return nil, fmt.Errorf("scanning usage row: %w", err)
		}
		usage[domain.Metric(metricStr)] = count
	}
	return usage, nil
}

// CheckLimit checks whether the tenant is within the limit for the given metric.
// Returns (within_limit, current_usage, limit, error).
func (a *Adapter) CheckLimit(ctx context.Context, tenantID string, metric domain.Metric) (bool, int64, int64, error) {
	// Get the tenant's plan.
	tenant, err := a.GetTenant(ctx, tenantID)
	if err != nil {
		return false, 0, 0, fmt.Errorf("getting tenant for limit check: %w", err)
	}

	limit := domain.GetLimit(tenant.Plan, metric)
	if domain.IsUnlimited(limit) {
		return true, 0, limit, nil
	}

	current, err := a.GetUsage(ctx, tenantID, metric)
	if err != nil {
		return false, 0, limit, fmt.Errorf("getting usage for limit check: %w", err)
	}

	return current < limit, current, limit, nil
}

// ---------------------------------------------------------------------------
// Transaction support for RLS
// ---------------------------------------------------------------------------

// Begin starts a new transaction that can be used via WithTx in context.
func (a *Adapter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return tx, nil
}

// SetTenantContextTx sets app.tenant_id inside an explicit transaction.
// This is the correct way to enable RLS — SET LOCAL only works inside a transaction.
func SetTenantContextTx(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, TRUE)", tenantID)
	return err
}

// ---------------------------------------------------------------------------
// RLS (Row-Level Security) support
// ---------------------------------------------------------------------------

// SetTenantContext sets the app.tenant_id session variable for the current
// connection. This enables PostgreSQL Row-Level Security (RLS) policies to
// filter queries to the current tenant.
//
// NOTE: Setting the session variable outside an explicit transaction is
// effectively a no-op in PostgreSQL — it only lasts for the duration of
// the implicit transaction of the single Exec call. For RLS to actually
// work, use SetTenantContextTx inside an explicit transaction, or wrap
// requests with the txMiddleware in main.go.
func (a *Adapter) SetTenantContext(ctx context.Context, tenantID string) error {
	_, err := a.getExec(ctx).Exec(ctx, "SELECT set_config('app.tenant_id', $1, TRUE)", tenantID)
	return err
}

// ---------------------------------------------------------------------------
// Manufacturing pivot (Phase 1.4A) — Purchase Order / Goods Receipt / Invoice
// ---------------------------------------------------------------------------
//
// All entity tables use a composite primary key (tenant_id, id) and are
// protected by RLS via app.current_tenant_id(). The adapter relies on the
// caller having set the tenant context (SetTenantContextTx inside an explicit
// transaction, or the request txMiddleware) so RLS is satisfied. Queries
// therefore always filter by both tenant_id and id.
//
// Optimistic locking: mutable entities carry a version column. On UPDATE we
// include `WHERE id=$1 AND tenant_id=$2 AND version=$3` and return
// ErrVersionConflict when rows-affected == 0 and a version was supplied. On
// insert we set version = 0.
//
// Line items (purchase_order_lines, goods_receipt_lines, invoice_lines) are
// upserted by deleting all existing children for the parent within the same
// tenant and re-inserting from the domain slice. This is simpler and fully
// deterministic for the small cardinality of line items on these documents,
// and keeps the adapter free of per-line diffing business logic.

// UpsertPurchaseOrder inserts or updates a purchase order and its line items
// with optimistic locking.
func (a *Adapter) UpsertPurchaseOrder(ctx context.Context, po *domain.PurchaseOrder) error {
	now := time.Now()
	if po.CreatedAt.IsZero() {
		po.CreatedAt = now
	}
	po.UpdatedAt = now

	if po.Version >= 0 {
		query := `UPDATE purchase_orders SET
			document_no = $1, vendor_gstin = $2, order_date = $3,
			version = version + 1, updated_at = $4
		WHERE id = $5 AND tenant_id = $6 AND version = $7
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			po.DocumentNo, po.VendorGSTIN, po.OrderDate,
			po.UpdatedAt,
			po.ID, po.TenantID, po.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetPurchaseOrderByID(ctx, po.ID, po.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
				// Not found — fall through to INSERT.
			} else {
				return fmt.Errorf("updating purchase order: %w", err)
			}
		} else {
			po.Version = newVersion
			return a.upsertPOLines(ctx, po)
		}
	}

	query := `INSERT INTO purchase_orders (
		id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		order_date = EXCLUDED.order_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		po.ID, po.TenantID, po.DocumentNo, po.VendorGSTIN, po.OrderDate,
		po.Version, po.CreatedAt, po.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting purchase order: %w", err)
	}
	return a.upsertPOLines(ctx, po)
}

// upsertPOLines deletes existing line items for the PO within the tenant and
// re-inserts the current domain slice.
func (a *Adapter) upsertPOLines(ctx context.Context, po *domain.PurchaseOrder) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM purchase_order_lines WHERE tenant_id = $1 AND po_id = $2`,
		po.TenantID, po.ID,
	); err != nil {
		return fmt.Errorf("deleting purchase order lines: %w", err)
	}

	for _, l := range po.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO purchase_order_lines (
				id, tenant_id, po_id, line_ref, item_code, item_desc,
				uom, quantity, unit_rate, tax_rate_pct, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW()
			)`,
			po.TenantID, po.ID, l.LineRef, l.ItemCode, l.ItemDesc,
			l.UoM, l.Quantity, l.UnitRate, l.TaxRatePct,
		); err != nil {
			return fmt.Errorf("inserting purchase order line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetPurchaseOrderByID retrieves a purchase order and its line items by id and tenant_id.
func (a *Adapter) GetPurchaseOrderByID(ctx context.Context, id, tenantID string) (*domain.PurchaseOrder, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	FROM purchase_orders WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	po := &domain.PurchaseOrder{}
	err := row.Scan(
		&po.ID, &po.TenantID, &po.DocumentNo, &po.VendorGSTIN, &po.OrderDate,
		&po.Version, &po.CreatedAt, &po.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting purchase order %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting purchase order %s: %w", id, err)
	}

	lines, err := a.getPOLines(ctx, po.ID, po.TenantID)
	if err != nil {
		return nil, err
	}
	po.Lines = lines

	return po, nil
}

// getPOLines retrieves the line items for a purchase order within the tenant.
func (a *Adapter) getPOLines(ctx context.Context, poID, tenantID string) ([]domain.PurchaseOrderLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, item_code, item_desc, uom, quantity, unit_rate, tax_rate_pct
		FROM purchase_order_lines WHERE tenant_id = $1 AND po_id = $2 ORDER BY line_ref`,
		tenantID, poID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying purchase order lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.PurchaseOrderLine
	for rows.Next() {
		var l domain.PurchaseOrderLine
		if err := rows.Scan(
			&l.LineRef, &l.ItemCode, &l.ItemDesc, &l.UoM,
			&l.Quantity, &l.UnitRate, &l.TaxRatePct,
		); err != nil {
			return nil, fmt.Errorf("scanning purchase order line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertGoodsReceipt inserts or updates a goods receipt and its line items
// with optimistic locking.
func (a *Adapter) UpsertGoodsReceipt(ctx context.Context, gr *domain.GoodsReceipt) error {
	now := time.Now()
	if gr.CreatedAt.IsZero() {
		gr.CreatedAt = now
	}
	gr.UpdatedAt = now

	if gr.Version >= 0 {
		query := `UPDATE goods_receipts SET
			document_no = $1, vendor_gstin = $2, po_number = $3, receipt_date = $4,
			version = version + 1, updated_at = $5
		WHERE id = $6 AND tenant_id = $7 AND version = $8
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			gr.DocumentNo, gr.VendorGSTIN, gr.PONumber, gr.ReceiptDate,
			gr.UpdatedAt,
			gr.ID, gr.TenantID, gr.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetGoodsReceiptByID(ctx, gr.ID, gr.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
			} else {
				return fmt.Errorf("updating goods receipt: %w", err)
			}
		} else {
			gr.Version = newVersion
			return a.upsertGRLines(ctx, gr)
		}
	}

	query := `INSERT INTO goods_receipts (
		id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_number = EXCLUDED.po_number,
		receipt_date = EXCLUDED.receipt_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		gr.ID, gr.TenantID, gr.DocumentNo, gr.VendorGSTIN, gr.PONumber, gr.ReceiptDate,
		gr.Version, gr.CreatedAt, gr.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting goods receipt: %w", err)
	}
	return a.upsertGRLines(ctx, gr)
}

// upsertGRLines deletes existing line items for the GRN within the tenant and
// re-inserts the current domain slice.
func (a *Adapter) upsertGRLines(ctx context.Context, gr *domain.GoodsReceipt) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM goods_receipt_lines WHERE tenant_id = $1 AND grn_id = $2`,
		gr.TenantID, gr.ID,
	); err != nil {
		return fmt.Errorf("deleting goods receipt lines: %w", err)
	}

	for _, l := range gr.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO goods_receipt_lines (
				id, tenant_id, grn_id, line_ref, po_line_ref, item_code,
				received_qty, uom, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, NOW()
			)`,
			gr.TenantID, gr.ID, l.LineRef, l.POLineRef, l.ItemCode,
			l.ReceivedQty, l.UoM,
		); err != nil {
			return fmt.Errorf("inserting goods receipt line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetGoodsReceiptByID retrieves a goods receipt and its line items by id and tenant_id.
func (a *Adapter) GetGoodsReceiptByID(ctx context.Context, id, tenantID string) (*domain.GoodsReceipt, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	FROM goods_receipts WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	gr := &domain.GoodsReceipt{}
	err := row.Scan(
		&gr.ID, &gr.TenantID, &gr.DocumentNo, &gr.VendorGSTIN, &gr.PONumber, &gr.ReceiptDate,
		&gr.Version, &gr.CreatedAt, &gr.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting goods receipt %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting goods receipt %s: %w", id, err)
	}

	lines, err := a.getGRLines(ctx, gr.ID, gr.TenantID)
	if err != nil {
		return nil, err
	}
	gr.Lines = lines

	return gr, nil
}

// getGRLines retrieves the line items for a goods receipt within the tenant.
func (a *Adapter) getGRLines(ctx context.Context, grnID, tenantID string) ([]domain.GoodsReceiptLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, po_line_ref, item_code, received_qty, uom
		FROM goods_receipt_lines WHERE tenant_id = $1 AND grn_id = $2 ORDER BY line_ref`,
		tenantID, grnID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying goods receipt lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.GoodsReceiptLine
	for rows.Next() {
		var l domain.GoodsReceiptLine
		if err := rows.Scan(
			&l.LineRef, &l.POLineRef, &l.ItemCode, &l.ReceivedQty, &l.UoM,
		); err != nil {
			return nil, fmt.Errorf("scanning goods receipt line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertInvoice inserts or updates an invoice and its line items with optimistic locking.
func (a *Adapter) UpsertInvoice(ctx context.Context, inv *domain.Invoice) error {
	now := time.Now()
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = now
	}
	inv.UpdatedAt = now

	if inv.Version >= 0 {
		query := `UPDATE invoices SET
			document_no = $1, vendor_gstin = $2, po_number = $3, invoice_date = $4,
			version = version + 1, updated_at = $5
		WHERE id = $6 AND tenant_id = $7 AND version = $8
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			inv.DocumentNo, inv.VendorGSTIN, inv.PONumber, inv.InvoiceDate,
			inv.UpdatedAt,
			inv.ID, inv.TenantID, inv.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetInvoiceByID(ctx, inv.ID, inv.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
			} else {
				return fmt.Errorf("updating invoice: %w", err)
			}
		} else {
			inv.Version = newVersion
			return a.upsertInvoiceLines(ctx, inv)
		}
	}

	query := `INSERT INTO invoices (
		id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_number = EXCLUDED.po_number,
		invoice_date = EXCLUDED.invoice_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		inv.ID, inv.TenantID, inv.DocumentNo, inv.VendorGSTIN, inv.PONumber, inv.InvoiceDate,
		inv.Version, inv.CreatedAt, inv.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting invoice: %w", err)
	}
	return a.upsertInvoiceLines(ctx, inv)
}

// upsertInvoiceLines deletes existing line items for the invoice within the
// tenant and re-inserts the current domain slice.
func (a *Adapter) upsertInvoiceLines(ctx context.Context, inv *domain.Invoice) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM invoice_lines WHERE tenant_id = $1 AND invoice_id = $2`,
		inv.TenantID, inv.ID,
	); err != nil {
		return fmt.Errorf("deleting invoice lines: %w", err)
	}

	for _, l := range inv.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO invoice_lines (
				id, tenant_id, invoice_id, line_ref, po_line_ref, item_code,
				quantity, unit_rate, tax_rate_pct, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, $8, NOW()
			)`,
			inv.TenantID, inv.ID, l.LineRef, l.POLineRef, l.ItemCode,
			l.Quantity, l.UnitRate, l.TaxRatePct,
		); err != nil {
			return fmt.Errorf("inserting invoice line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetInvoiceByID retrieves an invoice and its line items by id and tenant_id.
func (a *Adapter) GetInvoiceByID(ctx context.Context, id, tenantID string) (*domain.Invoice, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	FROM invoices WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	inv := &domain.Invoice{}
	err := row.Scan(
		&inv.ID, &inv.TenantID, &inv.DocumentNo, &inv.VendorGSTIN, &inv.PONumber, &inv.InvoiceDate,
		&inv.Version, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting invoice %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting invoice %s: %w", id, err)
	}

	lines, err := a.getInvoiceLines(ctx, inv.ID, inv.TenantID)
	if err != nil {
		return nil, err
	}
	inv.Lines = lines

	return inv, nil
}

// getInvoiceLines retrieves the line items for an invoice within the tenant.
func (a *Adapter) getInvoiceLines(ctx context.Context, invoiceID, tenantID string) ([]domain.InvoiceLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, po_line_ref, item_code, quantity, unit_rate, tax_rate_pct
		FROM invoice_lines WHERE tenant_id = $1 AND invoice_id = $2 ORDER BY line_ref`,
		tenantID, invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoice lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.InvoiceLine
	for rows.Next() {
		var l domain.InvoiceLine
		if err := rows.Scan(
			&l.LineRef, &l.POLineRef, &l.ItemCode, &l.Quantity, &l.UnitRate, &l.TaxRatePct,
		); err != nil {
			return nil, fmt.Errorf("scanning invoice line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertExceptionCase inserts a new exception case. The domain ExceptionCase
// carries no ID/tenant, so the caller must populate TenantID; the adapter
// generates the id (the table default is gen_random_uuid()::TEXT, used when
// id is empty) and maps the domain fields onto the exception_cases columns.
func (a *Adapter) UpsertExceptionCase(ctx context.Context, exc *domain.ExceptionCase) error {
	detailJSON, _ := json.Marshal(exc.Metadata)

	query := `INSERT INTO exception_cases (
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	) VALUES (
		CASE WHEN $1 = '' THEN gen_random_uuid()::TEXT ELSE $1 END,
		$2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW()
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		type = EXCLUDED.type,
		severity = EXCLUDED.severity,
		status = EXCLUDED.status,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_id = EXCLUDED.po_id,
		po_line_ref = EXCLUDED.po_line_ref,
		grn_id = EXCLUDED.grn_id,
		grn_line_ref = EXCLUDED.grn_line_ref,
		invoice_id = EXCLUDED.invoice_id,
		invoice_line_ref = EXCLUDED.invoice_line_ref,
		description = EXCLUDED.description,
		detail = EXCLUDED.detail,
		updated_at = EXCLUDED.updated_at`

	// po_id/grn_id/invoice_id are nullable parent links in the schema. The
	// domain ExceptionCase carries only line references (POLineRef/GRNLineRef/
	// InvoiceLineRef), not parent IDs, so we persist NULL for the parent-ID
	// columns. They remain available for future cross-entity joins.
	_, err := a.getExec(ctx).Exec(ctx, query,
		exc.ID, exc.TenantID, string(exc.Type), string(exc.Severity), exc.Status, exc.VendorGSTIN,
		nil, exc.POLineRef, nil, exc.GRNLineRef, nil, exc.InvoiceLineRef,
		exc.Description, detailJSON,
	)
	if err != nil {
		return fmt.Errorf("upserting exception case: %w", err)
	}
	return nil
}

// GetExceptionCaseByID retrieves an exception case by id and tenant_id.
func (a *Adapter) GetExceptionCaseByID(ctx context.Context, id, tenantID string) (*domain.ExceptionCase, error) {
	query := `SELECT
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	FROM exception_cases WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	exc := &domain.ExceptionCase{}
	var excType, severity, status string
	var poID, grnID, invoiceID *string
	var detailJSON []byte

	err := row.Scan(
		&exc.ID, &exc.TenantID, &excType, &severity, &status, &exc.VendorGSTIN,
		&poID, &exc.POLineRef, &grnID, &exc.GRNLineRef, &invoiceID, &exc.InvoiceLineRef,
		&exc.Description, &detailJSON, &exc.CreatedAt, &exc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting exception case %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting exception case %s: %w", id, err)
	}

	exc.Type = domain.MismatchType(excType)
	exc.Severity = domain.ExceptionSeverity(severity)
	exc.Status = status
	if len(detailJSON) > 0 {
		_ = json.Unmarshal(detailJSON, &exc.Metadata)
	}
	if exc.Metadata == nil {
		exc.Metadata = make(map[string]any)
	}

	return exc, nil
}

// ListExceptionCases retrieves exception cases for a tenant, optionally filtered
// by status and/or mismatch type, with pagination. Empty filters disable the
// corresponding filter.
func (a *Adapter) ListExceptionCases(ctx context.Context, tenantID, status, mismatchType string, limit, offset int) ([]*domain.ExceptionCase, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	FROM exception_cases WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if mismatchType != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, mismatchType)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing exception cases: %w", err)
	}
	defer rows.Close()

	var cases []*domain.ExceptionCase
	for rows.Next() {
		exc := &domain.ExceptionCase{}
		var excType, severity, status string
		var poID, grnID, invoiceID *string
		var detailJSON []byte

		if err := rows.Scan(
			&exc.ID, &exc.TenantID, &excType, &severity, &status, &exc.VendorGSTIN,
			&poID, &exc.POLineRef, &grnID, &exc.GRNLineRef, &invoiceID, &exc.InvoiceLineRef,
			&exc.Description, &detailJSON, &exc.CreatedAt, &exc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning exception case row: %w", err)
		}

		exc.Type = domain.MismatchType(excType)
		exc.Severity = domain.ExceptionSeverity(severity)
		exc.Status = status
		if len(detailJSON) > 0 {
			_ = json.Unmarshal(detailJSON, &exc.Metadata)
		}
		if exc.Metadata == nil {
			exc.Metadata = make(map[string]any)
		}

		cases = append(cases, exc)
	}

	return cases, nil
}

// UpdateExceptionCaseStatus transitions an exception case to a new lifecycle
// status. The status value is validated by the caller (domain constants); the
// adapter only persists the transition. Optimistic-lock-free: status changes
// are append-only lifecycle transitions recorded via audit elsewhere.
func (a *Adapter) UpdateExceptionCaseStatus(ctx context.Context, id, tenantID, status string) error {
	if status == "" {
		return fmt.Errorf("updating exception case %s: status must not be empty", id)
	}
	query := `UPDATE exception_cases SET status = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`
	tag, err := a.getExec(ctx).Exec(ctx, query, status, id, tenantID)
	if err != nil {
		return fmt.Errorf("updating exception case %s status: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("updating exception case %s status: %w", id, ErrNotFound)
	}
	return nil
}

// ListPurchaseOrders retrieves purchase orders for a tenant, with pagination.
func (a *Adapter) ListPurchaseOrders(ctx context.Context, tenantID string, limit, offset int) ([]*domain.PurchaseOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	FROM purchase_orders WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing purchase orders: %w", err)
	}
	defer rows.Close()

	var pos []*domain.PurchaseOrder
	for rows.Next() {
		po := &domain.PurchaseOrder{}
		if err := rows.Scan(
			&po.ID, &po.TenantID, &po.DocumentNo, &po.VendorGSTIN, &po.OrderDate,
			&po.Version, &po.CreatedAt, &po.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning purchase order row: %w", err)
		}
		pos = append(pos, po)
	}
	return pos, nil
}

// ListGoodsReceipts retrieves goods receipts for a tenant, optionally filtered
// by PO number, with pagination. Empty poNumber disables the filter.
func (a *Adapter) ListGoodsReceipts(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.GoodsReceipt, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	FROM goods_receipts WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if poNumber != "" {
		query += fmt.Sprintf(" AND po_number = $%d", argIdx)
		args = append(args, poNumber)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing goods receipts: %w", err)
	}
	defer rows.Close()

	var grns []*domain.GoodsReceipt
	for rows.Next() {
		gr := &domain.GoodsReceipt{}
		if err := rows.Scan(
			&gr.ID, &gr.TenantID, &gr.DocumentNo, &gr.VendorGSTIN, &gr.PONumber, &gr.ReceiptDate,
			&gr.Version, &gr.CreatedAt, &gr.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning goods receipt row: %w", err)
		}
		grns = append(grns, gr)
	}
	return grns, nil
}

// ListInvoices retrieves invoices for a tenant, optionally filtered by PO number,
// with pagination. Empty poNumber disables the filter.
func (a *Adapter) ListInvoices(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.Invoice, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	FROM invoices WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if poNumber != "" {
		query += fmt.Sprintf(" AND po_number = $%d", argIdx)
		args = append(args, poNumber)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing invoices: %w", err)
	}
	defer rows.Close()

	var invs []*domain.Invoice
	for rows.Next() {
		inv := &domain.Invoice{}
		if err := rows.Scan(
			&inv.ID, &inv.TenantID, &inv.DocumentNo, &inv.VendorGSTIN, &inv.PONumber, &inv.InvoiceDate,
			&inv.Version, &inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning invoice row: %w", err)
		}
		invs = append(invs, inv)
	}
	return invs, nil
}

// Compile-time interface checks.
var _ providers.DBProvider = (*Adapter)(nil)
var _ providers.UsageProvider = (*Adapter)(nil)
