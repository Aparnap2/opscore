package domain

import (
	"testing"
	"time"
)

func TestDetectMismatchLineMatching(t *testing.T) {
	po := samplePO()
	tol := DefaultMismatchTolerance()

	t.Run("happy path exact match", func(t *testing.T) {
		gr := &GoodsReceipt{
			DocumentNo: "GRN-001", VendorGSTIN: po.VendorGSTIN, PONumber: po.DocumentNo,
			ReceiptDate: time.Now(),
			Lines: []GoodsReceiptLine{
				{LineRef: "G1", POLineRef: "L1", ItemCode: "ITEM-A", ReceivedQty: 100},
				{LineRef: "G2", POLineRef: "L2", ItemCode: "ITEM-B", ReceivedQty: 50},
			},
		}
		inv := sampleInvoice()
		ex := DetectMismatch(po, gr, inv, tol)
		if len(ex) != 0 {
			t.Errorf("expected no mismatches on exact match, got %d: %+v", len(ex), ex)
		}
	})

	t.Run("missing GRN", func(t *testing.T) {
		inv := sampleInvoice()
		ex := DetectMismatch(po, nil, inv, tol)
		if !hasType(ex, MismatchMissingGRN) {
			t.Errorf("expected MISSING_GRN exception, got %+v", ex)
		}
	})

	t.Run("partial receipt", func(t *testing.T) {
		gr := &GoodsReceipt{
			DocumentNo: "GRN-001", VendorGSTIN: po.VendorGSTIN, PONumber: po.DocumentNo,
			ReceiptDate: time.Now(),
			Lines: []GoodsReceiptLine{
				{LineRef: "G1", POLineRef: "L1", ItemCode: "ITEM-A", ReceivedQty: 80},
				{LineRef: "G2", POLineRef: "L2", ItemCode: "ITEM-B", ReceivedQty: 50},
			},
		}
		ex := DetectMismatch(po, gr, sampleInvoice(), tol)
		if !hasType(ex, MismatchPartialReceipt) {
			t.Errorf("expected PARTIAL_RECEIPT exception, got %+v", ex)
		}
	})

	t.Run("quantity variance", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].Quantity = 120 // +20% over PO
		ex := DetectMismatch(po, nil, inv, tol)
		if !hasType(ex, MismatchQtyVariance) {
			t.Errorf("expected QTY_VARIANCE exception, got %+v", ex)
		}
	})

	t.Run("price variance", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].UnitRate = 13 // +30% over PO
		ex := DetectMismatch(po, nil, inv, tol)
		if !hasType(ex, MismatchPriceVariance) {
			t.Errorf("expected PRICE_VARIANCE exception, got %+v", ex)
		}
	})

	t.Run("tax variance", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].TaxRatePct = 28 // +10pp over PO
		ex := DetectMismatch(po, nil, inv, tol)
		if !hasType(ex, MismatchTaxVariance) {
			t.Errorf("expected TAX_VARIANCE exception, got %+v", ex)
		}
	})

	t.Run("vendor gstin mismatch flags high severity", func(t *testing.T) {
		inv := sampleInvoice()
		inv.VendorGSTIN = "27AAAAA9999B1Z2"
		ex := DetectMismatch(po, nil, inv, tol)
		if !hasType(ex, MismatchVendorMismatch) {
			t.Errorf("expected VENDOR_MISMATCH exception, got %+v", ex)
		}
		for _, e := range ex {
			if e.Type == MismatchVendorMismatch && e.Severity != SeverityHigh {
				t.Errorf("expected HIGH severity for GSTIN mismatch, got %s", e.Severity)
			}
		}
	})

	t.Run("duplicate item code across PO lines matched by po line ref", func(t *testing.T) {
		// PO has the same ItemCode on two lines at different rates.
		poDup := &PurchaseOrder{
			ID: "poD", TenantID: "t1", DocumentNo: "PO-DUP", VendorGSTIN: "29ABCDE1234F1Z5",
			Lines: []PurchaseOrderLine{
				{LineRef: "L1", ItemCode: "ITEM-A", Quantity: 100, UnitRate: 10, TaxRatePct: 18},
				{LineRef: "L2", ItemCode: "ITEM-A", Quantity: 50, UnitRate: 20, TaxRatePct: 12},
			},
		}
		// Invoice references each PO line explicitly via POLineRef.
		inv := &Invoice{
			ID: "invD", TenantID: "t1", DocumentNo: "INV-DUP", VendorGSTIN: "29ABCDE1234F1Z5",
			Lines: []InvoiceLine{
				{LineRef: "I1", POLineRef: "L1", ItemCode: "ITEM-A", Quantity: 100, UnitRate: 99, TaxRatePct: 18},
				{LineRef: "I2", POLineRef: "L2", ItemCode: "ITEM-A", Quantity: 50, UnitRate: 20, TaxRatePct: 12},
			},
		}
		ex := DetectMismatch(poDup, nil, inv, tol)
		// L1 price variance (10 -> 99) must be detected, not silently shadowed.
		if !hasType(ex, MismatchPriceVariance) {
			t.Errorf("expected PRICE_VARIANCE for L1, got %+v", ex)
		}
		for _, e := range ex {
			if e.Type == MismatchPriceVariance && e.POLineRef != "L1" {
				t.Errorf("price variance should reference L1, got %s", e.POLineRef)
			}
		}
	})

	t.Run("nil po returns empty", func(t *testing.T) {
		if ex := DetectMismatch(nil, nil, nil, tol); len(ex) != 0 {
			t.Errorf("expected no exceptions for nil PO, got %+v", ex)
		}
	})
}

func hasType(ex []ExceptionCase, mt MismatchType) bool {
	for _, e := range ex {
		if e.Type == mt {
			return true
		}
	}
	return false
}
