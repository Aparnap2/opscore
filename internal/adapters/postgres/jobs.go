package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aparna/opscore/internal/domain"
)

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
