package domain

import (
	"regexp"
)

// India-specific regex patterns and validators
var (
	// AadhaarRegex - 12 digit unique identification number (must not start with 0)
	AadhaarRegex = regexp.MustCompile(`^[1-9][0-9]{11}$`)

	// UPIRegex - Unified Payments Interface format (like mobilenumber@upi)
	UPIRegex = regexp.MustCompile(`^[0-9]{10}@[a-zA-Z]{3,}$`)

	// BankAccountRegex - Basic bank account number (8-18 digits)
	BankAccountRegex = regexp.MustCompile(`^[0-9]{8,18}$`)

	// PINCodeRegex - Indian PIN code (6 digits)
	PINCodeRegex = regexp.MustCompile(`^[1-9][0-9]{5}$`)

	// StateCodeRegex - Indian state/territory codes (01-37)
	StateCodeRegex = regexp.MustCompile(`^(0[1-9]|[1-2][0-9]|3[0-7])$`)
)

// ValidateAadhaar validates an Aadhaar number
func ValidateAadhaar(aadhaar string) bool {
	if aadhaar == "" {
		return false
	}
	return AadhaarRegex.MatchString(aadhaar)
}

// ValidateUPI validates a UPI ID format
func ValidateUPI(upi string) bool {
	if upi == "" {
		return false
	}
	return UPIRegex.MatchString(upi)
}

// ValidateBankAccount validates a bank account number
func ValidateBankAccount(account string) bool {
	if account == "" {
		return false
	}
	return BankAccountRegex.MatchString(account)
}

// ValidatePINCode validates an Indian PIN code
func ValidatePINCode(pin string) bool {
	if pin == "" {
		return false
	}
	return PINCodeRegex.MatchString(pin)
}

// ValidateStateCode validates an Indian state/territory code
func ValidateStateCode(code string) bool {
	if code == "" {
		return false
	}
	return StateCodeRegex.MatchString(code)
}

// ExtractStateCodeFromGST extracts the state code (first 2 digits) from a GST number
func ExtractStateCodeFromGST(gst string) string {
	if len(gst) >= 2 {
		return gst[:2]
	}
	return ""
}

// IsValidGSTStateCode checks if the state code (first 2 digits of GST) is valid
func IsValidGSTStateCode(gst string) bool {
	stateCode := ExtractStateCodeFromGST(gst)
	return ValidateStateCode(stateCode)
}

// MapStateCodeToName maps Indian state codes to state names
var StateCodeToName = map[string]string{
	"01": "Jammu and Kashmir",
	"02": "Himachal Pradesh",
	"03": "Punjab",
	"04": "Chandigarh",
	"05": "Uttarakhand",
	"06": "Haryana",
	"07": "Delhi",
	"08": "Rajasthan",
	"09": "Uttar Pradesh",
	"10": "Bihar",
	"11": "Sikkim",
	"12": "Arunachal Pradesh",
	"13": "Nagaland",
	"14": "Manipur",
	"15": "Mizoram",
	"16": "Tripura",
	"17": "Meghalaya",
	"18": "Assam",
	"19": "West Bengal",
	"20": "Jharkhand",
	"21": "Odisha",
	"22": "Chhattisgarh",
	"23": "Madhya Pradesh",
	"24": "Gujarat",
	"25": "Daman and Diu",
	"26": "Dadra and Nagar Haveli",
	"27": "Maharashtra",
	"28": "Andhra Pradesh (Old)",
	"29": "Karnataka",
	"30": "Goa",
	"31": "Lakshadweep",
	"32": "Kerala",
	"33": "Tamil Nadu",
	"34": "Puducherry",
	"35": "Andaman and Nicobar Islands",
	"36": "Telangana",
	"37": "Andhra Pradesh (New)",
}

// GetStateFromGST returns the state name from a GST number
func GetStateFromGST(gst string) string {
	stateCode := ExtractStateCodeFromGST(gst)
	if name, ok := StateCodeToName[stateCode]; ok {
		return name
	}
	return "Unknown"
}

// IndiaValidator provides India-specific validation
type IndiaValidator struct{}

// NewIndiaValidator creates a new India validator
func NewIndiaValidator() *IndiaValidator {
	return &IndiaValidator{}
}

// ValidationResult holds validation results
type ValidationResult struct {
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// HasErrors returns true if there are any errors
func (v *ValidationResult) HasErrors() bool {
	return len(v.Errors) > 0
}

// HasWarnings returns true if there are any warnings
func (v *ValidationResult) HasWarnings() bool {
	return len(v.Warnings) > 0
}

// ValidateVendor validates vendor identifiers (GST, PAN, IFSC)
func (iv *IndiaValidator) ValidateVendor(gst, pan, ifsc string) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if gst != "" && !ValidateGST(gst) {
		result.Errors = append(result.Errors, "Invalid GST number")
		result.Valid = false
	}

	if pan != "" && !ValidatePAN(pan) {
		result.Errors = append(result.Errors, "Invalid PAN number")
		result.Valid = false
	}

	if ifsc != "" && !ValidateIFSC(ifsc) {
		result.Errors = append(result.Errors, "Invalid IFSC code")
		result.Valid = false
	}

	return result
}

// ValidateAll validates multiple fields from key-values map
func (iv *IndiaValidator) ValidateAll(kvs map[string]string) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// Validate GST
	if gst, ok := kvs["gst"]; ok {
		if !ValidateGST(gst) {
			result.Errors = append(result.Errors, "Invalid GST number")
			result.Valid = false
		}
	}

	// Validate PAN
	if pan, ok := kvs["pan"]; ok {
		if !ValidatePAN(pan) {
			result.Errors = append(result.Errors, "Invalid PAN number")
			result.Valid = false
		}
	}

	// Validate IFSC
	if ifsc, ok := kvs["ifsc"]; ok {
		if !ValidateIFSC(ifsc) {
			result.Errors = append(result.Errors, "Invalid IFSC code")
			result.Valid = false
		}
	}

	return result
}