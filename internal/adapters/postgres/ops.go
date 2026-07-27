package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

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
