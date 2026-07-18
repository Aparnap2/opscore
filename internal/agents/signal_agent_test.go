package agents

import (
	"context"
	"testing"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/telemetry"
)

func newTestSignalAgent(ocr *mockOCR) *SignalAgent {
	doc := NewDocumentAgent(nil, nil, nil, ocr, domain.NewIndiaValidator(), telemetry.NoopTracer{})
	return NewSignalAgent(doc)
}

func TestProcessSignal_PurchaseOrderMapsClean(t *testing.T) {
	ocr := newMockOCR(0.95, map[string]string{
		"po_no":    "PO-100",
		"gstin":    "29ABCDE1234F1Z5",
		"order_no": "PO-100",
	})
	// Provide a line via flattened key/values.
	ocr.keyValues["line_1_item"] = "ITEM-A"
	ocr.keyValues["line_1_qty"] = "100"
	ocr.keyValues["line_1_rate"] = "10"
	ocr.keyValues["line_1_tax"] = "18"

	agent := newTestSignalAgent(ocr)
	job := &SignalJob{TenantID: "t1", JobID: "j1", BlobURL: "x", FileName: "purchase_order.pdf"}
	res, err := agent.ProcessSignal(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("ProcessSignal error: %v", err)
	}
	if res.DocumentType != string(domain.DocumentTypePurchaseOrder) {
		t.Errorf("doc type = %s, want purchase_order", res.DocumentType)
	}
	if res.NeedsHITL {
		t.Errorf("expected no HITL for clean high-confidence PO, got reason: %s", res.HITLReason)
	}
	po, ok := res.Mapped.(*domain.PurchaseOrder)
	if !ok {
		t.Fatalf("expected mapped PurchaseOrder, got %T", res.Mapped)
	}
	if err := po.Validate(); err != nil {
		t.Errorf("mapped PO failed validation: %v", err)
	}
}

func TestProcessSignal_MissingGSTINTriggersHITL(t *testing.T) {
	ocr := newMockOCR(0.95, map[string]string{
		"po_no":       "PO-100",
		"line_1_item": "ITEM-A",
		"line_1_qty":  "100",
		"line_1_rate": "10",
		"line_1_tax":  "18",
	})
	agent := newTestSignalAgent(ocr)
	job := &SignalJob{TenantID: "t1", JobID: "j2", BlobURL: "x", FileName: "purchase_order.pdf"}
	res, err := agent.ProcessSignal(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("ProcessSignal error: %v", err)
	}
	if !res.NeedsHITL {
		t.Error("expected HITL when GSTIN missing/invalid")
	}
}

func TestProcessSignal_LowConfidenceTriggersHITL(t *testing.T) {
	ocr := newMockOCR(0.50, map[string]string{
		"po_no":       "PO-100",
		"gstin":       "29ABCDE1234F1Z5",
		"line_1_item": "ITEM-A",
		"line_1_qty":  "100",
		"line_1_rate": "10",
		"line_1_tax":  "18",
	})
	agent := newTestSignalAgent(ocr)
	job := &SignalJob{TenantID: "t1", JobID: "j3", BlobURL: "x", FileName: "purchase_order.pdf"}
	res, err := agent.ProcessSignal(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("ProcessSignal error: %v", err)
	}
	if !res.NeedsHITL || res.HITLReason == "" {
		t.Error("expected HITL for low OCR confidence")
	}
}

func TestProcessSignal_MismatchDetection(t *testing.T) {
	ocr := newMockOCR(0.95, map[string]string{
		"invoice_no":     "INV-1",
		"gstin":          "29ABCDE1234F1Z5",
		"po_no":          "PO-1",
		"line_1_item":    "ITEM-A",
		"line_1_qty":     "120", // +20% over PO
		"line_1_rate":    "10",
		"line_1_tax":     "18",
		"line_1_po_line": "L1",
	})
	po := &domain.PurchaseOrder{
		ID: "po", TenantID: "t1", DocumentNo: "PO-1", VendorGSTIN: "29ABCDE1234F1Z5",
		Lines: []domain.PurchaseOrderLine{{LineRef: "L1", ItemCode: "ITEM-A", Quantity: 100, UnitRate: 10, TaxRatePct: 18}},
	}
	agent := newTestSignalAgent(ocr)
	job := &SignalJob{TenantID: "t1", JobID: "j4", BlobURL: "x", FileName: "invoice.pdf"}
	res, err := agent.ProcessSignal(context.Background(), job, &SignalRelated{PO: po})
	if err != nil {
		t.Fatalf("ProcessSignal error: %v", err)
	}
	if !hasSignalException(res.Exceptions, domain.MismatchQtyVariance) {
		t.Errorf("expected QTY_VARIANCE exception, got %+v", res.Exceptions)
	}
	if !res.NeedsHITL {
		t.Error("expected HITL for payment-affecting mismatch")
	}
}

func hasSignalException(exs []domain.ExceptionCase, mt domain.MismatchType) bool {
	for _, e := range exs {
		if e.Type == mt {
			return true
		}
	}
	return false
}
