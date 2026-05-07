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
		pan  string
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
		ifsc string
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
	tests := []struct {
		name          string
		vendor       *Vendor
		existing    []*Vendor
		blacklist   []string
		tenantID    string
		wantScore   int
		wantTier    RiskTier
		wantFlags   int // minimum expected flags
	}{
		{
			name: "clean vendor - no issues",
			vendor: &Vendor{
				ID:    "v1",
				Name:  "Clean Corp",
				GSTNumber: "27AABCS1209D1Z5",
				PANNumber: "AABCS1209D",
				IFSCCode: "HDFC0CGBIBL",
				BankAccount: "1234567890",
				TenantID: "t1",
			},
			existing:  []*Vendor{},
			blacklist: []string{},
			tenantID: "t1",
			wantScore: 0,
			wantTier:  RiskTierLow,
			wantFlags: 0,
		},
		{
			name: "invalid GST",
			vendor: &Vendor{
				ID:       "v2",
				Name:     "Bad GST Corp",
				GSTNumber: "INVALID",
				TenantID: "t1",
			},
			existing:  []*Vendor{},
			blacklist: []string{},
			tenantID:  "t1",
			wantScore: 45, // 25 GST + 20 PAN (empty PAN is invalid)
			wantTier:  RiskTierMedium,
			wantFlags: 2,
		},
		{
			name: "invalid PAN and IFSC",
			vendor: &Vendor{
				ID:       "v3",
				Name:     "Multiple Errors",
				PANNumber: "BADPAN",
				IFSCCode:  "1234",
				TenantID:  "t1",
			},
			existing:  []*Vendor{},
			blacklist: []string{},
			tenantID:  "t1",
			wantScore: 60, // 20 PAN + 15 IFSC + 25 No GST = 60
			wantTier:  RiskTierHigh,
			wantFlags: 3,
		},
		{
			name: "blacklisted vendor",
			vendor: &Vendor{
				ID:       "v4",
				Name:     "Bad Actor",
				GSTNumber: "27AABCS1209D1Z5",
				TenantID: "t1",
			},
			existing:  []*Vendor{},
			blacklist: []string{"27AABCS1209D1Z5"},
			tenantID: "t1",
			wantScore: 70, // 25 GST + 50 blacklist = 75, but check: valid GST=0, blacklist=50, so 50. Wait, invalid GST check is skipped when blacklisted matches
			wantTier: RiskTierHigh,
			wantFlags: 1,
		},
		{
			name: "duplicate bank account",
			vendor: &Vendor{
				ID:          "v5",
				Name:        "New Vendor",
				BankAccount: "1234567890",
				TenantID:    "t1",
			},
			existing: []*Vendor{
				{
					ID:          "existing1",
					Name:        "Old Vendor",
					BankAccount: "1234567890",
					TenantID:    "t1",
				},
			},
			blacklist: []string{},
			tenantID: "t1",
			wantScore: 85, // 25 GST + 20 PAN + 40 duplicate bank
			wantTier: RiskTierHigh,
			wantFlags: 3,
		},
		{
			name: "HIGH risk vendor",
			vendor: &Vendor{
				ID:          "v6",
				Name:        "Risky Corp",
				GSTNumber:   "BADGST",
				PANNumber:  "BADPANNUMBER",
				IFSCCode:   "BADIFSC",
				BankAccount: "999",
				TenantID:   "t1",
			},
			existing:  []*Vendor{},
			blacklist: []string{},
			tenantID:  "t1",
			wantScore: 60, // 25 + 20 + 15
			wantTier:  RiskTierHigh,
			wantFlags: 3,
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
			if len(result.Flags) < tt.wantFlags {
				t.Errorf("ComputeVendorRisk().Flags = %v, expected at least %d flags", result.Flags, tt.wantFlags)
			}
		})
	}
}