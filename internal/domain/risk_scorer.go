package domain

import (
	"regexp"
)

// Regex patterns for India-specific validation
var (
	// GST regex: 15 character alphanumeric format
	// 2 digits + 5 letters + 4 digits + 1 letter + 1 alphanumeric + Z + 1 alphanumeric
	GSTRegex = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`)

	// PAN regex: 5 letters + 4 digits + 1 letter
	PANRegex = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]{1}$`)

	// IFSC regex: 4 letters + 0 + 6 alphanumeric
	IFSRegex = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
)

// RiskResult holds the computed risk assessment for a vendor
type RiskResult struct {
	Tier  RiskTier `json:"tier"`
	Flags []string `json:"flags"`
	Score int      `json:"score"`
}

// ValidateGST checks if the given GST number is valid
func ValidateGST(gst string) bool {
	if gst == "" {
		return false
	}
	return GSTRegex.MatchString(gst)
}

// ValidatePAN checks if the given PAN number is valid
func ValidatePAN(pan string) bool {
	if pan == "" {
		return false
	}
	return PANRegex.MatchString(pan)
}

// ValidateIFSC checks if the given IFSC code is valid
func ValidateIFSC(ifsc string) bool {
	if ifsc == "" {
		return false
	}
	return IFSRegex.MatchString(ifsc)
}

// ComputeVendorRisk calculates the risk score and tier for a vendor
// PRD v3.0 Spec (Section 8):
// - Base score: 50
// - Valid GST: +15
// - Valid PAN: +10
// - Valid IFSC: +10
// - Duplicate GST in system: -40
// - Active dispute flag: -20 (using !Approved as proxy)
// - Incomplete documents: -15 (no PAN or no GST)
func ComputeVendorRisk(vendor *Vendor, existingVendors []*Vendor, blacklist []string, tenantID string) *RiskResult {
	score := 50 // Base score per PRD v3.0 spec
	var flags []string

	gst := vendor.GSTNumber
	pan := vendor.PANNumber
	ifsc := vendor.IFSCCode

	// Add points for valid credentials (per PRD spec)
	// Valid GST: +15
	if ValidateGST(gst) {
		score += 15
		flags = append(flags, "VALID_GST")
	}

	// Valid PAN: +10
	if ValidatePAN(pan) {
		score += 10
		flags = append(flags, "VALID_PAN")
	}

	// Valid IFSC: +10
	if ifsc != "" && ValidateIFSC(ifsc) {
		score += 10
		flags = append(flags, "VALID_IFSC")
	}

	// Check duplicate GST in system: -40
	if gst != "" {
		for _, ev := range existingVendors {
			if ev.ID == vendor.ID {
				continue
			}
			if ev.GSTNumber == gst && ev.TenantID == tenantID {
				score -= 40
				flags = append(flags, "DUPLICATE_GST:"+ev.ID)
				break
			}
		}
	}

	// Active dispute flag: -20 (vendor not approved indicates potential dispute)
	if !vendor.Approved {
		score -= 20
		flags = append(flags, "NOT_APPROVED")
	}

	// Incomplete documents: -15 (no valid PAN AND no valid GST)
	// Only penalize if both PAN and GST are missing/invalid
	if !ValidatePAN(pan) && !ValidateGST(gst) {
		score -= 15
		flags = append(flags, "INCOMPLETE_DOCUMENTS")
	}

	// Clamp score between 0-100
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	// Determine tier based on score (inverted from before)
	// Lower score = higher risk in this new model
	tier := RiskTierLow
	if score >= 70 {
		tier = RiskTierLow // High trust
	} else if score >= 40 {
		tier = RiskTierMedium // Medium trust
	} else {
		tier = RiskTierHigh // Low trust
	}

	return &RiskResult{
		Score: score,
		Tier:  tier,
		Flags: flags,
	}
}
