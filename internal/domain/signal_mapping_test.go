package domain

import (
	"testing"
)

// --- OCRPayload test helpers ---

func poOCRResult() *OCRPayload {
	return &OCRPayload{
		KeyValues: map[string]string{
			"po_no":        "PO-2026-001",
			"vendor_gstin": "29ABCDE1234F1Z5",
			"order_date":   "2026-07-10",
		},
		Tables: []OCRTable{
			{
				Headers: []string{"line_no", "item_code", "description", "qty", "rate", "tax%"},
				Rows: [][]string{
					{"1", "SKU-A", "Steel Rod", "100", "12.50", "18"},
					{"2", "SKU-B", "Bolt", "200", "2.00", "12"},
				},
			},
		},
	}
}

func grnOCRResult() *OCRPayload {
	return &OCRPayload{
		KeyValues: map[string]string{
			"po_no":        "PO-2026-001",
			"grn_no":       "GRN-2026-001",
			"vendor_gstin": "29ABCDE1234F1Z5",
			"receipt_date": "2026-07-12",
		},
		Tables: []OCRTable{
			{
				Headers: []string{"po_line", "item_code", "received_qty"},
				Rows: [][]string{
					{"L1", "SKU-A", "100"},
					{"L2", "SKU-B", "180"},
				},
			},
		},
	}
}

func invoiceOCRResult() *OCRPayload {
	return &OCRPayload{
		KeyValues: map[string]string{
			"invoice_no":   "INV-2026-001",
			"vendor_gstin": "29ABCDE1234F1Z5",
			"po_no":        "PO-2026-001",
			"invoice_date": "2026-07-15",
		},
		Tables: []OCRTable{
			{
				Headers: []string{"po_line_ref", "item_code", "qty", "unit_rate", "tax_rate"},
				Rows: [][]string{
					{"L1", "SKU-A", "100", "12.50", "18"},
					{"L2", "SKU-B", "200", "2.00", "12"},
				},
			},
		},
	}
}

// --- MapOCRToPurchaseOrder ---

func TestMapOCRToPurchaseOrder_HappyPath(t *testing.T) {
	ocr := poOCRResult()
	po, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if po.DocumentNo != "PO-2026-001" {
		t.Errorf("DocumentNo = %q, want %q", po.DocumentNo, "PO-2026-001")
	}
	if po.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want %q", po.VendorGSTIN, "29ABCDE1234F1Z5")
	}
	if po.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", po.TenantID, "tenant-1")
	}
	if len(po.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(po.Lines))
	}
	if po.Lines[0].LineRef != "1" {
		t.Errorf("Lines[0].LineRef = %q, want %q", po.Lines[0].LineRef, "1")
	}
	if po.Lines[0].ItemCode != "SKU-A" {
		t.Errorf("Lines[0].ItemCode = %q, want %q", po.Lines[0].ItemCode, "SKU-A")
	}
	if po.Lines[0].Quantity != 100 {
		t.Errorf("Lines[0].Quantity = %v, want 100", po.Lines[0].Quantity)
	}
	if po.Lines[0].UnitRate != 12.50 {
		t.Errorf("Lines[0].UnitRate = %v, want 12.50", po.Lines[0].UnitRate)
	}
	if po.Lines[0].TaxRatePct != 18 {
		t.Errorf("Lines[0].TaxRatePct = %v, want 18", po.Lines[0].TaxRatePct)
	}
	if po.Lines[1].LineRef != "2" || po.Lines[1].ItemCode != "SKU-B" {
		t.Errorf("Lines[1] = %+v, want ref 2 code SKU-B", po.Lines[1])
	}
	if err := po.Validate(); err != nil {
		t.Errorf("po.Validate() = %v, want nil", err)
	}
}

func TestMapOCRToPurchaseOrder_MissingGSTIN(t *testing.T) {
	ocr := poOCRResult()
	delete(ocr.KeyValues, "vendor_gstin")
	if _, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-1"); err == nil {
		t.Fatal("expected error for missing GSTIN, got nil")
	}
}

func TestMapOCRToPurchaseOrder_InvalidGSTIN(t *testing.T) {
	ocr := poOCRResult()
	ocr.KeyValues["vendor_gstin"] = "INVALIDGST"
	if _, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-1"); err == nil {
		t.Fatal("expected error for invalid GSTIN, got nil")
	}
}

