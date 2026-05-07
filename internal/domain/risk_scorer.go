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
func ComputeVendorRisk(vendor *Vendor, existingVendors []*Vendor, blacklist []string, tenantID string) *RiskResult {
	score := 0
	var flags []string

	gst := vendor.GSTNumber
	pan := vendor.PANNumber
	ifsc := vendor.IFSCCode
	bankAccount := vendor.BankAccount
	name := vendor.Name

	// Validate GST format
	if !ValidateGST(gst) {
		score += 25
		flags = append(flags, "INVALID_GST_FORMAT")
	}

	// Validate PAN format
	if !ValidatePAN(pan) {
		score += 20
		flags = append(flags, "INVALID_PAN_FORMAT")
	}

	// Validate IFSC format
	if ifsc != "" && !ValidateIFSC(ifsc) {
		score += 15
		flags = append(flags, "INVALID_IFSC_FORMAT")
	}

	// Check blacklist
	for _, blocked := range blacklist {
		if gst == blocked || pan == blocked {
			score += 50
			flags = append(flags, "BLACKLISTED_ENTITY")
			break
		}
	}

	// Check duplicate bank accounts
	if bankAccount != "" {
		for _, ev := range existingVendors {
			if ev.BankAccount == bankAccount && ev.TenantID == tenantID {
				score += 40
				flags = append(flags, "DUPLICATE_BANK_ACCOUNT:"+ev.ID)
				break
			}
		}
	}

	// Check similar vendor names (simple fuzzy matching)
	if name != "" && len(existingVendors) > 0 {
		for _, ev := range existingVendors {
			if ev.ID == vendor.ID {
				continue
			}
			existingName := ev.Name
			if existingName != "" {
				similarity := fuzzyMatch(name, existingName)
				if similarity > 85 {
					score += 20
					flags = append(flags, "SIMILAR_VENDOR_NAME:"+ev.ID)
					break
				}
			}
		}
	}

	// Determine tier based on score
	tier := RiskTierLow
	if score >= 60 {
		tier = RiskTierHigh
	} else if score >= 30 {
		tier = RiskTierMedium
	}

	return &RiskResult{
		Score: score,
		Tier:  tier,
		Flags: flags,
	}
}

// fuzzyMatch returns a simple similarity score (0-100)
// This is a basic implementation without external dependencies
func fuzzyMatch(s1, s2 string) int {
	if len(s1) == 0 || len(s2) == 0 {
		return 0
	}

	// Count matching characters at same positions
	matches := 0
	runes1 := []rune(s1)
	runes2 := []rune(s2)
	minLen := len(runes1)
	if len(runes2) < minLen {
		minLen = len(runes2)
	}

	for i := 0; i < minLen; i++ {
		c1, c2 := runes1[i], runes2[i]
		// Case-insensitive match
		if c1 == c2 {
			matches++
		} else if c1 >= 'A' && c1 <= 'Z' && c1+32 == c2 {
			matches++
		} else if c2 >= 'A' && c2 <= 'Z' && c2+32 == c1 {
			matches++
		}
	}

	// Return similarity as percentage
	total := len(runes1) + len(runes2)
	if total == 0 {
		return 0
	}
	return (matches * 200) / total
}
