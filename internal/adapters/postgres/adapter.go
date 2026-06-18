package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ErrVersionConflict is returned when an optimistic lock version check fails.
var ErrVersionConflict = errors.New("version conflict: job was modified concurrently")

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
		err := a.pool.QueryRow(ctx, query,
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
		$19, $20, $21
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

	_, execErr := a.pool.Exec(ctx, query,
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

	row := a.pool.QueryRow(ctx, query, id, tenantID)

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

	rows, err := a.pool.Query(ctx, query, args...)
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

	_, err := a.pool.Exec(ctx, query,
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

// GetVendor retrieves a vendor by id.
func (a *Adapter) GetVendor(ctx context.Context, id string) (*domain.Vendor, error) {
	query := `SELECT
		id, tenant_id, name, gst_number, pan_number, ifsc_code, bank_account,
		risk_tier, risk_score, approved, risk_flags, status, last_transaction_at,
		trust_battery, created_at, updated_at
	FROM vendors WHERE id = $1`

	row := a.pool.QueryRow(ctx, query, id)

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

	rows, err := a.pool.Query(ctx, query, tenantID)
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

	_, err := a.pool.Exec(ctx, query,
		doc.ID, doc.TenantID, doc.JobID, doc.FileName, doc.StoragePath,
		doc.Type, doc.Status, doc.ContentHash, extractedJSON, doc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by id.
func (a *Adapter) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	query := `SELECT id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	FROM documents WHERE id = $1`

	row := a.pool.QueryRow(ctx, query, id)

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

	row := a.pool.QueryRow(ctx, query, tenantID, contentHash)

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

	_, err := a.pool.Exec(ctx, query,
		req.ID, req.TenantID, req.JobID, req.Reason, req.Status,
		req.SentAt, req.RespondedAt, req.Responder, req.Decision, req.SlackTS,
	)
	if err != nil {
		return fmt.Errorf("upserting HITL request: %w", err)
	}
	return nil
}

// GetHITLRequest retrieves a HITL request by id.
func (a *Adapter) GetHITLRequest(ctx context.Context, id string) (*domain.HITLRequest, error) {
	query := `SELECT id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts
	FROM hitl_requests WHERE id = $1`

	row := a.pool.QueryRow(ctx, query, id)

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

// ListPendingHITL retrieves all pending HITL requests for a tenant.
func (a *Adapter) ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	query := `SELECT id, tenant_id, job_id, reason, status, sent_at, responded_at,
		responder, decision, slack_ts
	FROM hitl_requests WHERE tenant_id = $1 AND status = 'pending'
	ORDER BY sent_at DESC`

	rows, err := a.pool.Query(ctx, query, tenantID)
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
		_, err := a.pool.Exec(ctx, query,
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
	_, err := a.pool.Exec(ctx, query,
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

	rows, err := a.pool.Query(ctx, query, args...)
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

// Compile-time interface check.
var _ providers.DBProvider = (*Adapter)(nil)
