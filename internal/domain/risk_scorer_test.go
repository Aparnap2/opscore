package domain

import (
	"testing"
)

func TestValidateGST(t *testing.T) {
	tests := []struct {
		name  string
		gst   string
		valid bool
	}{
		{"valid GST", "27AABCS1209D1Z5", true},
		{"valid GST 2", "07AABCS1209D1Z5", true},
		{"invalid - too short", "27AABCS1209D", false},
		{"invalid - contains lowercase", "27aabcs1209d1z5", false},
		{"invalid - wrong pattern", "ZZAABCS1209D1Z5", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateGST(tt.gst)
			if result != tt.valid {
				t.Errorf("ValidateGST(%q) = %v, want %v", tt.gst, result, tt.valid)
			}
		})
	}
}

func TestValidatePAN(t *testing.T) {
	tests := []struct {
		name  string
		pan   string
		valid bool
	}{
		{"valid PAN", "AABCS1209D", true},
		{"valid PAN 2", "AABCS1234A", true},
		{"invalid - too short", "AABCS120", false},
		{"invalid - contains digit in letter pos", "1234S1209D", false},
		{"invalid - contains lowercase", "aabcs1209d", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePAN(tt.pan)
			if result != tt.valid {
				t.Errorf("ValidatePAN(%q) = %v, want %v", tt.pan, result, tt.valid)
			}
		})
	}
}

func TestValidateIFSC(t *testing.T) {
	tests := []struct {
		name  string
		ifsc  string
		valid bool
	}{
		{"valid IFSC", "HDFC0CGBIBL", true},
		{"valid IFSC 2", "SBIN0002655", true},
		{"invalid - no zero", "HDFCABCDE1234", false},
		{"invalid - too short", "HDFC0AB", false},
		{"invalid - contains lowercase", "hdFc0CGBIBL", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateIFSC(tt.ifsc)
			if result != tt.valid {
				t.Errorf("ValidateIFSC(%q) = %v, want %v", tt.ifsc, result, tt.valid)
			}
		})
	}
}

func TestComputeVendorRisk(t *testing.T) {
	// PRD v3.0 Spec (Section 8):
	// - Base score: 50
	// - Valid GST: +15
	// - Valid PAN: +10
	// - Valid IFSC: +10
	// - Duplicate GST in system: -40
	// - Active dispute flag: -20 (using !Approved)
	// - Incomplete documents: -15 (no PAN or no GST)
	// - Clamp between 0-100
	// - Tier: >=70 = LOW, >=40 = MEDIUM, <40 = HIGH
	tests := []struct {
		name         string
		vendor       *Vendor
		existing     []*Vendor
		blacklist    []string
		tenantID     string
		wantScore    int
		wantTier     RiskTier
		wantMinFlags int
	}{
		{
			name: "clean vendor - all valid credentials",
			vendor: &Vendor{
				ID:          "v1",
				Name:        "Clean Corp",
				GSTNumber:   "27AABCS1209D1Z5",
				PANNumber:   "AABCS1209D",
				IFSCCode:    "HDFC0CGBIBL",
				BankAccount: "1234567890",
				TenantID:    "t1",
				Approved:    true, // No dispute flag
			},
			existing:     []*Vendor{},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    85,          // 50 + 15 (GST) + 10 (PAN) + 10 (IFSC)
			wantTier:     RiskTierLow, // >= 70
			wantMinFlags: 3,           // VALID_GST, VALID_PAN, VALID_IFSC
		},
		{
			name: "invalid GST - incomplete docs",
			vendor: &Vendor{
				ID:        "v2",
				Name:      "Bad GST Corp",
				GSTNumber: "INVALID",
				TenantID:  "t1",
				Approved:  true,
			},
			existing:     []*Vendor{},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    35,           // 50 - 15 (incomplete: no PAN or no GST)
			wantTier:     RiskTierHigh, // < 40
			wantMinFlags: 1,            // INCOMPLETE_DOCUMENTS
		},
		{
			name: "invalid PAN and IFSC - no GST",
			vendor: &Vendor{
				ID:        "v3",
				Name:      "Multiple Errors",
				PANNumber: "BADPAN",
				IFSCCode:  "1234",
				TenantID:  "t1",
				Approved:  true,
			},
			existing:     []*Vendor{},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    35,           // 50 - 15 (incomplete: no PAN or no GST)
			wantTier:     RiskTierHigh, // < 40
			wantMinFlags: 1,            // INCOMPLETE_DOCUMENTS
		},
		{
			name: "duplicate GST in system",
			vendor: &Vendor{
				ID:        "v4",
				Name:      "Duplicate GST Corp",
				GSTNumber: "27AABCS1209D1Z5",
				TenantID:  "t1",
				Approved:  true, // No dispute flag
			},
			existing: []*Vendor{
				{
					ID:        "existing1",
					Name:      "Existing Corp",
					GSTNumber: "27AABCS1209D1Z5", // Same GST
					TenantID:  "t1",
				},
			},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    25,           // 50 + 15 (valid GST) - 40 (duplicate GST)
			wantTier:     RiskTierHigh, // < 40
			wantMinFlags: 2,            // VALID_GST, DUPLICATE_GST
		},
		{
			name: "vendor not approved (dispute flag)",
			vendor: &Vendor{
				ID:        "v5",
				Name:      "Disputed Vendor",
				GSTNumber: "27AABCS1209D1Z5",
				PANNumber: "AABCS1209D",
				TenantID:  "t1",
				Approved:  false, // Not approved = dispute flag
			},
			existing:     []*Vendor{},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    55,             // 50 + 15 (GST) + 10 (PAN) - 20 (not approved)
			wantTier:     RiskTierMedium, // >= 40
			wantMinFlags: 3,              // VALID_GST, VALID_PAN, NOT_APPROVED
		},
		{
			name: "HIGH risk - multiple issues",
			vendor: &Vendor{
				ID:          "v6",
				Name:        "Risky Corp",
				GSTNumber:   "BADGST",
				PANNumber:   "BADPANNUMBER",
				IFSCCode:    "BADIFSC",
				BankAccount: "999",
				TenantID:    "t1",
				Approved:    false,
			},
			existing:     []*Vendor{},
			blacklist:    []string{},
			tenantID:     "t1",
			wantScore:    15,           // 50 - 20 (not approved) - 15 (incomplete)
			wantTier:     RiskTierHigh, // < 40
			wantMinFlags: 2,            // NOT_APPROVED, INCOMPLETE_DOCUMENTS
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeVendorRisk(tt.vendor, tt.existing, tt.blacklist, tt.tenantID)
			if result.Score != tt.wantScore {
				t.Errorf("ComputeVendorRisk().Score = %v, want %v", result.Score, tt.wantScore)
			}
			if result.Tier != tt.wantTier {
				t.Errorf("ComputeVendorRisk().Tier = %v, want %v", result.Tier, tt.wantTier)
			}
			if len(result.Flags) < tt.wantMinFlags {
				t.Errorf("ComputeVendorRisk().Flags = %v, expected at least %d flags", result.Flags, tt.wantMinFlags)
			}
		})
	}
}
