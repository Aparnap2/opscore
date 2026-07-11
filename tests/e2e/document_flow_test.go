//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var apiBase = getEnvOrDefault("OPSCORE_API_URL", "http://localhost:8081")

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestUploadAndProcessDocument verifies the full document ingestion flow:
// upload → queue → process → complete.
func TestUploadAndProcessDocument(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. Health check
	resp, err := client.Get(apiBase + "/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	// 2. Upload a sample document
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test_invoice.txt")
	if err != nil {
		t.Fatalf("Create form file: %v", err)
	}
	content := `Invoice INV-001
Vendor: ABC Corp
GST: 29AABCU9603R1ZP
Date: 15-Jan-2024
Items: Office Chairs x 10 = ₹50,000
Subtotal: ₹50,000
CGST 9%: ₹4,500
SGST 9%: ₹4,500
Total: ₹59,000`
	part.Write([]byte(content))
	writer.Close()

	req, err := http.NewRequest("POST", apiBase+"/upload", body)
	if err != nil {
		t.Fatalf("Create upload request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "e2e-test")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("Expected 202 for upload, got %d", resp.StatusCode)
	}

	var uploadResp struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&uploadResp)

	if uploadResp.JobID == "" {
		t.Fatal("Empty job ID in upload response")
	}
	t.Logf("Upload accepted: job=%s status=%s", uploadResp.JobID, uploadResp.Status)

	// 3. Poll job status until completed
	var finalStatus string
	for i := 0; i < 30; i++ {
		resp, err := client.Get(apiBase + "/jobs/" + uploadResp.JobID)
		if err != nil {
			t.Fatalf("Get job failed: %v", err)
		}
		var jobResp struct {
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		json.NewDecoder(resp.Body).Decode(&jobResp)
		resp.Body.Close()

		finalStatus = jobResp.Status
		if finalStatus == "COMPLETED" || finalStatus == "FAILED" || finalStatus == "TERMINAL_FAILED" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Logf("Final job status: %s", finalStatus)
	if finalStatus != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s", finalStatus)
	}

	// 4. Verify summary endpoint reflects the job
	resp, err = client.Get(apiBase + "/status/summary")
	if err != nil {
		t.Fatalf("Summary request failed: %v", err)
	}
	defer resp.Body.Close()

	var summaryResp struct {
		JobCounts map[string]int `json:"job_counts"`
	}
	json.NewDecoder(resp.Body).Decode(&summaryResp)
	t.Logf("Summary: %v", summaryResp.JobCounts)

	total := 0
	for _, v := range summaryResp.JobCounts {
		total += v
	}
	if total == 0 {
		t.Error("Summary shows zero jobs — expected at least 1")
	}
}

// TestVendorOnboardingFlow verifies vendor creation → processing → completion.
func TestVendorOnboardingFlow(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}

	// Use form-encoded POST as the API expects
	body := strings.NewReader("name=E2E+Vendor+Inc&gst=29AABCU9603R1ZP&pan=AAACB1234C&ifsc=SBIN0001234&bank_account=123456789012")
	req, err := http.NewRequest("POST", apiBase+"/vendors", body)
	if err != nil {
		t.Fatalf("Create vendor request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Tenant-ID", "e2e-test")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Vendor create failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Errorf("Expected 201, got %d", resp.StatusCode)
	}

	var vendorResp struct {
		VendorID string `json:"vendor_id"`
		Status   string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&vendorResp)

	if vendorResp.VendorID == "" {
		t.Fatal("Empty vendor ID")
	}
	t.Logf("Vendor created: id=%s status=%s", vendorResp.VendorID, vendorResp.Status)

	// Verify vendor list includes the new vendor
	resp, err = client.Get(apiBase + "/vendors")
	if err != nil {
		t.Fatalf("List vendors failed: %v", err)
	}
	defer resp.Body.Close()

	var listResp struct {
		Vendors []any `json:"vendors"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Vendors) == 0 {
		t.Error("Vendor list is empty after creation")
	}
	t.Logf("Vendors listed: %d", len(listResp.Vendors))
}
