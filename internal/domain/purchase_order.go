package domain

import (
	"errors"
	"time"
)

// PurchaseOrderLine is a single line item on a purchase order.
type PurchaseOrderLine struct {
	LineRef    string  `json:"line_ref"`
	ItemCode   string  `json:"item_code"`
	ItemDesc   string  `json:"item_desc,omitempty"`
	UoM        string  `json:"uom,omitempty"`
	Quantity   float64 `json:"quantity"`
	UnitRate   float64 `json:"unit_rate"`
	TaxRatePct float64 `json:"tax_rate_pct"`
}

// PurchaseOrder is a manufacturing purchase order (deterministic domain model).
type PurchaseOrder struct {
	ID          string              `json:"id"`
	TenantID    string              `json:"tenant_id"`
	DocumentNo  string              `json:"document_no"`
	VendorGSTIN string              `json:"vendor_gstin"`
	OrderDate   time.Time           `json:"order_date"`
	Lines       []PurchaseOrderLine `json:"lines"`
	Version     int                 `json:"version"`
}

// Validate checks the PO for required fields and deterministic business rules.
func (po *PurchaseOrder) Validate() error {
	if po.DocumentNo == "" {
		return errors.New("purchase order: document number is required")
	}
	if po.VendorGSTIN == "" {
		return errors.New("purchase order: vendor GSTIN is required")
	}
	if !ValidateGST(po.VendorGSTIN) {
		return errors.New("purchase order: invalid vendor GSTIN format")
	}
	if len(po.Lines) == 0 {
		return errors.New("purchase order: at least one line item is required")
	}
	seen := make(map[string]bool)
	for _, l := range po.Lines {
		if l.LineRef == "" {
			return errors.New("purchase order: line reference is required")
		}
		if seen[l.LineRef] {
			return errors.New("purchase order: duplicate line reference " + l.LineRef)
		}
		seen[l.LineRef] = true
		if l.ItemCode == "" {
			return errors.New("purchase order: item code is required on line " + l.LineRef)
		}
		if l.Quantity <= 0 {
			return errors.New("purchase order: quantity must be positive on line " + l.LineRef)
		}
		if l.UnitRate < 0 {
			return errors.New("purchase order: unit rate must be non-negative on line " + l.LineRef)
		}
		if l.TaxRatePct < 0 {
			return errors.New("purchase order: tax rate must be non-negative on line " + l.LineRef)
		}
	}
	return nil
}

// ComputePOTotal returns the order total including line tax.
func (po *PurchaseOrder) ComputePOTotal() float64 {
	var total float64
	for _, l := range po.Lines {
		total += l.Quantity*l.UnitRate + l.Quantity*l.UnitRate*(l.TaxRatePct/100.0)
	}
	return round2(total)
}
