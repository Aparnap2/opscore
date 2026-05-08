package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ComplianceAgent handles compliance monitoring workflows
type ComplianceAgent struct {
	db         providers.DBProvider
	llm        providers.LLMProvider
	validator  *domain.IndiaValidator
	httpClient *http.Client
}

// ComplianceJob represents a compliance processing job
type ComplianceJob struct {
	TenantID      string           `json:"tenant_id"`
	JobID         string           `json:"job_id"`
	SourceName   string           `json:"source_name,omitempty"`
	SourceURL    string           `json:"source_url,omitempty"`
	BlobURL      string           `json:"blob_url,omitempty"`
	Content      string           `json:"content,omitempty"`
	JobType      string           `json:"job_type"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Items        []ComplianceItem `json:"items,omitempty"`
}

// ComplianceItem represents a compliance update item
type ComplianceItem struct {
	Title       string    `json:"title"`
	Link        string    `json:"link"`
	Description string    `json:"description,omitempty"`
	Published   time.Time `json:"published"`
	Guid        string    `json:"guid,omitempty"`
}

// NewComplianceAgent creates a new compliance agent
func NewComplianceAgent(
	db providers.DBProvider,
	validator *domain.IndiaValidator,
) *ComplianceAgent {
	return &ComplianceAgent{
		db:         db,
		validator:  validator,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// ComplianceSource represents a regulatory source
type ComplianceSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Type string `json:"type"` // rss, html, api
}

// ScrapeResult represents the result of a compliance scrape
type ScrapeResult struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	URL       string    `json:"url"`
	Content   string    `json:"content"`
	Chunks    []string  `json:"chunks"`
}

// FetchRegulatoryUpdate fetches updates from a regulatory source
func (a *ComplianceAgent) FetchRegulatoryUpdate(ctx context.Context, source *ComplianceSource) (*ScrapeResult, error) {
	// Simple HTTP fetch and extract
	resp, err := a.httpClient.Get(source.URL)
	if err != nil {
		return nil, fmt.Errorf("fetching source: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	result := &ScrapeResult{
		Source:    source.Name,
		URL:       source.URL,
		Content:   string(body),
		Timestamp: time.Now(),
	}

	// Simple chunking (split by paragraphs)
	result.Chunks = a.chunkText(string(body), 500)

	return result, nil
}

// chunkText splits text into chunks of approximately the given size
func (a *ComplianceAgent) chunkText(text string, size int) []string {
	// Simple word-based chunking
	words := strings.Fields(text)
	var chunks []string
	var current []string
	currentLen := 0

	for _, word := range words {
		current = append(current, word)
		currentLen += len(word) + 1

		if currentLen >= size {
			chunks = append(chunks, strings.Join(current, " "))
			current = nil
			currentLen = 0
		}
	}

	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, " "))
	}

	return chunks
}

// StoreComplianceChunks stores extracted chunks
func (a *ComplianceAgent) StoreComplianceChunks(ctx context.Context, result *ScrapeResult) error {
	for i, chunk := range result.Chunks {
		doc := &domain.Document{
			ID:          fmt.Sprintf("%s-%s-%d", result.Source, result.Timestamp.Format("20060102"), i),
			TenantID:    "compliance",
			JobID:       "scrape",
			Type:        "REGULATORY",
			FileName:    fmt.Sprintf("%s-chunk-%d", result.Source, i),
			StoragePath: result.URL,
			Status:      "INDEXED",
			Extracted:   map[string]any{"chunk": chunk},
			CreatedAt:   time.Now(),
		}

		if err := a.db.UpsertDocument(ctx, doc); err != nil {
			return fmt.Errorf("storing chunk: %w", err)
		}
	}

	return nil
}

// AnalyzeCompliance performs gap analysis using LLM
func (a *ComplianceAgent) AnalyzeCompliance(ctx context.Context, policyContext string, updates []string) (string, error) {
	if a.llm == nil {
		return "LLM not configured", nil
	}

	// Build prompt
	var sb strings.Builder
	sb.WriteString("Analyze the following regulatory updates against this policy context:\n\n")
	sb.WriteString("Policy: ")
	sb.WriteString(policyContext)
	sb.WriteString("\n\nUpdates:\n")

	for i, update := range updates {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, update))
	}

	sb.WriteString("\nProvide a gap analysis with citations.")

	reasoning, err := a.llm.Reason(ctx, sb.String())
	if err != nil {
		return "", fmt.Errorf("LLM analysis failed: %w", err)
	}

	return reasoning, nil
}

// DetectChanges detects meaningful changes from previous content
func (a *ComplianceAgent) DetectChanges(ctx context.Context, oldContent, newContent string) ([]string, error) {
	// Simple diff detection
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var changes []string
	oldSet := make(map[string]bool)
	for _, line := range oldLines {
		oldSet[strings.TrimSpace(line)] = true
	}

	for _, line := range newLines {
		line = strings.TrimSpace(line)
		if line != "" && !oldSet[line] {
			changes = append(changes, line)
		}
	}

	return changes, nil
}

// ClassifySeverity determines the severity of a compliance update
func (a *ComplianceAgent) ClassifySeverity(ctx context.Context, title, content string) (string, error) {
	title = strings.ToLower(title)
	content = strings.ToLower(content)

	highRisk := []string{"penalty", "fine", "non-compliance", "mandatory", "immediate"}
	mediumRisk := []string{"notice", "advisory", "guideline", "recommend"}

	for _, kw := range highRisk {
		if strings.Contains(title, kw) || strings.Contains(content, kw) {
			return "HIGH", nil
		}
	}

	for _, kw := range mediumRisk {
		if strings.Contains(title, kw) || strings.Contains(content, kw) {
			return "MEDIUM", nil
		}
	}

	return "LOW", nil
}

// CreateTicket creates a compliance ticket
func (a *ComplianceAgent) CreateTicket(ctx context.Context, tenantID, title, description, severity string) error {
	ticket := &domain.HITLRequest{
		ID:        fmt.Sprintf("ticket-%d", time.Now().Unix()),
		TenantID:  tenantID,
		JobID:     title,
		Type:      "COMPLIANCE_TICKET",
		Message:   description,
		Status:    severity, // Use as severity marker
		CreatedAt: time.Now(),
	}

	return a.db.UpsertHITLRequest(ctx, ticket)
}

// ProcessCompliance handles the complete compliance workflow
func (a *ComplianceAgent) ProcessCompliance(ctx context.Context, job *ComplianceJob) (map[string]any, error) {
	result := map[string]any{
		"source_name": job.SourceName,
		"job_type":    job.JobType,
	}

	// Process based on job type
	switch job.JobType {
	case "scrape":
		// Store compliance items as chunks
		if len(job.Items) > 0 {
			for i, item := range job.Items {
				doc := &domain.Document{
					ID:          fmt.Sprintf("compliance-%s-%d", job.JobID, i),
					TenantID:    job.TenantID,
					JobID:       job.JobID,
					Type:        "REGULATORY",
					FileName:    item.Title,
					StoragePath: item.Link,
					Status:      "INDEXED",
					Extracted: map[string]any{
						"title":       item.Title,
						"description": item.Description,
						"link":        item.Link,
						"published":   item.Published,
					},
					CreatedAt: time.Now(),
				}

				if err := a.db.UpsertDocument(ctx, doc); err != nil {
					return nil, fmt.Errorf("storing compliance document: %w", err)
				}
			}
			result["items_processed"] = len(job.Items)
		}

		// If content provided, chunk it
		if job.Content != "" {
			chunks := a.chunkText(job.Content, 500)
			result["chunks_created"] = len(chunks)

			for i, chunk := range chunks {
				doc := &domain.Document{
					ID:          fmt.Sprintf("chunk-%s-%d", job.JobID, i),
					TenantID:    job.TenantID,
					JobID:       job.JobID,
					Type:        "REGULATORY_CHUNK",
					FileName:    fmt.Sprintf("chunk-%d", i),
					StoragePath: job.BlobURL,
					Status:      "INDEXED",
					Extracted:   map[string]any{"chunk": chunk},
					CreatedAt:   time.Now(),
				}

				if err := a.db.UpsertDocument(ctx, doc); err != nil {
					log.Printf("Failed to store chunk %d: %v", i, err)
				}
			}
		}

	case "analyze":
		// Run gap analysis
		if a.llm != nil {
			items := make([]string, len(job.Items))
			for i, item := range job.Items {
				items[i] = item.Title
			}

			gapAnalysis, err := a.AnalyzeCompliance(ctx, "default policy", items)
			if err == nil {
				result["gap_analysis"] = gapAnalysis
			}
		}

	default:
		result["status"] = "unknown job type"
	}

	result["status"] = "completed"
	return result, nil
}

// ComplianceAgentTools returns the list of tools available to this agent
func (a *ComplianceAgent) ComplianceAgentTools() []string {
	return []string{
		"fetch_regulatory_update",
		"store_compliance_chunks",
		"analyze_compliance",
		"detect_changes",
		"classify_severity",
		"create_ticket",
		"process_compliance",
	}
}

// Stub for json marshal usage
var _ = json.Marshal