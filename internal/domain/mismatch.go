package domain

import (
	"fmt"
	"math"
)

// round2 rounds a float to 2 decimal places deterministically.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// MismatchType enumerates the deterministic exception classes detected on the
// PO -> GRN -> Invoice transaction flow.
type MismatchType string

const (
	MismatchMissingGRN     MismatchType = "MISSING_GRN"
	MismatchPartialReceipt MismatchType = "PARTIAL_RECEIPT"
	MismatchQtyVariance    MismatchType = "QTY_VARIANCE"
	MismatchPriceVariance  MismatchType = "PRICE_VARIANCE"
	MismatchTaxVariance    MismatchType = "TAX_VARIANCE"
	MismatchMissingInvoice MismatchType = "MISSING_INVOICE"
	MismatchVendorMismatch MismatchType = "VENDOR_MISMATCH"
)

// ExceptionSeverity ranks detected exceptions for triage and HITL routing.
type ExceptionSeverity string

const (
	SeverityLow      ExceptionSeverity = "LOW"
	SeverityMedium   ExceptionSeverity = "MEDIUM"
	SeverityHigh     ExceptionSeverity = "HIGH"
	SeverityCritical ExceptionSeverity = "CRITICAL"
)

// ExceptionCase is a typed, deterministic output of mismatch detection.
// It maps cleanly into DB tables, audit events, and HITL routing.
type ExceptionCase struct {
	Type           MismatchType      `json:"type"`
	Severity       ExceptionSeverity `json:"severity"`
	VendorGSTIN    string            `json:"vendor_gstin"`
	POLineRef      string            `json:"po_line_ref,omitempty"`
	GRNLineRef     string            `json:"grn_line_ref,omitempty"`
	InvoiceLineRef string            `json:"invoice_line_ref,omitempty"`
	Description    string            `json:"description"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
}

// MismatchTolerance defines the deterministic thresholds used by DetectMismatch.
// QuantityVariancePct and PriceVariancePct are expressed as percentages (e.g. 2.0 = 2%).
// A value of 0 disables the corresponding check.
type MismatchTolerance struct {
	QuantityVariancePct float64
	PriceVariancePct    float64
	TaxVariancePct      float64
}

// DefaultMismatchTolerance returns the standard tolerance policy.
func DefaultMismatchTolerance() MismatchTolerance {
	return MismatchTolerance{
		QuantityVariancePct: 2.0,
		PriceVariancePct:    2.0,
		TaxVariancePct:      2.0,
	}
}

// DetectMismatch compares a purchase order, optional goods receipt, and optional
// invoice and returns the list of deterministic exceptions. No I/O, no LLM.
//
// Line matching is performed by item code. When a GRN or Invoice links to the PO
// via a PO line reference, that reference is used to associate the exception.
func DetectMismatch(po *PurchaseOrder, gr *GoodsReceipt, inv *Invoice, tol MismatchTolerance) []ExceptionCase {
	var exceptions []ExceptionCase

	if po == nil {
		return exceptions
	}

	// Index PO lines by POLineRef (the explicit foreign key). When a PO line
	// reference is absent on the GRN/Invoice side, fall back to matching by
	// ItemCode. Keying on POLineRef avoids the silent false-match that occurs
	// when a PO repeats the same ItemCode across multiple lines.
	poByRef := make(map[string]PurchaseOrderLine)
	poByItem := make(map[string]PurchaseOrderLine)
	for _, l := range po.Lines {
		poByRef[l.LineRef] = l
		poByItem[l.ItemCode] = l
	}
	matchPOLine := func(ref, itemCode string) (PurchaseOrderLine, bool) {
		if ref != "" {
			if l, ok := poByRef[ref]; ok {
				return l, true
			}
		}
		if itemCode != "" {
			if l, ok := poByItem[itemCode]; ok {
				return l, true
			}
		}
		return PurchaseOrderLine{}, false
	}

	// --- GRN checks (only when a GRN is present) ---
	if gr != nil {
		if gr.VendorGSTIN != po.VendorGSTIN {
			exceptions = append(exceptions, ExceptionCase{
				Type:        MismatchVendorMismatch,
				Severity:    SeverityHigh,
				VendorGSTIN: gr.VendorGSTIN,
				Description: fmt.Sprintf("GRN %s vendor GSTIN %s does not match PO %s vendor GSTIN %s", gr.DocumentNo, gr.VendorGSTIN, po.DocumentNo, po.VendorGSTIN),
				Metadata:    map[string]any{"po_no": po.DocumentNo, "grn_no": gr.DocumentNo},
			})
		}

		for _, grLine := range gr.Lines {
			poLine, ok := matchPOLine(grLine.POLineRef, grLine.ItemCode)
			if !ok {
				continue
			}
			received := gr.ReceivedQtyForPOLine(grLine.POLineRef)
			if received < poLine.Quantity && poLine.Quantity > 0 {
				pct := (poLine.Quantity - received) / poLine.Quantity * 100.0
				sev := SeverityMedium
				if pct > tol.QuantityVariancePct {
					sev = SeverityHigh
				}
				exceptions = append(exceptions, ExceptionCase{
					Type:        MismatchPartialReceipt,
					Severity:    sev,
					VendorGSTIN: po.VendorGSTIN,
					POLineRef:   grLine.POLineRef,
					GRNLineRef:  grLine.LineRef,
					Description: fmt.Sprintf("Partial receipt on PO line %s: received %.2f of %.2f (%.2f%% short)", grLine.POLineRef, received, poLine.Quantity, pct),
					Metadata:    map[string]any{"po_qty": poLine.Quantity, "received_qty": received, "variance_pct": round2(pct)},
				})
			}
		}
	} else {
		// No GRN supplied: flag missing GRN for every PO line as a monitoring exception.
		exceptions = append(exceptions, ExceptionCase{
			Type:        MismatchMissingGRN,
			Severity:    SeverityMedium,
			VendorGSTIN: po.VendorGSTIN,
			Description: fmt.Sprintf("No goods receipt found for PO %s", po.DocumentNo),
			Metadata:    map[string]any{"po_no": po.DocumentNo},
		})
	}

	// --- Invoice checks (only when an invoice is present) ---
	if inv != nil {
		if inv.VendorGSTIN != po.VendorGSTIN {
			exceptions = append(exceptions, ExceptionCase{
				Type:        MismatchVendorMismatch,
				Severity:    SeverityHigh,
				VendorGSTIN: inv.VendorGSTIN,
				Description: fmt.Sprintf("Invoice %s vendor GSTIN %s does not match PO %s vendor GSTIN %s", inv.DocumentNo, inv.VendorGSTIN, po.DocumentNo, po.VendorGSTIN),
				Metadata:    map[string]any{"po_no": po.DocumentNo, "invoice_no": inv.DocumentNo},
			})
		}

		for _, invLine := range inv.Lines {
			poLine, ok := matchPOLine(invLine.POLineRef, invLine.ItemCode)
			if !ok {
				continue
			}

			// Quantity variance
			if poLine.Quantity > 0 {
				qtyPct := (invLine.Quantity - poLine.Quantity) / poLine.Quantity * 100.0
				if tol.QuantityVariancePct > 0 && absFloat(qtyPct) > tol.QuantityVariancePct {
					sev := SeverityMedium
					if absFloat(qtyPct) > 10 {
						sev = SeverityHigh
					}
					exceptions = append(exceptions, ExceptionCase{
						Type:           MismatchQtyVariance,
						Severity:       sev,
						VendorGSTIN:    po.VendorGSTIN,
						POLineRef:      poLine.LineRef,
						InvoiceLineRef: invLine.LineRef,
						Description:    fmt.Sprintf("Invoice qty %.2f vs PO qty %.2f on item %s (%.2f%%)", invLine.Quantity, poLine.Quantity, invLine.ItemCode, qtyPct),
						Metadata:       map[string]any{"po_qty": poLine.Quantity, "invoice_qty": invLine.Quantity, "variance_pct": round2(qtyPct)},
					})
				}
			}

			// Price variance
			if tol.PriceVariancePct > 0 && poLine.UnitRate > 0 {
				pricePct := (invLine.UnitRate - poLine.UnitRate) / poLine.UnitRate * 100.0
				if absFloat(pricePct) > tol.PriceVariancePct {
					sev := SeverityMedium
					if absFloat(pricePct) > 10 {
						sev = SeverityHigh
					}
					exceptions = append(exceptions, ExceptionCase{
						Type:           MismatchPriceVariance,
						Severity:       sev,
						VendorGSTIN:    po.VendorGSTIN,
						POLineRef:      poLine.LineRef,
						InvoiceLineRef: invLine.LineRef,
						Description:    fmt.Sprintf("Invoice rate %.2f vs PO rate %.2f on item %s (%.2f%%)", invLine.UnitRate, poLine.UnitRate, invLine.ItemCode, pricePct),
						Metadata:       map[string]any{"po_rate": poLine.UnitRate, "invoice_rate": invLine.UnitRate, "variance_pct": round2(pricePct)},
					})
				}
			}

			// Tax variance
			if tol.TaxVariancePct > 0 && poLine.TaxRatePct > 0 {
				taxPct := invLine.TaxRatePct - poLine.TaxRatePct
				if absFloat(taxPct) > tol.TaxVariancePct {
					sev := SeverityMedium
					if absFloat(taxPct) > 5 {
						sev = SeverityHigh
					}
					exceptions = append(exceptions, ExceptionCase{
						Type:           MismatchTaxVariance,
						Severity:       sev,
						VendorGSTIN:    po.VendorGSTIN,
						POLineRef:      poLine.LineRef,
						InvoiceLineRef: invLine.LineRef,
						Description:    fmt.Sprintf("Invoice tax %.2f%% vs PO tax %.2f%% on item %s", invLine.TaxRatePct, poLine.TaxRatePct, invLine.ItemCode),
						Metadata:       map[string]any{"po_tax": poLine.TaxRatePct, "invoice_tax": invLine.TaxRatePct, "variance_pct": round2(taxPct)},
					})
				}
			}
		}
	}

	return exceptions
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
