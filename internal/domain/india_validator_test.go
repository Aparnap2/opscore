package domain

import (
	"testing"
)

func TestValidateAadhaar(t *testing.T) {
	tests := []struct {
		name    string
		aadhaar string
		valid  bool
	}{
		{"valid 12 digit", "123456789012", true},
		{"valid all 9s", "999999999999", true},
		{"invalid - too short", "12345678901", false},
		{"invalid - too long", "1234567890123", false},
		{"invalid - starts with 0", "012345678901", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateAadhaar(tt.aadhaar)
			if result != tt.valid {
				t.Errorf("ValidateAadhaar(%q) = %v, want %v", tt.aadhaar, result, tt.valid)
			}
		})
	}
}

func TestValidateUPI(t *testing.T) {
	tests := []struct {
		name string
		upi  string
		valid bool
	}{
		{"valid UPI", "9876543210@upi", true},
		{"valid UPI with bank", "9876543210@okhdfcbank", true},
		{"valid UPI 3 letters", "9876543210@xyz", true},
		{"invalid - no @", "9876543210upi", false},
		{"invalid - starts with letter", "a987654321@upi", false},
		{"invalid - short number", "987654321@upi", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateUPI(tt.upi)
			if result != tt.valid {
				t.Errorf("ValidateUPI(%q) = %v, want %v", tt.upi, result, tt.valid)
			}
		})
	}
}

func TestValidateBankAccount(t *testing.T) {
	tests := []struct {
		name    string
		account string
		valid   bool
	}{
		{"valid 9 digits", "123456789", true},
		{"valid 18 digits", "123456789012345678", true},
		{"valid 8 digits", "12345678", true},
		{"invalid - too short", "1234567", false},
		{"invalid - has letters", "1234abcd", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateBankAccount(tt.account)
			if result != tt.valid {
				t.Errorf("ValidateBankAccount(%q) = %v, want %v", tt.account, result, tt.valid)
			}
		})
	}
}

func TestValidatePINCode(t *testing.T) {
	tests := []struct {
		name    string
		pin    string
		valid  bool
	}{
		{"valid PIN", "110001", true},
		{"valid PIN 2", "500001", true},
		{"invalid - starts with 0", "011001", false},
		{"invalid - too short", "11001", false},
		{"invalid - all zeros", "000000", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePINCode(tt.pin)
			if result != tt.valid {
				t.Errorf("ValidatePINCode(%q) = %v, want %v", tt.pin, result, tt.valid)
			}
		})
	}
}

func TestValidateStateCode(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		valid bool
	}{
		{"valid state 01", "01", true},
		{"valid state 37", "37", true},
		{"valid state 27", "27", true},
		{"invalid - 00", "00", false},
		{"invalid - 38", "38", false},
		{"invalid - too short", "1", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateStateCode(tt.code)
			if result != tt.valid {
				t.Errorf("ValidateStateCode(%q) = %v, want %v", tt.code, result, tt.valid)
			}
		})
	}
}

func TestExtractStateCodeFromGST(t *testing.T) {
	tests := []struct {
		name      string
		gst      string
		wantCode string
	}{
		{"valid GST 27", "27AABCS1209D1Z5", "27"},
		{"valid GST 07", "07AABCS1209D1Z5", "07"},
		{"short GST", "27", "27"},
		{"empty GST", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractStateCodeFromGST(tt.gst)
			if result != tt.wantCode {
				t.Errorf("ExtractStateCodeFromGST(%q) = %q, want %q", tt.gst, result, tt.wantCode)
			}
		})
	}
}

func TestGetStateFromGST(t *testing.T) {
	tests := []struct {
		name    string
		gst     string
		want    string
	}{
		{"Maharashtra", "27AABCS1209D1Z5", "Maharashtra"},
		{"Delhi", "07AABCS1209D1Z5", "Delhi"},
		{"Karnataka", "29AABCS1209D1Z5", "Karnataka"},
		{"Unknown", "00", "Unknown"},
		{"Invalid", "", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetStateFromGST(tt.gst)
			if result != tt.want {
				t.Errorf("GetStateFromGST(%q) = %q, want %q", tt.gst, result, tt.want)
			}
		})
	}
}

func TestMapStateCodeToName(t *testing.T) {
	// Test that known state codes map correctly
	testCases := map[string]string{
		"01": "Jammu and Kashmir",
		"07": "Delhi",
		"11": "Sikkim",
		"24": "Gujarat",
		"29": "Karnataka",
		"33": "Tamil Nadu",
	}

	for code, wantName := range testCases {
		if gotName, ok := StateCodeToName[code]; !ok {
			t.Errorf("StateCodeToName missing key %s", code)
		} else if gotName != wantName {
			t.Errorf("StateCodeToName[%s] = %q, want %q", code, gotName, wantName)
		}
	}
}