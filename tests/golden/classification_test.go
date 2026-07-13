package golden

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aparna/opscore/internal/domain"
)

func TestDocumentClassifier_Golden(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		golden string
	}{
		{"invoice", "tax invoice gstin 27abcde1234f1z1", "invoice"},
		{"contract", "agreement terms and conditions", "contract"},
		{"gst_notice", "gst department show cause notice", "gst_notice"},
		{"po", "purchase order order no 12345", "purchase_order"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domain.Classify(tt.input)

			goldenFile := filepath.Join("testdata", tt.name+".golden")
			expected, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("golden file not found: %v", err)
			}

			if string(expected) != string(result.Type) {
				t.Errorf("got %v, want %v (from golden)", result.Type, string(expected))
			}
		})
	}
}
