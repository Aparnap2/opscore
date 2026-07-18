package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestFullPipelineE2E(t *testing.T) {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("E2E_BASE_URL not set; skipping e2e")
	}
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. Health check
	req, err := http.NewRequest("GET", baseURL+"/health", nil)
	if err != nil {
		t.Fatalf("Health check request: %v", err)
	}
	req.Header.Set("X-API-Key", "owner-dev-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Health check returned %d", resp.StatusCode)
	}
	t.Log("✓ Health check OK")

	// 2. Upload a test PDF with unique content to avoid duplicate detection
	content := fmt.Sprintf("%%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/MediaBox[0 0 612 792]>>endobj\nxref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n0000000115 00000 n \ntrailer<</Size 4/Root 1 0 R>>\nstartxref\n190\n%%%%EOF\n-- e2e-run-%d --", time.Now().UnixNano())

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "test-invoice.pdf")
	if err != nil {
		t.Fatalf("Create form file: %v", err)
	}
	fw.Write([]byte(content))
	w.Close()

	req, err = http.NewRequest("POST", baseURL+"/upload", &buf)
	if err != nil {
		t.Fatalf("Create upload request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "owner-dev-key")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Upload returned %d: %s", resp.StatusCode, string(body))
	}

	var uploadResp struct {
		JobID   string `json:"job_id"`
		Status  string `json:"status"`
		BlobURL string `json:"blob_url"`
	}
	json.NewDecoder(resp.Body).Decode(&uploadResp)
	if uploadResp.JobID == "" {
		t.Fatal("Upload returned empty job_id")
	}
	t.Logf("✓ Upload OK: job_id=%s, status=%s", uploadResp.JobID, uploadResp.Status)

	// 3. Poll job status until it changes from PENDING
	maxPolls := 15
	for i := 0; i < maxPolls; i++ {
		time.Sleep(1 * time.Second)

		req, _ := http.NewRequest("GET", baseURL+"/jobs/"+uploadResp.JobID, nil)
		req.Header.Set("X-Tenant-ID", "default")
		req.Header.Set("X-API-Key", "owner-dev-key")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Job status request failed: %v", err)
		}

		var statusResp struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		json.NewDecoder(resp.Body).Decode(&statusResp)
		resp.Body.Close()

		t.Logf("  Poll %d: job status = %s", i+1, statusResp.Status)

		// Job should transition from PENDING (OCR with no Sarvam API key will fail)
		if statusResp.Status != "PENDING" {
			t.Logf("✓ Job status changed from PENDING to %s", statusResp.Status)
			if statusResp.Error != "" {
				t.Logf("  Error: %s", statusResp.Error)
			}
			return
		}
	}

	// Check server log for what happened
	serverLog, _ := os.ReadFile("/tmp/opscore-server.log")
	t.Logf("Server log:\n%s", string(serverLog))

	t.Fatal("Job status did not change within timeout — queue consumer may not be running")
}
