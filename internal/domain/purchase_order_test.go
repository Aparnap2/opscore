package domain

import (
	"testing"
	"time"
)

func samplePO() *PurchaseOrder {
	return &PurchaseOrder{
		ID:          "po1",
		TenantID:    "t1",
		DocumentNo:  "PO-001",
		VendorGSTIN: "29ABCDE1234F1Z5",
		OrderDate:   time.Now(),
		Lines: []PurchaseOrderLine{
			{LineRef: "L1", ItemCode: "ITEM-A", Quantity: 100, UnitRate: 10, TaxRatePct: 18},
			{LineRef: "L2", ItemCode: "ITEM-B", Quantity: 50, UnitRate: 20, TaxRatePct: 12},
		},
	}
}

func TestPurchaseOrderValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := samplePO().Validate(); err != nil {
			t.Errorf("expected valid PO, got error: %v", err)
		}
	})
	t.Run("missing document number", func(t *testing.T) {
		po := samplePO()
		po.DocumentNo = ""
		if err := po.Validate(); err == nil {
			t.Error("expected error for missing document number")
		}
	})
	t.Run("missing vendor gstin", func(t *testing.T) {
		po := samplePO()
		po.VendorGSTIN = ""
		if err := po.Validate(); err == nil {
			t.Error("expected error for missing vendor GSTIN")
		}
	})
	t.Run("invalid vendor gstin format", func(t *testing.T) {
		po := samplePO()
		po.VendorGSTIN = "BADGST"
		if err := po.Validate(); err == nil {
			t.Error("expected error for invalid vendor GSTIN format")
		}
	})
	t.Run("empty lines", func(t *testing.T) {
		po := samplePO()
		po.Lines = nil
		if err := po.Validate(); err == nil {
			t.Error("expected error for empty line items")
		}
	})
	t.Run("duplicate line reference", func(t *testing.T) {
		po := samplePO()
		po.Lines[1].LineRef = "L1"
		if err := po.Validate(); err == nil {
			t.Error("expected error for duplicate line reference")
		}
	})
	t.Run("non-positive quantity", func(t *testing.T) {
		po := samplePO()
		po.Lines[0].Quantity = 0
		if err := po.Validate(); err == nil {
			t.Error("expected error for non-positive quantity")
		}
	})
}

func TestComputePOTotal(t *testing.T) {
	po := samplePO()
	// L1: 100*10 + 18% = 1180 ; L2: 50*20 + 12% = 1120 ; total = 2300
	want := 2300.0
	if got := po.ComputePOTotal(); got != want {
		t.Errorf("ComputePOTotal() = %.2f, want %.2f", got, want)
	}
}
