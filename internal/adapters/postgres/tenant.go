package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

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
