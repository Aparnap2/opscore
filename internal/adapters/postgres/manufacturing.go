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

// ---------------------------------------------------------------------------
// Manufacturing pivot (Phase 1.4A) — Purchase Order / Goods Receipt / Invoice
// ---------------------------------------------------------------------------
//
// All entity tables use a composite primary key (tenant_id, id) and are
// protected by RLS via app.current_tenant_id(). The adapter relies on the
// caller having set the tenant context (SetTenantContextTx inside an explicit
// transaction, or the request txMiddleware) so RLS is satisfied. Queries
// therefore always filter by both tenant_id and id.
//
// Optimistic locking: mutable entities carry a version column. On UPDATE we
// include `WHERE id=$1 AND tenant_id=$2 AND version=$3` and return
// ErrVersionConflict when rows-affected == 0 and a version was supplied. On
// insert we set version = 0.
//
// Line items (purchase_order_lines, goods_receipt_lines, invoice_lines) are
// upserted by deleting all existing children for the parent within the same
// tenant and re-inserting from the domain slice. This is simpler and fully
// deterministic for the small cardinality of line items on these documents,
// and keeps the adapter free of per-line diffing business logic.

// UpsertPurchaseOrder inserts or updates a purchase order and its line items
// with optimistic locking.
func (a *Adapter) UpsertPurchaseOrder(ctx context.Context, po *domain.PurchaseOrder) error {
	now := time.Now()
	if po.CreatedAt.IsZero() {
		po.CreatedAt = now
	}
	po.UpdatedAt = now

	if po.Version >= 0 {
		query := `UPDATE purchase_orders SET
			document_no = $1, vendor_gstin = $2, order_date = $3,
			version = version + 1, updated_at = $4
		WHERE id = $5 AND tenant_id = $6 AND version = $7
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			po.DocumentNo, po.VendorGSTIN, po.OrderDate,
			po.UpdatedAt,
			po.ID, po.TenantID, po.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetPurchaseOrderByID(ctx, po.ID, po.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
				// Not found — fall through to INSERT.
			} else {
				return fmt.Errorf("updating purchase order: %w", err)
			}
		} else {
			po.Version = newVersion
			return a.upsertPOLines(ctx, po)
		}
	}

	query := `INSERT INTO purchase_orders (
		id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		order_date = EXCLUDED.order_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		po.ID, po.TenantID, po.DocumentNo, po.VendorGSTIN, po.OrderDate,
		po.Version, po.CreatedAt, po.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting purchase order: %w", err)
	}
	return a.upsertPOLines(ctx, po)
}

// upsertPOLines deletes existing line items for the PO within the tenant and
// re-inserts the current domain slice.
func (a *Adapter) upsertPOLines(ctx context.Context, po *domain.PurchaseOrder) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM purchase_order_lines WHERE tenant_id = $1 AND po_id = $2`,
		po.TenantID, po.ID,
	); err != nil {
		return fmt.Errorf("deleting purchase order lines: %w", err)
	}

	for _, l := range po.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO purchase_order_lines (
				id, tenant_id, po_id, line_ref, item_code, item_desc,
				uom, quantity, unit_rate, tax_rate_pct, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW()
			)`,
			po.TenantID, po.ID, l.LineRef, l.ItemCode, l.ItemDesc,
			l.UoM, l.Quantity, l.UnitRate, l.TaxRatePct,
		); err != nil {
			return fmt.Errorf("inserting purchase order line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetPurchaseOrderByID retrieves a purchase order and its line items by id and tenant_id.
func (a *Adapter) GetPurchaseOrderByID(ctx context.Context, id, tenantID string) (*domain.PurchaseOrder, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	FROM purchase_orders WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	po := &domain.PurchaseOrder{}
	err := row.Scan(
		&po.ID, &po.TenantID, &po.DocumentNo, &po.VendorGSTIN, &po.OrderDate,
		&po.Version, &po.CreatedAt, &po.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting purchase order %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting purchase order %s: %w", id, err)
	}

	lines, err := a.getPOLines(ctx, po.ID, po.TenantID)
	if err != nil {
		return nil, err
	}
	po.Lines = lines

	return po, nil
}

// getPOLines retrieves the line items for a purchase order within the tenant.
func (a *Adapter) getPOLines(ctx context.Context, poID, tenantID string) ([]domain.PurchaseOrderLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, item_code, item_desc, uom, quantity, unit_rate, tax_rate_pct
		FROM purchase_order_lines WHERE tenant_id = $1 AND po_id = $2 ORDER BY line_ref`,
		tenantID, poID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying purchase order lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.PurchaseOrderLine
	for rows.Next() {
		var l domain.PurchaseOrderLine
		if err := rows.Scan(
			&l.LineRef, &l.ItemCode, &l.ItemDesc, &l.UoM,
			&l.Quantity, &l.UnitRate, &l.TaxRatePct,
		); err != nil {
			return nil, fmt.Errorf("scanning purchase order line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertGoodsReceipt inserts or updates a goods receipt and its line items
// with optimistic locking.
func (a *Adapter) UpsertGoodsReceipt(ctx context.Context, gr *domain.GoodsReceipt) error {
	now := time.Now()
	if gr.CreatedAt.IsZero() {
		gr.CreatedAt = now
	}
	gr.UpdatedAt = now

	if gr.Version >= 0 {
		query := `UPDATE goods_receipts SET
			document_no = $1, vendor_gstin = $2, po_number = $3, receipt_date = $4,
			version = version + 1, updated_at = $5
		WHERE id = $6 AND tenant_id = $7 AND version = $8
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			gr.DocumentNo, gr.VendorGSTIN, gr.PONumber, gr.ReceiptDate,
			gr.UpdatedAt,
			gr.ID, gr.TenantID, gr.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetGoodsReceiptByID(ctx, gr.ID, gr.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
			} else {
				return fmt.Errorf("updating goods receipt: %w", err)
			}
		} else {
			gr.Version = newVersion
			return a.upsertGRLines(ctx, gr)
		}
	}

	query := `INSERT INTO goods_receipts (
		id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_number = EXCLUDED.po_number,
		receipt_date = EXCLUDED.receipt_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		gr.ID, gr.TenantID, gr.DocumentNo, gr.VendorGSTIN, gr.PONumber, gr.ReceiptDate,
		gr.Version, gr.CreatedAt, gr.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting goods receipt: %w", err)
	}
	return a.upsertGRLines(ctx, gr)
}

// upsertGRLines deletes existing line items for the GRN within the tenant and
// re-inserts the current domain slice.
func (a *Adapter) upsertGRLines(ctx context.Context, gr *domain.GoodsReceipt) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM goods_receipt_lines WHERE tenant_id = $1 AND grn_id = $2`,
		gr.TenantID, gr.ID,
	); err != nil {
		return fmt.Errorf("deleting goods receipt lines: %w", err)
	}

	for _, l := range gr.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO goods_receipt_lines (
				id, tenant_id, grn_id, line_ref, po_line_ref, item_code,
				received_qty, uom, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, NOW()
			)`,
			gr.TenantID, gr.ID, l.LineRef, l.POLineRef, l.ItemCode,
			l.ReceivedQty, l.UoM,
		); err != nil {
			return fmt.Errorf("inserting goods receipt line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetGoodsReceiptByID retrieves a goods receipt and its line items by id and tenant_id.
func (a *Adapter) GetGoodsReceiptByID(ctx context.Context, id, tenantID string) (*domain.GoodsReceipt, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	FROM goods_receipts WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	gr := &domain.GoodsReceipt{}
	err := row.Scan(
		&gr.ID, &gr.TenantID, &gr.DocumentNo, &gr.VendorGSTIN, &gr.PONumber, &gr.ReceiptDate,
		&gr.Version, &gr.CreatedAt, &gr.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting goods receipt %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting goods receipt %s: %w", id, err)
	}

	lines, err := a.getGRLines(ctx, gr.ID, gr.TenantID)
	if err != nil {
		return nil, err
	}
	gr.Lines = lines

	return gr, nil
}

// getGRLines retrieves the line items for a goods receipt within the tenant.
func (a *Adapter) getGRLines(ctx context.Context, grnID, tenantID string) ([]domain.GoodsReceiptLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, po_line_ref, item_code, received_qty, uom
		FROM goods_receipt_lines WHERE tenant_id = $1 AND grn_id = $2 ORDER BY line_ref`,
		tenantID, grnID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying goods receipt lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.GoodsReceiptLine
	for rows.Next() {
		var l domain.GoodsReceiptLine
		if err := rows.Scan(
			&l.LineRef, &l.POLineRef, &l.ItemCode, &l.ReceivedQty, &l.UoM,
		); err != nil {
			return nil, fmt.Errorf("scanning goods receipt line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertInvoice inserts or updates an invoice and its line items with optimistic locking.
func (a *Adapter) UpsertInvoice(ctx context.Context, inv *domain.Invoice) error {
	now := time.Now()
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = now
	}
	inv.UpdatedAt = now

	if inv.Version >= 0 {
		query := `UPDATE invoices SET
			document_no = $1, vendor_gstin = $2, po_number = $3, invoice_date = $4,
			version = version + 1, updated_at = $5
		WHERE id = $6 AND tenant_id = $7 AND version = $8
		RETURNING version`

		var newVersion int
		err := a.getExec(ctx).QueryRow(ctx, query,
			inv.DocumentNo, inv.VendorGSTIN, inv.PONumber, inv.InvoiceDate,
			inv.UpdatedAt,
			inv.ID, inv.TenantID, inv.Version,
		).Scan(&newVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := a.GetInvoiceByID(ctx, inv.ID, inv.TenantID)
				if getErr == nil {
					return ErrVersionConflict
				}
			} else {
				return fmt.Errorf("updating invoice: %w", err)
			}
		} else {
			inv.Version = newVersion
			return a.upsertInvoiceLines(ctx, inv)
		}
	}

	query := `INSERT INTO invoices (
		id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		document_no = EXCLUDED.document_no,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_number = EXCLUDED.po_number,
		invoice_date = EXCLUDED.invoice_date,
		version = EXCLUDED.version,
		updated_at = EXCLUDED.updated_at`

	_, err := a.getExec(ctx).Exec(ctx, query,
		inv.ID, inv.TenantID, inv.DocumentNo, inv.VendorGSTIN, inv.PONumber, inv.InvoiceDate,
		inv.Version, inv.CreatedAt, inv.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting invoice: %w", err)
	}
	return a.upsertInvoiceLines(ctx, inv)
}

// upsertInvoiceLines deletes existing line items for the invoice within the
// tenant and re-inserts the current domain slice.
func (a *Adapter) upsertInvoiceLines(ctx context.Context, inv *domain.Invoice) error {
	if _, err := a.getExec(ctx).Exec(ctx,
		`DELETE FROM invoice_lines WHERE tenant_id = $1 AND invoice_id = $2`,
		inv.TenantID, inv.ID,
	); err != nil {
		return fmt.Errorf("deleting invoice lines: %w", err)
	}

	for _, l := range inv.Lines {
		if _, err := a.getExec(ctx).Exec(ctx,
			`INSERT INTO invoice_lines (
				id, tenant_id, invoice_id, line_ref, po_line_ref, item_code,
				quantity, unit_rate, tax_rate_pct, created_at
			) VALUES (
				gen_random_uuid()::TEXT, $1, $2, $3, $4, $5, $6, $7, $8, NOW()
			)`,
			inv.TenantID, inv.ID, l.LineRef, l.POLineRef, l.ItemCode,
			l.Quantity, l.UnitRate, l.TaxRatePct,
		); err != nil {
			return fmt.Errorf("inserting invoice line %s: %w", l.LineRef, err)
		}
	}
	return nil
}

// GetInvoiceByID retrieves an invoice and its line items by id and tenant_id.
func (a *Adapter) GetInvoiceByID(ctx context.Context, id, tenantID string) (*domain.Invoice, error) {
	query := `SELECT
		id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	FROM invoices WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	inv := &domain.Invoice{}
	err := row.Scan(
		&inv.ID, &inv.TenantID, &inv.DocumentNo, &inv.VendorGSTIN, &inv.PONumber, &inv.InvoiceDate,
		&inv.Version, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting invoice %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting invoice %s: %w", id, err)
	}

	lines, err := a.getInvoiceLines(ctx, inv.ID, inv.TenantID)
	if err != nil {
		return nil, err
	}
	inv.Lines = lines

	return inv, nil
}

// getInvoiceLines retrieves the line items for an invoice within the tenant.
func (a *Adapter) getInvoiceLines(ctx context.Context, invoiceID, tenantID string) ([]domain.InvoiceLine, error) {
	rows, err := a.getExec(ctx).Query(ctx,
		`SELECT line_ref, po_line_ref, item_code, quantity, unit_rate, tax_rate_pct
		FROM invoice_lines WHERE tenant_id = $1 AND invoice_id = $2 ORDER BY line_ref`,
		tenantID, invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoice lines: %w", err)
	}
	defer rows.Close()

	var lines []domain.InvoiceLine
	for rows.Next() {
		var l domain.InvoiceLine
		if err := rows.Scan(
			&l.LineRef, &l.POLineRef, &l.ItemCode, &l.Quantity, &l.UnitRate, &l.TaxRatePct,
		); err != nil {
			return nil, fmt.Errorf("scanning invoice line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// UpsertExceptionCase inserts a new exception case. The domain ExceptionCase
// carries no ID/tenant, so the caller must populate TenantID; the adapter
// generates the id (the table default is gen_random_uuid()::TEXT, used when
// id is empty) and maps the domain fields onto the exception_cases columns.
func (a *Adapter) UpsertExceptionCase(ctx context.Context, exc *domain.ExceptionCase) error {
	detailJSON, _ := json.Marshal(exc.Metadata)

	query := `INSERT INTO exception_cases (
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	) VALUES (
		CASE WHEN $1 = '' THEN gen_random_uuid()::TEXT ELSE $1 END,
		$2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW()
	) ON CONFLICT (tenant_id, id) DO UPDATE SET
		type = EXCLUDED.type,
		severity = EXCLUDED.severity,
		status = EXCLUDED.status,
		vendor_gstin = EXCLUDED.vendor_gstin,
		po_id = EXCLUDED.po_id,
		po_line_ref = EXCLUDED.po_line_ref,
		grn_id = EXCLUDED.grn_id,
		grn_line_ref = EXCLUDED.grn_line_ref,
		invoice_id = EXCLUDED.invoice_id,
		invoice_line_ref = EXCLUDED.invoice_line_ref,
		description = EXCLUDED.description,
		detail = EXCLUDED.detail,
		updated_at = EXCLUDED.updated_at`

	// po_id/grn_id/invoice_id are nullable parent links in the schema. The
	// domain ExceptionCase carries only line references (POLineRef/GRNLineRef/
	// InvoiceLineRef), not parent IDs, so we persist NULL for the parent-ID
	// columns. They remain available for future cross-entity joins.
	_, err := a.getExec(ctx).Exec(ctx, query,
		exc.ID, exc.TenantID, string(exc.Type), string(exc.Severity), exc.Status, exc.VendorGSTIN,
		nil, exc.POLineRef, nil, exc.GRNLineRef, nil, exc.InvoiceLineRef,
		exc.Description, detailJSON,
	)
	if err != nil {
		return fmt.Errorf("upserting exception case: %w", err)
	}
	return nil
}

// GetExceptionCaseByID retrieves an exception case by id and tenant_id.
func (a *Adapter) GetExceptionCaseByID(ctx context.Context, id, tenantID string) (*domain.ExceptionCase, error) {
	query := `SELECT
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	FROM exception_cases WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	exc := &domain.ExceptionCase{}
	var excType, severity, status string
	var poID, grnID, invoiceID *string
	var detailJSON []byte

	err := row.Scan(
		&exc.ID, &exc.TenantID, &excType, &severity, &status, &exc.VendorGSTIN,
		&poID, &exc.POLineRef, &grnID, &exc.GRNLineRef, &invoiceID, &exc.InvoiceLineRef,
		&exc.Description, &detailJSON, &exc.CreatedAt, &exc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("getting exception case %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting exception case %s: %w", id, err)
	}

	exc.Type = domain.MismatchType(excType)
	exc.Severity = domain.ExceptionSeverity(severity)
	exc.Status = status
	if len(detailJSON) > 0 {
		_ = json.Unmarshal(detailJSON, &exc.Metadata)
	}
	if exc.Metadata == nil {
		exc.Metadata = make(map[string]any)
	}

	return exc, nil
}

// ListExceptionCases retrieves exception cases for a tenant, optionally filtered
// by status and/or mismatch type, with pagination. Empty filters disable the
// corresponding filter.
func (a *Adapter) ListExceptionCases(ctx context.Context, tenantID, status, mismatchType string, limit, offset int) ([]*domain.ExceptionCase, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT
		id, tenant_id, type, severity, status, vendor_gstin,
		po_id, po_line_ref, grn_id, grn_line_ref, invoice_id, invoice_line_ref,
		description, detail, created_at, updated_at
	FROM exception_cases WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if mismatchType != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, mismatchType)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing exception cases: %w", err)
	}
	defer rows.Close()

	var cases []*domain.ExceptionCase
	for rows.Next() {
		exc := &domain.ExceptionCase{}
		var excType, severity, status string
		var poID, grnID, invoiceID *string
		var detailJSON []byte

		if err := rows.Scan(
			&exc.ID, &exc.TenantID, &excType, &severity, &status, &exc.VendorGSTIN,
			&poID, &exc.POLineRef, &grnID, &exc.GRNLineRef, &invoiceID, &exc.InvoiceLineRef,
			&exc.Description, &detailJSON, &exc.CreatedAt, &exc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning exception case row: %w", err)
		}

		exc.Type = domain.MismatchType(excType)
		exc.Severity = domain.ExceptionSeverity(severity)
		exc.Status = status
		if len(detailJSON) > 0 {
			_ = json.Unmarshal(detailJSON, &exc.Metadata)
		}
		if exc.Metadata == nil {
			exc.Metadata = make(map[string]any)
		}

		cases = append(cases, exc)
	}

	return cases, nil
}

