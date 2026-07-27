package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aparna/opscore/internal/domain"
)

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
