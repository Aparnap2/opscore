package domain

import (
	"errors"
	"time"
)

// InvoiceLine is a single line item on a supplier invoice.
type InvoiceLine struct {
	LineRef    string  `json:"line_ref"`
	POLineRef  string  `json:"po_line_ref,omitempty"`
	ItemCode   string  `json:"item_code"`
	Quantity   float64 `json:"quantity"`
	UnitRate   float64 `json:"unit_rate"`
	TaxRatePct float64 `json:"tax_rate_pct"`
}

// Invoice is a supplier invoice against one or more purchase orders.
type Invoice struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenant_id"`
	DocumentNo  string        `json:"document_no"`
	VendorGSTIN string        `json:"vendor_gstin"`
	PONumber    string        `json:"po_number,omitempty"`
	InvoiceDate time.Time     `json:"invoice_date"`
	Lines       []InvoiceLine `json:"lines"`
	Version     int           `json:"version"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// Validate checks the invoice for required fields and deterministic business rules.
func (inv *Invoice) Validate() error {
	if inv.DocumentNo == "" {
		return errors.New("invoice: document number is required")
	}
	if inv.VendorGSTIN == "" {
		return errors.New("invoice: vendor GSTIN is required")
	}
	if !ValidateGST(inv.VendorGSTIN) {
		return errors.New("invoice: invalid vendor GSTIN format")
	}
	if len(inv.Lines) == 0 {
		return errors.New("invoice: at least one line item is required")
	}
	seen := make(map[string]bool)
	for _, l := range inv.Lines {
		if l.LineRef == "" {
			return errors.New("invoice: line reference is required")
		}
		if seen[l.LineRef] {
			return errors.New("invoice: duplicate line reference " + l.LineRef)
		}
		seen[l.LineRef] = true
		if l.ItemCode == "" {
			return errors.New("invoice: item code is required on line " + l.LineRef)
		}
		if l.Quantity <= 0 {
			return errors.New("invoice: quantity must be positive on line " + l.LineRef)
		}
		if l.UnitRate < 0 {
			return errors.New("invoice: unit rate must be non-negative on line " + l.LineRef)
		}
		if l.TaxRatePct < 0 {
			return errors.New("invoice: tax rate must be non-negative on line " + l.LineRef)
		}
	}
	return nil
}

// InvoiceTotal returns the invoice total including line tax.
func (inv *Invoice) InvoiceTotal() float64 {
	var total float64
	for _, l := range inv.Lines {
		total += l.Quantity*l.UnitRate + l.Quantity*l.UnitRate*(l.TaxRatePct/100.0)
	}
	return round2(total)
}

// IsDuplicate returns true when the invoice matches an existing invoice on the
// deterministic identity keys: document number, vendor GSTIN, and total.
func (inv *Invoice) IsDuplicate(existing []Invoice) bool {
	total := inv.InvoiceTotal()
	for _, e := range existing {
		if e.TenantID == inv.TenantID &&
			e.DocumentNo == inv.DocumentNo &&
			e.VendorGSTIN == inv.VendorGSTIN &&
			e.InvoiceTotal() == total {
			return true
		}
	}
	return false
}
