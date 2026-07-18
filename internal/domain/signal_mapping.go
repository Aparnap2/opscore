package domain

import (
	"errors"
	"strconv"
	"strings"
)

// OCRPayload is the domain-local, I/O-free representation of an OCR extraction
// result. It mirrors providers.OCRResult but lives in the domain package so the
// mapping layer stays free of the import cycle (providers already imports
// domain). Adapters in internal/adapters are responsible for translating
// providers.OCRResult -> OCRPayload before calling the mappers below.
type OCRPayload struct {
	KeyValues  map[string]string
	Text       string
	Tables     []OCRTable
	Confidence float64
}

// OCRTable is a single extracted table within an OCRPayload.
type OCRTable struct {
	Headers []string
	Rows    [][]string
}

// Header-name variant lists. Kept package-private so adapters/tests can reuse
// the same tolerant matching. Lookup is case-insensitive and ignores spaces/underscores.
var (
	poDocNoKeys  = []string{"po_no", "purchase_order_no", "order_no"}
	grnDocNoKeys = []string{"grn_no", "grn", "receipt_no"}
	grnPONoKeys  = []string{"po_no", "po_number", "purchase_order_no"}
	invDocNoKeys = []string{"invoice_no", "invoice_number"}
	invPONoKeys  = []string{"po_no", "po_number", "purchase_order_no"}

	gstinKeys = []string{"gstin", "vendor_gstin", "supplier_gstin"}

	lineNoKeys    = []string{"line_no", "line_ref", "sr_no", "sno", "sl"}
	itemCodeKeys  = []string{"item_code", "sku", "code", "item_code_sku"}
	descKeys      = []string{"description", "item_desc", "desc", "particulars", "item"}
	qtyKeys       = []string{"qty", "quantity", "received_qty", "qty_received"}
	rateKeys      = []string{"rate", "unit_rate", "unit_price", "price"}
	taxKeys       = []string{"tax%", "tax_rate", "tax_rate_pct", "tax", "gst%", "gst_rate"}
	poLineRefKeys = []string{"po_line", "po_line_ref", "po_line_no", "po_ref"}
)

// kv returns the first non-empty KeyValues match across the given candidate
// keys. Key matching is case-insensitive and ignores spaces and underscores so
// that "po_no", "PO_No", and "pono" all resolve to the same logical field.
func kv(ocr *OCRPayload, keys ...string) string {
	if ocr == nil || ocr.KeyValues == nil {
		return ""
	}
	for _, k := range keys {
		norm := normalizeKey(k)
		for ek, ev := range ocr.KeyValues {
			if normalizeKey(ek) == norm && strings.TrimSpace(ev) != "" {
				return strings.TrimSpace(ev)
			}
		}
	}
	return ""
}

// normalizeKey lowercases and strips spaces/underscores for tolerant matching.
func normalizeKey(k string) string {
	r := strings.NewReplacer(" ", "", "_", "", "-", "")
	return strings.ToLower(r.Replace(k))
}

// parseFloatField parses a numeric field, tolerating surrounding whitespace and
// thousands separators. Returns an error on an unparseable value.
func parseFloatField(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, errors.New("empty numeric field")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errors.New("invalid numeric value: " + s)
	}
	return v, nil
}

// deriveID builds a deterministic, I/O-free identifier from the tenant, job, and
// document type. The domain layer must not perform I/O, so we avoid uuid/random.
func deriveID(tenantID, jobID, docType string) string {
	return strings.Join([]string{docType, tenantID, jobID}, "_")
}

// columnIndex finds the index of the first header matching any of the candidate
// keys (case-insensitive, space/underscore-insensitive). Returns -1 if none.
func columnIndex(headers []string, keys ...string) int {
	for i, h := range headers {
		nh := normalizeKey(h)
		for _, k := range keys {
			if nh == normalizeKey(k) {
				return i
			}
		}
	}
	return -1
}

// cell returns the trimmed value at column idx for a row, or "" if out of range.
func cell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

