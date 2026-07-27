package domain

import (
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantType DocumentType
	}{
		{
			name:     "invoice with keywords",
			text:     "INVOICE Tax Invoice Bill No: INV/2024/001 Proforma for services",
			wantType: DocumentTypeInvoice,
		},
		{
			name:     "invoice with gst invoice",
			text:     "GST INVOICE Bill To: Test Company Proforma Invoice Number: INV/2024/001",
			wantType: DocumentTypeInvoice,
		},
		{
			name:     "purchase order",
			text:     "PURCHASE ORDER Order No: PO/2024/001 Procurement Order for Q4 2024",
			wantType: DocumentTypePurchaseOrder,
		},
		{
			name:     "GST notice",
			text:     "GST NOTICE DRC-01 ARN: AR123456789012 Assessment Period: FY 2023-24 Demand Notice for Recovery",
			wantType: DocumentTypeGSTNotice,
		},
		{
			name:     "contract agreement",
			text:     "AGREEMENT This Agreement executed on Terms and Conditions Whereas the parties hereby agree",
			wantType: DocumentTypeContract,
		},
		{
			name:     "contract with witness clause",
			text:     "CONTRACT In Witness Wherefore the parties have executed this agreement",
			wantType: DocumentTypeContract,
		},
		{
			name:     "empty text",
			text:     "",
			wantType: DocumentTypeUnknown,
		},
		{
			name:     "random text",
			text:     "This is just some random text without any specific keywords",
			wantType: DocumentTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(tt.text)
			if result.Type != tt.wantType {
				t.Errorf("Classify(%q).Type = %v, want %v", tt.name, result.Type, tt.wantType)
			}
		})
	}
}

func TestClassify_GoodsReceipt(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantType DocumentType
	}{
		{
			name:     "goods receipt note",
			text:     "GOODS RECEIPT NOTE GR No: GRN/2024/001 Receipt Note for materials received",
			wantType: DocumentTypeGoodsReceipt,
		},
		{
			name:     "grn detection",
			text:     "GRN No: GRN/2024/001 Receipt Note for goods received into store against PO/2024/001",
			wantType: DocumentTypeGoodsReceipt,
		},
		{
			name:     "delivery challan",
			text:     "DELIVERY CHALLAN Challan No: DC/2024/001 Receipt Note for goods delivered",
			wantType: DocumentTypeGoodsReceipt,
		},
		{
			name:     "invoice regression",
			text:     "INVOICE Tax Invoice Bill No: INV/2024/001 Proforma for services",
			wantType: DocumentTypeInvoice,
		},
		{
			name:     "purchase order regression",
			text:     "PURCHASE ORDER Order No: PO/2024/001 Procurement Order for Q4 2024",
			wantType: DocumentTypePurchaseOrder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(tt.text)
			if result.Type != tt.wantType {
				t.Errorf("Classify(%q).Type = %v, want %v", tt.name, result.Type, tt.wantType)
			}
		})
	}
}

func TestClassify_Confidence(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantMinConf float64
	}{
		{
			name:        "high confidence invoice",
			text:        "tax invoice gstin cgst sgst igst invoice number bill to ship to",
			wantMinConf: 20, // 10 keywords found / 10 total = 100%, but normalized to 100
		},
		{
			name:        "low confidence unknown",
			text:        "random words",
			wantMinConf: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(tt.text)
			if result.Confidence < tt.wantMinConf {
				t.Errorf("Classify(%q).Confidence = %v, want >= %v", tt.name, result.Confidence, tt.wantMinConf)
			}
		})
	}
}

func TestValidateGSTNumber(t *testing.T) {
	tests := []struct {
		name  string
		gst   string
		valid bool
	}{
		{"valid GST", "27AABCS1209D1Z5", true},
		{"valid GST 2", "07AABCS1209D1Z5", true},
		{"invalid GST", "INVALID", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateGSTNumber(tt.gst)
			if result != tt.valid {
				t.Errorf("ValidateGSTNumber(%q) = %v, want %v", tt.gst, result, tt.valid)
			}
		})
	}
}

func TestValidatePANNumber(t *testing.T) {
	tests := []struct {
		name  string
		pan   string
		valid bool
	}{
		{"valid PAN", "AABCS1209D", true},
		{"valid PAN 2", "XYZAB1234K", true},
		{"invalid PAN", "INVALID", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePANNumber(tt.pan)
			if result != tt.valid {
				t.Errorf("ValidatePANNumber(%q) = %v, want %v", tt.pan, result, tt.valid)
			}
		})
	}
}

func TestValidateIFSCCode(t *testing.T) {
	tests := []struct {
		name  string
		ifsc  string
		valid bool
	}{
		{"valid IFSC", "HDFC0CGBIBL", true},
		{"valid IFSC 2", "SBIN0002655", true},
		{"invalid IFSC", "INVALID", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateIFSCCode(tt.ifsc)
			if result != tt.valid {
				t.Errorf("ValidateIFSCCode(%q) = %v, want %v", tt.ifsc, result, tt.valid)
			}
		})
	}
}
