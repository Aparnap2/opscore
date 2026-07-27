package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

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
