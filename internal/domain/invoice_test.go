package domain

import (
	"testing"
	"time"
)

func sampleInvoice() *Invoice {
	return &Invoice{
		ID:          "inv1",
		TenantID:    "t1",
		DocumentNo:  "INV-001",
		VendorGSTIN: "29ABCDE1234F1Z5",
		PONumber:    "PO-001",
		InvoiceDate: time.Now(),
		Lines: []InvoiceLine{
			{LineRef: "I1", POLineRef: "L1", ItemCode: "ITEM-A", Quantity: 100, UnitRate: 10, TaxRatePct: 18},
			{LineRef: "I2", POLineRef: "L2", ItemCode: "ITEM-B", Quantity: 50, UnitRate: 20, TaxRatePct: 12},
		},
	}
}

func TestInvoiceValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := sampleInvoice().Validate(); err != nil {
			t.Errorf("expected valid invoice, got error: %v", err)
		}
	})
	t.Run("missing document number", func(t *testing.T) {
		inv := sampleInvoice()
		inv.DocumentNo = ""
		if err := inv.Validate(); err == nil {
			t.Error("expected error for missing document number")
		}
	})
	t.Run("invalid vendor gstin", func(t *testing.T) {
		inv := sampleInvoice()
		inv.VendorGSTIN = "NOPE"
		if err := inv.Validate(); err == nil {
			t.Error("expected error for invalid vendor GSTIN")
		}
	})
	t.Run("empty lines", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines = nil
		if err := inv.Validate(); err == nil {
			t.Error("expected error for empty line items")
		}
	})
	t.Run("duplicate line reference", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[1].LineRef = "I1"
		if err := inv.Validate(); err == nil {
			t.Error("expected error for duplicate line reference")
		}
	})
	t.Run("missing item code", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].ItemCode = ""
		if err := inv.Validate(); err == nil {
			t.Error("expected error for missing item code")
		}
	})
	t.Run("non-positive quantity", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].Quantity = 0
		if err := inv.Validate(); err == nil {
			t.Error("expected error for non-positive quantity")
		}
	})
	t.Run("negative unit rate", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].UnitRate = -1
		if err := inv.Validate(); err == nil {
			t.Error("expected error for negative unit rate")
		}
	})
	t.Run("negative tax", func(t *testing.T) {
		inv := sampleInvoice()
		inv.Lines[0].TaxRatePct = -5
		if err := inv.Validate(); err == nil {
			t.Error("expected error for negative tax rate")
		}
	})
}

func TestInvoiceTotal(t *testing.T) {
	inv := sampleInvoice()
	if got := inv.InvoiceTotal(); got != 2300.0 {
		t.Errorf("InvoiceTotal() = %.2f, want 2300.00", got)
	}
}

func TestInvoiceIsDuplicate(t *testing.T) {
	inv := sampleInvoice()
	existing := []Invoice{
		{DocumentNo: "INV-OTHER", VendorGSTIN: "29ABCDE1234F1Z5", Lines: []InvoiceLine{{ItemCode: "X", Quantity: 1, UnitRate: 1, TaxRatePct: 0}}},
		*inv,
	}
	t.Run("exact duplicate", func(t *testing.T) {
		if !inv.IsDuplicate(existing) {
			t.Error("expected IsDuplicate to return true for same doc no + gstin + total")
		}
	})
	t.Run("different document number", func(t *testing.T) {
		other := sampleInvoice()
		other.DocumentNo = "INV-999"
		if other.IsDuplicate(existing) {
			t.Error("expected IsDuplicate false when document number differs")
		}
	})
	t.Run("different gstin", func(t *testing.T) {
		other := sampleInvoice()
		other.VendorGSTIN = "27AAAAA9999B1Z2"
		if other.IsDuplicate(existing) {
			t.Error("expected IsDuplicate false when vendor GSTIN differs")
		}
	})
	t.Run("different total", func(t *testing.T) {
		other := sampleInvoice()
		other.Lines[0].Quantity = 200
		if other.IsDuplicate(existing) {
			t.Error("expected IsDuplicate false when total differs")
		}
	})
	t.Run("empty existing list", func(t *testing.T) {
		if inv.IsDuplicate(nil) {
			t.Error("expected IsDuplicate false against empty list")
		}
	})
}
