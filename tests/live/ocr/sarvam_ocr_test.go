//go:build live

package ocr

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/sarvam"
	"github.com/aparna/opscore/tests/live"
)

// fixturePath resolves a repo-root-relative path regardless of the test's working
// directory (go test runs from the package dir, not the repo root).
func fixturePath(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("could not determine test source path")
	}
	// thisFile = tests/live/ocr/sarvam_ocr_test.go -> repo root is 3 levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, rel)
}

func init() { live.ResetBudget() }

func TestSarvamOCRLive(t *testing.T) {
	apiKey := os.Getenv("SARVAM_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_API_KEY not set — skipping live OCR test")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{APIKey: apiKey})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Try the sample PDF, skip if not present
	pdfPath := fixturePath(t, "tests/fixtures/pdfs/sample_invoice.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		t.Skipf("Sample PDF not found at %s", pdfPath)
	}

	blobURL := pdfPath // For local file testing, use file path directly
	result, err := adapter.Extract(ctx, blobURL)
	if err != nil {
		t.Fatalf("OCR extraction failed: %v", err)
	}

	// Assert schema, not exact values
	if result.Text == "" && len(result.KeyValues) == 0 {
		t.Error("OCR returned empty text and no key values")
	}
	t.Logf("Confidence: %.2f", result.Confidence)
	t.Logf("Text length: %d", len(result.Text))
	t.Logf("Provider: %s", result.Provider)
	t.Logf("Language: %s", result.Language)
	for k, v := range result.KeyValues {
		t.Logf("  %s = %s", k, v)
	}

	live.DebugDump("ocr_result", result)
}

func TestSarvamOCREdgeCases(t *testing.T) {
	apiKey := os.Getenv("SARVAM_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{APIKey: apiKey})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Test with non-existent file — should error gracefully
	_, err := adapter.Extract(ctx, "tests/fixtures/pdfs/non_existent.pdf")
	if err == nil {
		t.Log("Non-existent file: no error returned (check adapter behavior)")
	} else {
		t.Logf("Non-existent file correctly errored: %v", err)
	}
}
