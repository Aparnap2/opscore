package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

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