// parseTable extracts line rows from the first TableData that contains a usable
// item-code column. Returns the matched headers' column indices and the rows.
func parseTable(ocr *OCRPayload, wantKeys ...[]string) (itemIdx, qtyIdx, rateIdx, taxIdx, poRefIdx, lineNoIdx, descIdx int, rows [][]string) {
	itemIdx, qtyIdx, rateIdx, taxIdx, poRefIdx, lineNoIdx, descIdx = -1, -1, -1, -1, -1, -1, -1
	if ocr == nil || len(ocr.Tables) == 0 {
		return
	}
	for _, tbl := range ocr.Tables {
		if len(tbl.Headers) == 0 || len(tbl.Rows) == 0 {
			continue
		}
		ii := columnIndex(tbl.Headers, itemCodeKeys...)
		if ii < 0 {
			continue
		}
		itemIdx = ii
		qtyIdx = columnIndex(tbl.Headers, qtyKeys...)
		rateIdx = columnIndex(tbl.Headers, rateKeys...)
		taxIdx = columnIndex(tbl.Headers, taxKeys...)
		poRefIdx = columnIndex(tbl.Headers, poLineRefKeys...)
		lineNoIdx = columnIndex(tbl.Headers, lineNoKeys...)
		descIdx = columnIndex(tbl.Headers, descKeys...)
		rows = tbl.Rows
		return
	}
	return
}

// MapOCRToPurchaseOrder maps an OCR payload into a deterministic PurchaseOrder.
// It performs no OCR/LLM calls and no validation beyond GSTIN format sanity.
func MapOCRToPurchaseOrder(ocr *OCRPayload, tenantID, jobID string) (*PurchaseOrder, error) {
	if ocr == nil {
		return nil, errors.New("purchase order: nil OCR result")
	}
	docNo := kv(ocr, poDocNoKeys...)
	if docNo == "" {
		return nil, errors.New("purchase order: document number not found in OCR payload")
	}
	gstin := kv(ocr, gstinKeys...)
	if gstin == "" {
		return nil, errors.New("purchase order: vendor GSTIN not found in OCR payload")
	}
	if !ValidateGST(gstin) {
		return nil, errors.New("purchase order: invalid vendor GSTIN format: " + gstin)
	}

	po := &PurchaseOrder{
		ID:          deriveID(tenantID, jobID, "po"),
		TenantID:    tenantID,
		DocumentNo:  docNo,
		VendorGSTIN: gstin,
	}

	lines, err := mapPOLines(ocr)
	if err != nil {
		return nil, err
	}
	po.Lines = lines
	return po, nil
}

func mapPOLines(ocr *OCRPayload) ([]PurchaseOrderLine, error) {
	itemIdx, qtyIdx, rateIdx, taxIdx, _, lineNoIdx, descIdx, rows := parseTable(ocr, itemCodeKeys)
	if itemIdx >= 0 && len(rows) > 0 {
		lines := make([]PurchaseOrderLine, 0, len(rows))
		for i, row := range rows {
			ref := cell(row, lineNoIdx)
			if ref == "" {
				ref = "L" + strconv.Itoa(i+1)
			}
			item := cell(row, itemIdx)
			qty, err := parseFloatField(cell(row, qtyIdx))
			if err != nil {
				return nil, errors.New("purchase order: line " + ref + ": " + err.Error())
			}
			rate, err := parseFloatField(cell(row, rateIdx))
			if err != nil {
				return nil, errors.New("purchase order: line " + ref + ": " + err.Error())
			}
			tax, _ := parseFloatField(cell(row, taxIdx)) // tax optional
			lines = append(lines, PurchaseOrderLine{
				LineRef:    ref,
				ItemCode:   item,
				ItemDesc:   cell(row, descIdx),
				Quantity:   qty,
				UnitRate:   rate,
				TaxRatePct: tax,
			})
		}
		return lines, nil
	}
	return mapPOLinesFromKV(ocr)
}

// mapPOLinesFromKV builds PO lines from flattened key/values (line_1_item, line_1_qty, ...).
func mapPOLinesFromKV(ocr *OCRPayload) ([]PurchaseOrderLine, error) {
	var lines []PurchaseOrderLine
	for n := 1; n <= 100; n++ {
		item := kv(ocr, "line_"+itoa(n)+"_item", "line_"+itoa(n)+"_item_code", "line_"+itoa(n)+"_sku")
		if item == "" {
			// stop at first missing line item
			if n > 1 {
				break
			}
			continue
		}
		ref := "L" + itoa(n)
		qty, err := parseFloatField(kv(ocr, "line_"+itoa(n)+"_qty", "line_"+itoa(n)+"_quantity"))
		if err != nil {
			return nil, errors.New("purchase order: line " + ref + ": " + err.Error())
		}
		rate, err := parseFloatField(kv(ocr, "line_"+itoa(n)+"_rate", "line_"+itoa(n)+"_unit_rate"))
		if err != nil {
			return nil, errors.New("purchase order: line " + ref + ": " + err.Error())
		}
		tax, _ := parseFloatField(kv(ocr, "line_"+itoa(n)+"_tax", "line_"+itoa(n)+"_tax_rate"))
		lines = append(lines, PurchaseOrderLine{
			LineRef:    ref,
			ItemCode:   item,
			ItemDesc:   kv(ocr, "line_"+itoa(n)+"_desc", "line_"+itoa(n)+"_description"),
			Quantity:   qty,
			UnitRate:   rate,
			TaxRatePct: tax,
		})
	}
	if len(lines) == 0 {
		return nil, errors.New("purchase order: no line items found in OCR payload")
	}
	return lines, nil
}