func TestMapOCRToPurchaseOrder_MissingDocNo(t *testing.T) {
	ocr := poOCRResult()
	delete(ocr.KeyValues, "po_no")
	_, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-1")
	if err == nil {
		t.Fatal("expected error for missing document number, got nil")
	}
}

func TestMapOCRToPurchaseOrder_NumericParseFailure(t *testing.T) {
	ocr := poOCRResult()
	ocr.Tables[0].Rows[0][3] = "not-a-number" // rate column
	if _, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-1"); err == nil {
		t.Fatal("expected error for bad numeric, got nil")
	}
}

func TestMapOCRToPurchaseOrder_FlatKeyValues(t *testing.T) {
	ocr := &OCRPayload{
		KeyValues: map[string]string{
			"purchase_order_no": "PO-FLAT-1",
			"supplier_gstin":    "29ABCDE1234F1Z5",
			"line_1_item":       "SKU-X",
			"line_1_qty":        "10",
			"line_1_rate":       "5.00",
			"line_1_tax":        "5",
			"line_2_item":       "SKU-Y",
			"line_2_qty":        "20",
			"line_2_rate":       "3.00",
			"line_2_tax":        "5",
		},
	}
	po, err := MapOCRToPurchaseOrder(ocr, "tenant-1", "job-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(po.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(po.Lines))
	}
	if po.Lines[0].ItemCode != "SKU-X" || po.Lines[0].Quantity != 10 {
		t.Errorf("Lines[0] = %+v", po.Lines[0])
	}
	if po.Lines[1].ItemCode != "SKU-Y" || po.Lines[1].Quantity != 20 {
		t.Errorf("Lines[1] = %+v", po.Lines[1])
	}
	if err := po.Validate(); err != nil {
		t.Errorf("po.Validate() = %v, want nil", err)
	}
}

// --- MapOCRToGoodsReceipt ---

func TestMapOCRToGoodsReceipt_HappyPath(t *testing.T) {
	ocr := grnOCRResult()
	gr, err := MapOCRToGoodsReceipt(ocr, "tenant-1", "job-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.DocumentNo != "GRN-2026-001" {
		t.Errorf("DocumentNo = %q, want %q", gr.DocumentNo, "GRN-2026-001")
	}
	if gr.PONumber != "PO-2026-001" {
		t.Errorf("PONumber = %q, want %q", gr.PONumber, "PO-2026-001")
	}
	if gr.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want %q", gr.VendorGSTIN, "29ABCDE1234F1Z5")
	}
	if len(gr.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(gr.Lines))
	}
	if gr.Lines[0].POLineRef != "L1" || gr.Lines[0].ItemCode != "SKU-A" {
		t.Errorf("Lines[0] = %+v", gr.Lines[0])
	}
	if gr.Lines[0].ReceivedQty != 100 {
		t.Errorf("Lines[0].ReceivedQty = %v, want 100", gr.Lines[0].ReceivedQty)
	}
	if gr.Lines[1].POLineRef != "L2" || gr.Lines[1].ReceivedQty != 180 {
		t.Errorf("Lines[1] = %+v", gr.Lines[1])
	}
	if err := gr.Validate(); err != nil {
		t.Errorf("gr.Validate() = %v, want nil", err)
	}
}

func TestMapOCRToGoodsReceipt_MissingGSTIN(t *testing.T) {
	ocr := grnOCRResult()
	delete(ocr.KeyValues, "vendor_gstin")
	if _, err := MapOCRToGoodsReceipt(ocr, "tenant-1", "job-3"); err == nil {
		t.Fatal("expected error for missing GSTIN, got nil")
	}
}

func TestMapOCRToGoodsReceipt_MissingDocNo(t *testing.T) {
	ocr := grnOCRResult()
	delete(ocr.KeyValues, "grn_no")
	if _, err := MapOCRToGoodsReceipt(ocr, "tenant-1", "job-3"); err == nil {
		t.Fatal("expected error for missing document number, got nil")
	}
}

func TestMapOCRToGoodsReceipt_MissingPONumber(t *testing.T) {
	ocr := grnOCRResult()
	delete(ocr.KeyValues, "po_no")
	if _, err := MapOCRToGoodsReceipt(ocr, "tenant-1", "job-3"); err == nil {
		t.Fatal("expected error for missing PO number, got nil")
	}
}

