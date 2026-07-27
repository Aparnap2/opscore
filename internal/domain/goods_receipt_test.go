package domain

import (
	"testing"
	"time"
)

func sampleGRN() *GoodsReceipt {
	return &GoodsReceipt{
		ID: "grn1", TenantID: "t1", DocumentNo: "GRN-001",
		VendorGSTIN: "29ABCDE1234F1Z5", PONumber: "PO-001",
		ReceiptDate: time.Now(),
		Lines: []GoodsReceiptLine{
			{LineRef: "G1", POLineRef: "L1", ItemCode: "ITEM-A", ReceivedQty: 100},
			{LineRef: "G2", POLineRef: "L2", ItemCode: "ITEM-B", ReceivedQty: 50},
		},
	}
}

func TestGoodsReceiptValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := sampleGRN().Validate(); err != nil {
			t.Errorf("expected valid GRN, got error: %v", err)
		}
	})
	t.Run("missing document number", func(t *testing.T) {
		gr := sampleGRN()
		gr.DocumentNo = ""
		if err := gr.Validate(); err == nil {
			t.Error("expected error for missing document number")
		}
	})
	t.Run("missing po number", func(t *testing.T) {
		gr := sampleGRN()
		gr.PONumber = ""
		if err := gr.Validate(); err == nil {
			t.Error("expected error for missing PO number")
		}
	})
	t.Run("invalid vendor gstin", func(t *testing.T) {
		gr := sampleGRN()
		gr.VendorGSTIN = "BAD"
		if err := gr.Validate(); err == nil {
			t.Error("expected error for invalid vendor GSTIN")
		}
	})
	t.Run("empty lines", func(t *testing.T) {
		gr := sampleGRN()
		gr.Lines = nil
		if err := gr.Validate(); err == nil {
			t.Error("expected error for empty line items")
		}
	})
	t.Run("missing po line ref", func(t *testing.T) {
		gr := sampleGRN()
		gr.Lines[0].POLineRef = ""
		if err := gr.Validate(); err == nil {
			t.Error("expected error for missing PO line reference")
		}
	})
	t.Run("negative received qty", func(t *testing.T) {
		gr := sampleGRN()
		gr.Lines[0].ReceivedQty = -5
		if err := gr.Validate(); err == nil {
			t.Error("expected error for negative received quantity")
		}
	})
	t.Run("duplicate line reference", func(t *testing.T) {
		gr := sampleGRN()
		gr.Lines[1].LineRef = "G1"
		if err := gr.Validate(); err == nil {
			t.Error("expected error for duplicate line reference")
		}
	})
}

func TestReceivedQtyForPOLine(t *testing.T) {
	gr := sampleGRN()
	gr.Lines = append(gr.Lines, GoodsReceiptLine{LineRef: "G3", POLineRef: "L1", ItemCode: "ITEM-A", ReceivedQty: 20})
	if got := gr.ReceivedQtyForPOLine("L1"); got != 120 {
		t.Errorf("ReceivedQtyForPOLine(L1) = %.2f, want 120.00", got)
	}
	if got := gr.ReceivedQtyForPOLine("L2"); got != 50 {
		t.Errorf("ReceivedQtyForPOLine(L2) = %.2f, want 50.00", got)
	}
	if got := gr.ReceivedQtyForPOLine("LX"); got != 0 {
		t.Errorf("ReceivedQtyForPOLine(LX) = %.2f, want 0.00", got)
	}
}