// MapOCRToGoodsReceipt maps an OCR payload into a deterministic GoodsReceipt.
func MapOCRToGoodsReceipt(ocr *OCRPayload, tenantID, jobID string) (*GoodsReceipt, error) {
	if ocr == nil {
		return nil, errors.New("goods receipt: nil OCR result")
	}
	docNo := kv(ocr, grnDocNoKeys...)
	if docNo == "" {
		return nil, errors.New("goods receipt: document number not found in OCR payload")
	}
	poNo := kv(ocr, grnPONoKeys...)
	if poNo == "" {
		return nil, errors.New("goods receipt: PO number not found in OCR payload")
	}
	gstin := kv(ocr, gstinKeys...)
	if gstin == "" {
		return nil, errors.New("goods receipt: vendor GSTIN not found in OCR payload")
	}
	if !ValidateGST(gstin) {
		return nil, errors.New("goods receipt: invalid vendor GSTIN format: " + gstin)
	}

	gr := &GoodsReceipt{
		ID:          deriveID(tenantID, jobID, "grn"),
		TenantID:    tenantID,
		DocumentNo:  docNo,
		PONumber:    poNo,
		VendorGSTIN: gstin,
	}

	lines, err := mapGRLines(ocr)
	if err != nil {
		return nil, err
	}
	gr.Lines = lines
	return gr, nil
}

func mapGRLines(ocr *OCRPayload) ([]GoodsReceiptLine, error) {
	itemIdx, qtyIdx, _, _, poRefIdx, lineNoIdx, _, rows := parseTable(ocr, itemCodeKeys)
	if itemIdx >= 0 && len(rows) > 0 {
		lines := make([]GoodsReceiptLine, 0, len(rows))
		for i, row := range rows {
			ref := cell(row, lineNoIdx)
			if ref == "" {
				ref = "G" + strconv.Itoa(i+1)
			}
			poRef := cell(row, poRefIdx)
			if poRef == "" {
				poRef = "L" + strconv.Itoa(i+1)
			}
			item := cell(row, itemIdx)
			qty, err := parseFloatField(cell(row, qtyIdx))
			if err != nil {
				return nil, errors.New("goods receipt: line " + ref + ": " + err.Error())
			}
			lines = append(lines, GoodsReceiptLine{
				LineRef:     ref,
				POLineRef:   poRef,
				ItemCode:    item,
				ReceivedQty: qty,
			})
		}
		return lines, nil
	}
	return mapGRLinesFromKV(ocr)
}

func mapGRLinesFromKV(ocr *OCRPayload) ([]GoodsReceiptLine, error) {
	var lines []GoodsReceiptLine
	for n := 1; n <= 100; n++ {
		item := kv(ocr, "line_"+itoa(n)+"_item", "line_"+itoa(n)+"_item_code", "line_"+itoa(n)+"_sku")
		if item == "" {
			if n > 1 {
				break
			}
			continue
		}
		ref := "G" + itoa(n)
		poRef := kv(ocr, "line_"+itoa(n)+"_po_line", "line_"+itoa(n)+"_po_line_ref")
		if poRef == "" {
			poRef = "L" + itoa(n)
		}
		qty, err := parseFloatField(kv(ocr, "line_"+itoa(n)+"_qty", "line_"+itoa(n)+"_received_qty", "line_"+itoa(n)+"_quantity"))
		if err != nil {
			return nil, errors.New("goods receipt: line " + ref + ": " + err.Error())
		}
		lines = append(lines, GoodsReceiptLine{
			LineRef:     ref,
			POLineRef:   poRef,
			ItemCode:    item,
			ReceivedQty: qty,
		})
	}
	if len(lines) == 0 {
		return nil, errors.New("goods receipt: no line items found in OCR payload")
	}
	return lines, nil
}

