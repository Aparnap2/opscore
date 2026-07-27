package main

import (
	"context"
	"fmt"

	"github.com/aparna/opscore/internal/domain"
)

// staticTenantProvider is a simple in-memory TenantProvider that only
// knows about the "default" tenant. Replace with a DB-backed adapter
// once tenant management is implemented.
type staticTenantProvider struct{}

func (staticTenantProvider) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	if id == "default" {
		return domain.NewTenant("default", "Default", "default"), nil
	}
	return nil, fmt.Errorf("tenant not found: %s", id)
}

func (staticTenantProvider) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	if slug == "default" {
		return domain.NewTenant("default", "Default", "default"), nil
	}
	return nil, fmt.Errorf("tenant not found: %s", slug)
}

func (staticTenantProvider) CreateTenant(_ context.Context, t *domain.Tenant) error {
	return fmt.Errorf("CreateTenant not implemented by staticTenantProvider")
}

func (staticTenantProvider) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	return []*domain.Tenant{domain.NewTenant("default", "Default", "default")}, nil
}

func (staticTenantProvider) UpdateTenantStatus(_ context.Context, id, status string) error {
	if id == "default" {
		return nil
	}
	return fmt.Errorf("tenant not found: %s", id)
}
