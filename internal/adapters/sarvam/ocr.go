package sarvam

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/providers"
)

// OCRConfig holds the configuration for the Sarvam OCR adapter
type OCRConfig struct {
	APIKey     string
	BaseURL    string
	APIVersion string
}

// OCRAdapter handles OCR operations using Sarvam's document digitization API
type OCRAdapter struct {
	client *http.Client
	config OCRConfig
}

// Job states from Sarvam API per Context7 docs
const (
	SarvamJobStateAccepted           = "Accepted"
	SarvamJobStatePending            = "Pending"
	SarvamJobStateRunning            = "Running"
	SarvamJobStateCompleted          = "Completed"
	SarvamJobStatePartiallyCompleted = "PartiallyCompleted"
	SarvamJobStateFailed             = "Failed"
)

// NewOCRAdapter creates a new Sarvam OCR adapter
func NewOCRAdapter(config OCRConfig) *OCRAdapter {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.sarvam.ai"
	}

	return &OCRAdapter{
		client: &http.Client{Timeout: 120 * time.Second},
		config:  config,
	}
}

// Extract performs OCR on a document
// Workflow: create job → upload file → start → poll status
func (o *OCRAdapter) Extract(ctx context.Context, input string) (*providers.OCRResult, error) {
	// Determine if input is a file path or URL
	// If it's a local file path, read and upload it
	// If it's a URL, we need to handle differently

	// Check if input is a local file
	if data, err := os.ReadFile(input); err == nil {
		return o.extractFromFile(ctx, data, input)
	}

	// If not a local file, treat as a URL to download and process
	// For now, we'll try to extract from URL - this may not work with Sarvam's new API
	// In production, you'd download the file first then upload
	return o.extractFromURL(ctx, input)
}

// extractFromFile performs OCR by uploading a file directly
func (o *OCRAdapter) extractFromFile(ctx context.Context, fileData []byte, filename string) (*providers.OCRResult, error) {
	// Step 1: Create the job with language and output_format
	jobID, err := o.createJob(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating OCR job: %w", err)
	}

	// Step 2: Get presigned upload URL
	uploadURL, err := o.getUploadURL(ctx, jobID, filename)
	if err != nil {
		return nil, fmt.Errorf("getting upload URL: %w", err)
	}

	// Step 3: Upload the file to presigned URL
	if err := o.uploadFile(ctx, uploadURL, fileData, filename); err != nil {
		return nil, fmt.Errorf("uploading file: %w", err)
	}

	// Step 4: Start the job
	if err := o.startJob(ctx, jobID); err != nil {
		return nil, fmt.Errorf("starting OCR job: %w", err)
	}

	// Step 5: Poll for completion
	result, err := o.pollForResult(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("polling OCR result: %w", err)
	}

	return result, nil
}

// extractFromURL attempts to extract from a URL - downloads and re-uploads
func (o *OCRAdapter) extractFromURL(ctx context.Context, url string) (*providers.OCRResult, error) {
	// Download the file from URL
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloading file from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file: %d", resp.StatusCode)
	}

	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading downloaded file: %w", err)
	}

	// Extract filename from URL or use default
	filename := "document.pdf"
	if idx := len(url) - 1; idx > 0 {
		if slashIdx := bytes.LastIndexByte([]byte(url), '/'); slashIdx > 0 {
			filename = url[slashIdx+1:]
			if !bytes.HasSuffix([]byte(filename), []byte(".pdf")) {
				filename = "document.pdf"
			}
		}
	}

	return o.extractFromFile(ctx, fileData, filename)
}