func TestMapOCRToGoodsReceipt_NumericParseFailure(t *testing.T) {
	ocr := grnOCRResult()
	ocr.Tables[0].Rows[0][2] = "abc" // received_qty column
	if _, err := MapOCRToGoodsReceipt(ocr, "tenant-1", "job-3"); err == nil {
		t.Fatal("expected error for bad numeric, got nil")
	}
}

// --- MapOCRToInvoice ---

func TestMapOCRToInvoice_HappyPath(t *testing.T) {
	ocr := invoiceOCRResult()
	inv, err := MapOCRToInvoice(ocr, "tenant-1", "job-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.DocumentNo != "INV-2026-001" {
		t.Errorf("DocumentNo = %q, want %q", inv.DocumentNo, "INV-2026-001")
	}
	if inv.VendorGSTIN != "29ABCDE1234F1Z5" {
		t.Errorf("VendorGSTIN = %q, want %q", inv.VendorGSTIN, "29ABCDE1234F1Z5")
	}
	if inv.PONumber != "PO-2026-001" {
		t.Errorf("PONumber = %q, want %q", inv.PONumber, "PO-2026-001")
	}
	if len(inv.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(inv.Lines))
	}
	if inv.Lines[0].POLineRef != "L1" || inv.Lines[0].ItemCode != "SKU-A" {
		t.Errorf("Lines[0] = %+v", inv.Lines[0])
	}
	if inv.Lines[0].Quantity != 100 || inv.Lines[0].UnitRate != 12.50 || inv.Lines[0].TaxRatePct != 18 {
		t.Errorf("Lines[0] = %+v", inv.Lines[0])
	}
	if inv.Lines[1].POLineRef != "L2" || inv.Lines[1].Quantity != 200 {
		t.Errorf("Lines[1] = %+v", inv.Lines[1])
	}
	if err := inv.Validate(); err != nil {
		t.Errorf("inv.Validate() = %v, want nil", err)
	}
}

func TestMapOCRToInvoice_MissingGSTIN(t *testing.T) {
	ocr := invoiceOCRResult()
	delete(ocr.KeyValues, "vendor_gstin")
	if _, err := MapOCRToInvoice(ocr, "tenant-1", "job-4"); err == nil {
		t.Fatal("expected error for missing GSTIN, got nil")
	}
}

func TestMapOCRToInvoice_MissingDocNo(t *testing.T) {
	ocr := invoiceOCRResult()
	delete(ocr.KeyValues, "invoice_no")
	if _, err := MapOCRToInvoice(ocr, "tenant-1", "job-4"); err == nil {
		t.Fatal("expected error for missing document number, got nil")
	}
}

func TestMapOCRToInvoice_OptionalPONumber(t *testing.T) {
	ocr := invoiceOCRResult()
	delete(ocr.KeyValues, "po_no")
	inv, err := MapOCRToInvoice(ocr, "tenant-1", "job-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.PONumber != "" {
		t.Errorf("PONumber = %q, want empty", inv.PONumber)
	}
	if err := inv.Validate(); err != nil {
		t.Errorf("inv.Validate() = %v, want nil", err)
	}
}

func TestMapOCRToInvoice_NumericParseFailure(t *testing.T) {
	ocr := invoiceOCRResult()
	ocr.Tables[0].Rows[0][3] = "x" // unit_rate column
	if _, err := MapOCRToInvoice(ocr, "tenant-1", "job-4"); err == nil {
		t.Fatal("expected error for bad numeric, got nil")
	}
}

// --- shared helpers ---

func TestKVLookup(t *testing.T) {
	ocr := &OCRPayload{
		KeyValues: map[string]string{
			"PO_No":    "X1",
			"gstin":    "Y1",
			"Line_1_Q": "5",
		},
	}
	if got := kv(ocr, "po_no", "purchase_order_no", "order_no"); got != "X1" {
		t.Errorf("kv(po_no) = %q, want %q", got, "X1")
	}
	if got := kv(ocr, "missing", "gstin"); got != "Y1" {
		t.Errorf("kv(gstin) = %q, want %q", got, "Y1")
	}
	if got := kv(ocr, "nope"); got != "" {
		t.Errorf("kv(nope) = %q, want empty", got)
	}
}

func TestParseFloatField(t *testing.T) {
	if v, err := parseFloatField("12.5"); err != nil || v != 12.5 {
		t.Errorf("parseFloatField(12.5) = %v, %v", v, err)
	}
	if _, err := parseFloatField("bad"); err == nil {
		t.Error("expected error for bad float")
	}
}
