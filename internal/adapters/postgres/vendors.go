package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

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