// UpdateExceptionCaseStatus transitions an exception case to a new lifecycle
// status. The status value is validated by the caller (domain constants); the
// adapter only persists the transition. Optimistic-lock-free: status changes
// are append-only lifecycle transitions recorded via audit elsewhere.
func (a *Adapter) UpdateExceptionCaseStatus(ctx context.Context, id, tenantID, status string) error {
	if status == "" {
		return fmt.Errorf("updating exception case %s: status must not be empty", id)
	}
	query := `UPDATE exception_cases SET status = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`
	tag, err := a.getExec(ctx).Exec(ctx, query, status, id, tenantID)
	if err != nil {
		return fmt.Errorf("updating exception case %s status: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("updating exception case %s status: %w", id, ErrNotFound)
	}
	return nil
}

// ListPurchaseOrders retrieves purchase orders for a tenant, with pagination.
func (a *Adapter) ListPurchaseOrders(ctx context.Context, tenantID string, limit, offset int) ([]*domain.PurchaseOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, order_date,
		version, created_at, updated_at
	FROM purchase_orders WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing purchase orders: %w", err)
	}
	defer rows.Close()

	var pos []*domain.PurchaseOrder
	for rows.Next() {
		po := &domain.PurchaseOrder{}
		if err := rows.Scan(
			&po.ID, &po.TenantID, &po.DocumentNo, &po.VendorGSTIN, &po.OrderDate,
			&po.Version, &po.CreatedAt, &po.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning purchase order row: %w", err)
		}
		pos = append(pos, po)
	}
	return pos, nil
}

