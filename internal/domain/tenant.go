package domain

import "time"

// Tenant represents a multi-tenant organization in OpsCore.
type Tenant struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Slug      string         `json:"slug"`
	Plan      string         `json:"plan"`
	Status    string         `json:"status"`
	Config    map[string]any `json:"config,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// NewTenant creates a new Tenant with default values.
func NewTenant(id, name, slug string) *Tenant {
	now := time.Now()
	return &Tenant{
		ID:        id,
		Name:      name,
		Slug:      slug,
		Plan:      "starter",
		Status:    "active",
		Config:    make(map[string]any),
		CreatedAt: now,
		UpdatedAt: now,
	}
}