// createJob creates a new OCR job
// POST https://api.sarvam.ai/doc-digitization/job/v1
// Body: {"job_parameters": {"language": "en-IN", "output_format": "json"}}
// Response: {"job_id": "job_12345", "status": "Accepted"}
func (o *OCRAdapter) createJob(ctx context.Context) (string, error) {
	type jobParams struct {
		Language    string `json:"language"`
		OutputFormat string `json:"output_format"`
	}

	type createJobRequest struct {
		JobParameters jobParams `json:"job_parameters"`
	}

	type createJobResponse struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}

	// Use job_parameters format as required by the API
	// Output format must be "html" or "md" (json is always included by default)
	reqBody := createJobRequest{
		JobParameters: jobParams{
			Language:    "en-IN",
			OutputFormat: "md",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/doc-digitization/job/v1", o.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", o.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("creating job: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var result createJobResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if result.JobID == "" {
		return "", fmt.Errorf("no job_id returned from API")
	}

	return result.JobID, nil
}

// getUploadURL gets a presigned URL for file upload
// POST https://api.sarvam.ai/doc-digitization/job/v1/upload-files
// Body: {"job_id": "uuid", "files": ["document.pdf"]}
// Response: {"job_id": "uuid", "upload_urls": {"document.pdf": {"url": "..."}}}
func (o *OCRAdapter) getUploadURL(ctx context.Context, jobID, filename string) (string, error) {
	type uploadFilesRequest struct {
		JobID  string   `json:"job_id"`
		Files  []string `json:"files"`
	}

	// Response format varies - handle different structures
	type uploadURLInfo struct {
		URL       string `json:"url"`
		FileURL   string `json:"file_url"` // Azure storage format
	}

	type uploadFilesResponse struct {
		JobID       string                     `json:"job_id"`
		JobState    string                     `json:"job_state"`
		UploadURLs map[string]uploadURLInfo   `json:"upload_urls"`
		Message     string                     `json:"message,omitempty"`
		Code        int                        `json:"code,omitempty"`
	}

	// Use just the filename without path
	baseFilename := filename
	if idx := len(filename) - 1; idx > 0 {
		if slashIdx := strings.LastIndex(filename, "/"); slashIdx >= 0 {
			baseFilename = filename[slashIdx+1:]
		}
	}

	reqBody := uploadFilesRequest{
		JobID:  jobID,
		Files:  []string{baseFilename},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/doc-digitization/job/v1/upload-files", o.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", o.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("getting upload URL: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var result uploadFilesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// Check for various response formats
	// Format 1: upload_urls with url field
	if urlInfo, ok := result.UploadURLs[baseFilename]; ok && urlInfo.URL != "" {
		return urlInfo.URL, nil
	}

	// Format 2: upload_urls with file_url field (Azure storage)
	if urlInfo, ok := result.UploadURLs[baseFilename]; ok && urlInfo.FileURL != "" {
		return urlInfo.FileURL, nil
	}

	// Format 3: Try the first entry in upload_urls if only one file was sent
	if len(result.UploadURLs) > 0 {
		for _, urlInfo := range result.UploadURLs {
			if urlInfo.URL != "" {
				return urlInfo.URL, nil
			}
			if urlInfo.FileURL != "" {
				return urlInfo.FileURL, nil
			}
		}
	}

	// If we got a success message but no URL, the API might have queued the file differently
	if result.Message != "" && result.Code == 200 {
		// The API might return a message without URLs - check job status
		return "", fmt.Errorf("upload URL not in response (API returned: %s). Job may need polling.", result.Message)
	}

	return "", fmt.Errorf("no upload URL returned for file: %s (response: %s)", baseFilename, string(respBody))
}

// uploadFile uploads a file to the presigned URL
// PUT <presigned_url> with raw PDF content
// Azure Blob Storage requires x-ms-blob-type header
func (o *OCRAdapter) uploadFile(ctx context.Context, presignedURL string, fileData []byte, filename string) error {
	// Determine content type
	contentType := "application/pdf"
	if len(filename) > 4 && strings.ToLower(filename[len(filename)-4:]) == ".zip" {
		contentType = "application/zip"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "PUT", presignedURL, bytes.NewReader(fileData))
	if err != nil {
		return fmt.Errorf("creating upload request: %w", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("x-ms-blob-type", "BlockBlob") // Required for Azure Blob Storage

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("uploading file: %w", err)
	}
	defer resp.Body.Close()

	// Sarvam presigned URLs typically return 200 or 201 on success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// startJob starts an existing OCR job
// POST https://api.sarvam.ai/doc-digitization/job/v1/{job_id}/start
// Body: {}
func (o *OCRAdapter) startJob(ctx context.Context, jobID string) error {
	url := fmt.Sprintf("%s/doc-digitization/job/v1/%s/start", o.config.BaseURL, jobID)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", o.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("starting job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// pollForResult polls until the job completes or fails
// GET https://api.sarvam.ai/doc-digitization/job/v1/{job_id}/status
// Response: {job_id, job_state: "Completed|Running|Failed", output: {...}}
func (o *OCRAdapter) pollForResult(ctx context.Context, jobID string) (*providers.OCRResult, error) {
	url := fmt.Sprintf("%s/doc-digitization/job/v1/%s/status", o.config.BaseURL, jobID)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	maxAttempts := 90 // 3 minutes max (2s * 90 = 180s)

	for attempts := 0; attempts < maxAttempts; attempts++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return nil, fmt.Errorf("creating request: %w", err)
			}

			httpReq.Header.Set("api-subscription-key", o.config.APIKey)
			httpReq.Header.Set("Accept", "application/json")

			resp, err := o.client.Do(httpReq)
			if err != nil {
				return nil, fmt.Errorf("polling job: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				continue
			}

			// Parse response - structure from Context7 docs
			var jobResult struct {
				JobID    string          `json:"job_id"`
				JobState string          `json:"job_state"`
				Output   json.RawMessage `json:"output,omitempty"`
				Error    string          `json:"error,omitempty"`
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("reading response: %w", err)
			}

if err := json.Unmarshal(body, &jobResult); err != nil {
				return nil, fmt.Errorf("decoding result: %w", err)
			}

			switch jobResult.JobState {
			case SarvamJobStateCompleted, SarvamJobStatePartiallyCompleted:
				// Job completed - need to download the output ZIP file
				// Get download URL
				downloadURL, err := o.getDownloadURL(ctx, jobID)
				if err != nil {
					return nil, fmt.Errorf("getting download URL: %w", err)
				}

				// Download and extract the ZIP file
				extractedText, err := o.downloadAndExtract(ctx, downloadURL)
				if err != nil {
					return nil, fmt.Errorf("downloading output: %w", err)
				}

				return &providers.OCRResult{
					Text:       extractedText,
					Tables:     []providers.TableData{},
					KeyValues:  map[string]string{},
					Confidence: 0.85, // Default confidence for completed jobs
					Language:   "en-IN",
					Provider:   "sarvam",
				}, nil

			case SarvamJobStateFailed:
				return nil, fmt.Errorf("OCR job failed: %s", jobResult.Error)

			case SarvamJobStateAccepted, SarvamJobStatePending, SarvamJobStateRunning:
				// Continue polling
				continue
			}
		}
	}

	return nil, fmt.Errorf("OCR timeout after %d attempts", maxAttempts)
}

// Exposed methods for testing

// CreateJob creates a new OCR job (exposed for testing)
func (o *OCRAdapter) CreateJob(ctx context.Context) (string, error) {
	return o.createJob(ctx)
}

// StartJob starts an existing OCR job (exposed for testing)
func (o *OCRAdapter) StartJob(ctx context.Context, jobID string) error {
	return o.startJob(ctx, jobID)
}

// PollForResult polls for OCR job result (exposed for testing)
func (o *OCRAdapter) PollForResult(ctx context.Context, jobID string) (*providers.OCRResult, error) {
	return o.pollForResult(ctx, jobID)
}

// GetUploadURL gets presigned upload URL (exposed for testing)
func (o *OCRAdapter) GetUploadURL(ctx context.Context, jobID, filename string) (string, error) {
	return o.getUploadURL(ctx, jobID, filename)
}

// UploadFile uploads file to presigned URL (exposed for testing)
func (o *OCRAdapter) UploadFile(ctx context.Context, presignedURL string, fileData []byte, filename string) error {
	return o.uploadFile(ctx, presignedURL, fileData, filename)
}

var _ providers.OCRProvider = (*OCRAdapter)(nil)

// OCRAdapterMock implements OCRProvider for testing
type OCRAdapterMock struct {
	ExtractFunc func(ctx context.Context, input string) (*providers.OCRResult, error)
}

func (m *OCRAdapterMock) Extract(ctx context.Context, input string) (*providers.OCRResult, error) {
	if m.ExtractFunc != nil {
		return m.ExtractFunc(ctx, input)
	}
	return &providers.OCRResult{
		Text:       "mocked text",
		Confidence: 0.95,
		Provider:  "sarvam-mock",
	}, nil
}

var _ providers.OCRProvider = (*OCRAdapterMock)(nil)

// UploadFileViaMultipart uploads a file using multipart/form-data
// This is an alternative method that some storage backends may require
func (o *OCRAdapter) UploadFileViaMultipart(ctx context.Context, presignedURL string, fileData []byte, filename string) error {
	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add file field
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("creating form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return fmt.Errorf("writing file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing writer: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "PUT", presignedURL, &buf)
	if err != nil {
		return fmt.Errorf("creating upload request: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("uploading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// getDownloadURL gets presigned URL for downloading output files
// POST https://api.sarvam.ai/doc-digitization/job/v1/{job_id}/download-files
func (o *OCRAdapter) getDownloadURL(ctx context.Context, jobID string) (string, error) {
	type downloadFileInfo struct {
		FileURL string `json:"file_url"`
	}
	type downloadFilesResponse struct {
		JobID       string `json:"job_id"`
		JobState    string `json:"job_state"`
		DownloadURLs map[string]downloadFileInfo `json:"download_urls"`
	}

	url := fmt.Sprintf("%s/doc-digitization/job/v1/%s/download-files", o.config.BaseURL, jobID)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", o.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("getting download URL: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var result downloadFilesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// Get the first download URL
	for _, download := range result.DownloadURLs {
		if download.FileURL != "" {
			return download.FileURL, nil
		}
	}

	return "", fmt.Errorf("no download URL in response: %s", string(respBody))
}

// downloadAndExtract downloads the ZIP output and extracts text content
// The ZIP contains the processed document (md/html/json files)
func (o *OCRAdapter) downloadAndExtract(ctx context.Context, downloadURL string) (string, error) {
	// Download the ZIP file
	httpReq, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating download request: %w", err)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download failed: %d - %s", resp.StatusCode, string(respBody))
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ZIP data: %w", err)
	}

	// Extract text from ZIP - look for .md, .html, or .json files
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("opening ZIP: %w", err)
	}

	// Priority: .md > .html > .json
	var extractedText string
	
	for _, file := range reader.File {
		name := strings.ToLower(file.Name)
		if strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".markdown") {
			content, err := readZipFile(file)
			if err == nil {
				return content, nil // Return first .md file found
			}
		}
		if extractedText == "" && (strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm")) {
			content, err := readZipFile(file)
			if err == nil {
				extractedText = content
			}
		}
		if extractedText == "" && strings.HasSuffix(name, ".json") {
			content, err := readZipFile(file)
			if err == nil {
				extractedText = content
			}
		}
	}

	if extractedText != "" {
		return extractedText, nil
	}

	return fmt.Sprintf("[ZIP contains %d files, no text content found]", len(reader.File)), nil
}

// readZipFile reads the content of a file inside a ZIP archive
func readZipFile(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return string(content), nil
}