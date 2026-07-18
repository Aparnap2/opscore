package domain

import (
	"errors"
	"time"
)

// GoodsReceiptLine is a single received line item against a purchase order.
type GoodsReceiptLine struct {
	LineRef     string  `json:"line_ref"`
	POLineRef   string  `json:"po_line_ref"`
	ItemCode    string  `json:"item_code"`
	ReceivedQty float64 `json:"received_qty"`
	UoM         string  `json:"uom,omitempty"`
}

// GoodsReceipt is a goods receipt note (GRN) for a purchase order.
type GoodsReceipt struct {
	ID          string             `json:"id"`
	TenantID    string             `json:"tenant_id"`
	DocumentNo  string             `json:"document_no"`
	VendorGSTIN string             `json:"vendor_gstin"`
	PONumber    string             `json:"po_number"`
	ReceiptDate time.Time          `json:"receipt_date"`
	Lines       []GoodsReceiptLine `json:"lines"`
	Version     int                `json:"version"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// Validate checks the GRN for required fields and deterministic business rules.
func (gr *GoodsReceipt) Validate() error {
	if gr.DocumentNo == "" {
		return errors.New("goods receipt: document number is required")
	}
	if gr.PONumber == "" {
		return errors.New("goods receipt: PO number is required")
	}
	if gr.VendorGSTIN == "" {
		return errors.New("goods receipt: vendor GSTIN is required")
	}
	if !ValidateGST(gr.VendorGSTIN) {
		return errors.New("goods receipt: invalid vendor GSTIN format")
	}
	if len(gr.Lines) == 0 {
		return errors.New("goods receipt: at least one line item is required")
	}
	seen := make(map[string]bool)
	for _, l := range gr.Lines {
		if l.LineRef == "" {
			return errors.New("goods receipt: line reference is required")
		}
		if seen[l.LineRef] {
			return errors.New("goods receipt: duplicate line reference " + l.LineRef)
		}
		seen[l.LineRef] = true
		if l.POLineRef == "" {
			return errors.New("goods receipt: PO line reference is required on line " + l.LineRef)
		}
		if l.ItemCode == "" {
			return errors.New("goods receipt: item code is required on line " + l.LineRef)
		}
		if l.ReceivedQty < 0 {
			return errors.New("goods receipt: received quantity must be non-negative on line " + l.LineRef)
		}
	}
	return nil
}

// ReceivedQtyForPOLine returns the total received quantity for a given PO line reference.
func (gr *GoodsReceipt) ReceivedQtyForPOLine(poLineRef string) float64 {
	var qty float64
	for _, l := range gr.Lines {
		if l.POLineRef == poLineRef {
			qty += l.ReceivedQty
		}
	}
	return qty
}