// ListGoodsReceipts retrieves goods receipts for a tenant, optionally filtered
// by PO number, with pagination. Empty poNumber disables the filter.
func (a *Adapter) ListGoodsReceipts(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.GoodsReceipt, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, po_number, receipt_date,
		version, created_at, updated_at
	FROM goods_receipts WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if poNumber != "" {
		query += fmt.Sprintf(" AND po_number = $%d", argIdx)
		args = append(args, poNumber)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing goods receipts: %w", err)
	}
	defer rows.Close()

	var grns []*domain.GoodsReceipt
	for rows.Next() {
		gr := &domain.GoodsReceipt{}
		if err := rows.Scan(
			&gr.ID, &gr.TenantID, &gr.DocumentNo, &gr.VendorGSTIN, &gr.PONumber, &gr.ReceiptDate,
			&gr.Version, &gr.CreatedAt, &gr.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning goods receipt row: %w", err)
		}
		grns = append(grns, gr)
	}
	return grns, nil
}

// ListInvoices retrieves invoices for a tenant, optionally filtered by PO number,
// with pagination. Empty poNumber disables the filter.
func (a *Adapter) ListInvoices(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.Invoice, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, tenant_id, document_no, vendor_gstin, po_number, invoice_date,
		version, created_at, updated_at
	FROM invoices WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if poNumber != "" {
		query += fmt.Sprintf(" AND po_number = $%d", argIdx)
		args = append(args, poNumber)
		argIdx++
	}

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := a.getExec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing invoices: %w", err)
	}
	defer rows.Close()

	var invs []*domain.Invoice
	for rows.Next() {
		inv := &domain.Invoice{}
		if err := rows.Scan(
			&inv.ID, &inv.TenantID, &inv.DocumentNo, &inv.VendorGSTIN, &inv.PONumber, &inv.InvoiceDate,
			&inv.Version, &inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning invoice row: %w", err)
		}
		invs = append(invs, inv)
	}
	return invs, nil
}
