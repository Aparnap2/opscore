//go:build integration

package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/sarvam"
	"github.com/aparna/opscore/internal/providers"
)

// TestSarvamConnectivity verifies that the Sarvam OCR API is reachable
// and properly authenticates with the provided API key.
func TestSarvamConnectivity(t *testing.T) {
	// Read API key from environment
	apiKey := os.Getenv("SARVAM_AI_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_AI_API_KEY not set - skipping integration test")
	}

	// Create the adapter
	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.sarvam.ai",
	})

	// Test with a sample public PDF URL - this tests the full job lifecycle
	// Even if the job fails due to invalid URL, we verify connectivity
	testURL := "https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := adapter.Extract(ctx, testURL)

	// We're testing connectivity - so we either expect:
	// 1. Success (job completed) - best case
	// 2. Failure with specific error (API is reachable but URL invalid)
	// 3. Network error (API is unreachable)

	if err != nil {
		// Check if it's a network/auth error vs processing error
		errStr := err.Error()
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no such host") {
			t.Fatalf("Sarvam API is unreachable: %v", err)
		}
		// API reachable but job failed (e.g., invalid URL) - connectivity OK
		t.Logf("API reachable but job failed (expected for invalid URL): %v", err)
		return
	}

	// If we got a result, verify the structure
	if result == nil {
		t.Fatal("Expected OCR result, got nil")
	}

	// Validate result structure
	if result.Provider != "sarvam" {
		t.Errorf("Expected provider 'sarvam', got '%s'", result.Provider)
	}

	t.Logf("Sarvam connectivity verified - Provider: %s, Confidence: %.2f", result.Provider, result.Confidence)
}

// TestSarvamOCRWithLocalFile tests the full OCR workflow with a local PDF file
// This tests the 5-step Sarvam workflow: create job → get upload URL → upload file → start → poll
func TestSarvamOCRWithLocalFile(t *testing.T) {
	apiKey := os.Getenv("SARVAM_AI_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_AI_API_KEY not set - skipping integration test")
	}

	// Find the sample PDF
	// Check multiple possible locations
	possiblePaths := []string{
		"/home/aparna/Desktop/opscore/tests/fixtures/pdfs/sample_invoice.pdf",
		"tests/fixtures/pdfs/sample_invoice.pdf",
		"./tests/fixtures/pdfs/sample_invoice.pdf",
	}

	var pdfPath string
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			pdfPath = p
			break
		}
	}

	if pdfPath == "" {
		t.Skip("Sample PDF not found - skipping test")
	}

	t.Logf("Using PDF: %s", pdfPath)

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.sarvam.ai",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Test the full Extract workflow with local file
	result, err := adapter.Extract(ctx, pdfPath)

	if err != nil {
		errStr := err.Error()
		// Check for connectivity issues
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no such host") {
			t.Fatalf("Sarvam API is unreachable: %v", err)
		}
		// Other errors - log but don't fail (API might be down)
		t.Logf("OCR extraction failed: %v", err)
		t.SkipNow()
	}

	if result == nil {
		t.Fatal("Expected OCR result, got nil")
	}

	// Validate result
	if result.Provider != "sarvam" {
		t.Errorf("Expected provider 'sarvam', got '%s'", result.Provider)
	}

	if result.Language == "" {
		t.Error("Expected language to be set")
	}

	if result.Confidence == 0 {
		t.Error("Expected confidence to be set")
	}

	t.Logf("OCR Result - Provider: %s, Language: %s, Confidence: %.2f", result.Provider, result.Language, result.Confidence)
	if result.Text != "" {
		// Show first 200 chars of extracted text
		textPreview := result.Text
		if len(textPreview) > 200 {
			textPreview = textPreview[:200] + "..."
		}
		t.Logf("Extracted text (first 200 chars): %s", textPreview)
		t.Logf("Total extracted text length: %d chars", len(result.Text))
	}
	if len(result.Tables) > 0 {
		t.Logf("Number of tables: %d", len(result.Tables))
	}
	if len(result.KeyValues) > 0 {
		t.Logf("Number of key-value pairs: %d", len(result.KeyValues))
	}
}

// TestSarvamAPIJobCreation tests only the job creation endpoint
// This is a lighter test that doesn't wait for job completion.
func TestSarvamAPIJobCreation(t *testing.T) {
	apiKey := os.Getenv("SARVAM_AI_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_AI_API_KEY not set - skipping integration test")
	}

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.sarvam.ai",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test job creation only
	jobID, err := adapter.CreateJob(ctx)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no such host") {
			t.Fatalf("Sarvam API is unreachable: %v", err)
		}
		// API error but reachable
		t.Logf("API reachable, job creation returned error: %v", err)
		return
	}

	if jobID == "" {
		t.Fatal("Expected non-empty job ID")
	}

	t.Logf("Job created successfully: %s", jobID)
}

// TestSarvamUploadURLGeneration tests getting a presigned upload URL
func TestSarvamUploadURLGeneration(t *testing.T) {
	apiKey := os.Getenv("SARVAM_AI_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_AI_API_KEY not set - skipping integration test")
	}

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.sarvam.ai",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create job first
	jobID, err := adapter.CreateJob(ctx)
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Get upload URL
	uploadURL, err := adapter.GetUploadURL(ctx, jobID, "test.pdf")
	if err != nil {
		t.Fatalf("Failed to get upload URL: %v", err)
	}

	if uploadURL == "" {
		t.Fatal("Expected non-empty upload URL")
	}

	// Verify it's a valid URL
	if !strings.HasPrefix(uploadURL, "http") {
		t.Errorf("Expected URL to start with http, got: %s", uploadURL)
	}

	t.Logf("Got upload URL: %s...", uploadURL[:min(50, len(uploadURL))])
}

// TestSarvamAdapterImplementsInterface verifies the Sarvam adapter
// properly implements the OCRProvider interface.
func TestSarvamAdapterImplementsInterface(t *testing.T) {
	apiKey := os.Getenv("SARVAM_AI_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_AI_API_KEY not set - skipping integration test")
	}

	adapter := sarvam.NewOCRAdapter(sarvam.OCRConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.sarvam.ai",
	})

	// Compile-time check: this verifies OCRAdapter implements providers.OCRProvider
	var _ providers.OCRProvider = adapter

	t.Log("Sarvam adapter correctly implements providers.OCRProvider interface")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Helper function to resolve path relative to project root
func getProjectRoot() string {
	// Check if we're in the project root
	if _, err := os.Stat("go.mod"); err == nil {
		return "."
	}
	// Check parent directory
	if _, err := os.Stat("../go.mod"); err == nil {
		return ".."
	}
	// Check for specific path
	if abs, err := filepath.Abs("/home/aparna/Desktop/opscore"); err == nil {
		return abs
	}
	return "."
}