// MapOCRToInvoice maps an OCR payload into a deterministic Invoice.
func MapOCRToInvoice(ocr *OCRPayload, tenantID, jobID string) (*Invoice, error) {
	if ocr == nil {
		return nil, errors.New("invoice: nil OCR result")
	}
	docNo := kv(ocr, invDocNoKeys...)
	if docNo == "" {
		return nil, errors.New("invoice: document number not found in OCR payload")
	}
	gstin := kv(ocr, gstinKeys...)
	if gstin == "" {
		return nil, errors.New("invoice: vendor GSTIN not found in OCR payload")
	}
	if !ValidateGST(gstin) {
		return nil, errors.New("invoice: invalid vendor GSTIN format: " + gstin)
	}

	inv := &Invoice{
		ID:          deriveID(tenantID, jobID, "inv"),
		TenantID:    tenantID,
		DocumentNo:  docNo,
		VendorGSTIN: gstin,
		PONumber:    kv(ocr, invPONoKeys...), // optional
	}

	lines, err := mapInvLines(ocr)
	if err != nil {
		return nil, err
	}
	inv.Lines = lines
	return inv, nil
}

func mapInvLines(ocr *OCRPayload) ([]InvoiceLine, error) {
	itemIdx, qtyIdx, rateIdx, taxIdx, poRefIdx, lineNoIdx, _, rows := parseTable(ocr, itemCodeKeys)
	if itemIdx >= 0 && len(rows) > 0 {
		lines := make([]InvoiceLine, 0, len(rows))
		for i, row := range rows {
			ref := cell(row, lineNoIdx)
			if ref == "" {
				ref = "I" + strconv.Itoa(i+1)
			}
			poRef := cell(row, poRefIdx) // optional
			item := cell(row, itemIdx)
			qty, err := parseFloatField(cell(row, qtyIdx))
			if err != nil {
				return nil, errors.New("invoice: line " + ref + ": " + err.Error())
			}
			rate, err := parseFloatField(cell(row, rateIdx))
			if err != nil {
				return nil, errors.New("invoice: line " + ref + ": " + err.Error())
			}
			tax, _ := parseFloatField(cell(row, taxIdx))
			lines = append(lines, InvoiceLine{
				LineRef:    ref,
				POLineRef:  poRef,
				ItemCode:   item,
				Quantity:   qty,
				UnitRate:   rate,
				TaxRatePct: tax,
			})
		}
		return lines, nil
	}
	return mapInvLinesFromKV(ocr)
}

func mapInvLinesFromKV(ocr *OCRPayload) ([]InvoiceLine, error) {
	var lines []InvoiceLine
	for n := 1; n <= 100; n++ {
		item := kv(ocr, "line_"+itoa(n)+"_item", "line_"+itoa(n)+"_item_code", "line_"+itoa(n)+"_sku")
		if item == "" {
			if n > 1 {
				break
			}
			continue
		}
		ref := "I" + itoa(n)
		poRef := kv(ocr, "line_"+itoa(n)+"_po_line", "line_"+itoa(n)+"_po_line_ref") // optional
		qty, err := parseFloatField(kv(ocr, "line_"+itoa(n)+"_qty", "line_"+itoa(n)+"_quantity"))
		if err != nil {
			return nil, errors.New("invoice: line " + ref + ": " + err.Error())
		}
		rate, err := parseFloatField(kv(ocr, "line_"+itoa(n)+"_rate", "line_"+itoa(n)+"_unit_rate"))
		if err != nil {
			return nil, errors.New("invoice: line " + ref + ": " + err.Error())
		}
		tax, _ := parseFloatField(kv(ocr, "line_"+itoa(n)+"_tax", "line_"+itoa(n)+"_tax_rate"))
		lines = append(lines, InvoiceLine{
			LineRef:    ref,
			POLineRef:  poRef,
			ItemCode:   item,
			Quantity:   qty,
			UnitRate:   rate,
			TaxRatePct: tax,
		})
	}
	if len(lines) == 0 {
		return nil, errors.New("invoice: no line items found in OCR payload")
	}
	return lines, nil
}

// itoa is a tiny local helper to avoid importing strconv in call sites repeatedly.
func itoa(n int) string {
	return strconv.Itoa(n)
}
