package handlers

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
	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/agents"
)

// Regulatory sources for compliance monitoring
var regulatorySources = []struct {
	Name    string
	URL     string
	Type    string // "rss", "html"
	FeedURL string
}{
	{
		Name:    "SEBI",
		URL:     "https://www.sebi.gov.in",
		Type:    "rss",
		FeedURL: "https://www.sebi.gov.in/rss/latest.xml",
	},
	{
		Name:    "RBI",
		URL:     "https://www.rbi.org.in",
		Type:    "html", // RBI doesn't have public RSS
		FeedURL: "https://www.rbi.org.in/Scripts/BS_PressReleaseDisplay.aspx",
	},
	{
		Name:    "GST",
		URL:     "https://www.gst.gov.in",
		Type:    "rss",
		FeedURL: "https://www.gst.gov.in/rss",
	},
}

// TimerScraperHandler handles Timer trigger for regulatory scraper
// Fetches SEBI/RBI/GST RSS feeds, stores in Blob, writes job to Cosmos, enqueues
func TimerScraperHandler(ctx context.Context) error {
	log.Println("Starting regulatory scraper timer")

	// Get adapters
	blobAdapter, err := getBlobAdapter(ctx)
	if err != nil {
		log.Printf("Blob adapter not available: %v", err)
		return err
	}

	queueAdapter, err := getQueueAdapter(ctx)
	if err != nil {
		log.Printf("Queue adapter not available: %v", err)
		return err
	}

	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Cosmos adapter not available: %v", err)
		return err
	}

	now := time.Now()
	tenantID := "system"
	scrapeID := uuid.New().String()

	log.Printf("Starting compliance scrape: id=%s", scrapeID)

	// Create master job for this scrape
	masterJob := &domain.Job{
		ID:           scrapeID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowCompliance,
		Status:       domain.JobStatusProcessing,
		CreatedAt:    now,
		UpdatedAt:    now,
		Input:        map[string]string{"sources": "SEBI,RBI,GST"},
	}
	cosmosAdapter.UpsertJob(ctx, masterJob)

	var successCount, failCount int

	// Scrape each source
	for _, source := range regulatorySources {
		log.Printf("Scraping: %s from %s", source.Name, source.FeedURL)

		sourceID := uuid.New().String()
		scrapeResult, err := scrapeSource(ctx, source)
		if err != nil {
			log.Printf("Failed to scrape %s: %v", source.Name, err)
			failCount++
			continue
		}

		// Store raw content in Blob
		blobPath := fmt.Sprintf("compliance/%d/%s/%s.json", now.Unix(), source.Name, sourceID)
		content, _ := json.Marshal(scrapeResult)
		blobURL, err := blobAdapter.Upload(ctx, "compliance", blobPath, strings.NewReader(string(content)), "application/json")
		if err != nil {
			log.Printf("Failed to store compliance data for %s: %v", source.Name, err)
			failCount++
			continue
		}

		// Create job for compliance processing
		complianceJob := &agents.ComplianceJob{
			TenantID:      tenantID,
			JobID:         sourceID,
			SourceName:    source.Name,
			SourceURL:     source.URL,
			BlobURL:       blobURL,
			Content:       scrapeResult.Content,
			Items:         scrapeResult.Items,
			JobType:       "scrape",
			CorrelationID: scrapeID,
		}

		if _, err := queueAdapter.Enqueue(ctx, QueueCompliance, complianceJob); err != nil {
			log.Printf("Failed to enqueue compliance job for %s: %v", source.Name, err)
			failCount++
			continue
		}

		successCount++
		log.Printf("Successfully queued compliance job for %s: %d items", source.Name, len(scrapeResult.Items))
	}

	// Update master job status
	masterJob.Status = domain.JobStatusCompleted
	masterJob.UpdatedAt = time.Now()
	masterJob.Output = map[string]int{
		"success_count": successCount,
		"fail_count":    failCount,
		"total_sources": len(regulatorySources),
	}
	cosmosAdapter.UpsertJob(ctx, masterJob)

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:       "timer_scraper",
		Action:      "SCRAPE_COMPLETED",
		TargetType:  "job",
		TargetID:    scrapeID,
		NewState:    "COMPLETED",
		Timestamp:   now,
	})

	log.Printf("Compliance scrape completed: id=%s, success=%d, failed=%d", scrapeID, successCount, failCount)
	return nil
}

// ScrapeResult holds the result of scraping a regulatory source
type ScrapeResult struct {
	Source   string          `json:"source"`
	URL      string          `json:"url"`
	ScrapedAt time.Time      `json:"scraped_at"`
	Content  string          `json:"content,omitempty"`
	Items    []agents.ComplianceItem `json:"items"`
}

// scrapeSource fetches and parses a regulatory source
func scrapeSource(ctx context.Context, source struct {
	Name    string
	URL     string
	Type    string
	FeedURL string
}) (*ScrapeResult, error) {
	result := &ScrapeResult{
		Source:   source.Name,
		URL:      source.URL,
		ScrapedAt: time.Now(),
	}

	var resp *http.Response
	var err error

	// Try RSS first
	if source.Type == "rss" {
		resp, err = fetchWithTimeout(ctx, source.FeedURL)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				result.Items, err = parseRSS(resp.Body)
				if err == nil {
					return result, nil
				}
			}
		}
	}

	// Fallback to HTML scraping
	if source.Type == "html" || len(result.Items) == 0 {
		resp, err = fetchWithTimeout(ctx, source.FeedURL)
		if err != nil {
			return nil, fmt.Errorf("fetch failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		result.Content = string(content)
		result.Items = parseHTMLItems(string(content), source.Name)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no items found")
	}

	return result, nil
}

// fetchWithTimeout performs an HTTP GET with timeout
func fetchWithTimeout(ctx context.Context, url string) (*http.Response, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OpsCore-Compliance-Scraper/1.0")
	return client.Do(req)
}

// parseRSS parses RSS feed XML (simplified)
func parseRSS(body io.Reader) ([]agents.ComplianceItem, error) {
	// Simplified RSS parsing
	// In production, use xml.Unmarshal or a library like go-pkg-xmlx
	content, _ := io.ReadAll(body)
	xml := string(content)

	// Basic extraction of items from RSS
	var items []agents.ComplianceItem

	// Very simple parser - in production use proper XML parsing
	// This is a stub that extracts titles between <item> and </item>
	// Or <entry> and </entry>
	itemMarkers := []string{"<item>", "<entry>"}
	for _, marker := range itemMarkers {
		parts := strings.Split(xml, marker)
		for i := 1; i < len(parts); i++ {
			part := parts[i]
			endIdx := strings.Index(part, "</item>")
			if endIdx == -1 {
				endIdx = strings.Index(part, "</entry>")
			}
			if endIdx > 0 {
				itemContent := part[:endIdx]
				title := extractTag(itemContent, "title")
				link := extractTag(itemContent, "link")
				if title != "" {
					items = append(items, agents.ComplianceItem{
						Title:       title,
						Link:        link,
						Description: extractTag(itemContent, "description"),
					})
				}
			}
		}
	}

	return items, nil
}

// extractTag extracts content between XML tags
func extractTag(xml, tag string) string {
	start := strings.Index(xml, "<"+tag+">")
	if start == -1 {
		start = strings.Index(xml, "<"+tag+" ")
	}
	if start == -1 {
		return ""
	}
	start += len(tag) + 2

	end := strings.Index(xml, "</"+tag+">")
	if end == -1 {
		// Self-closing tag
		end = strings.Index(xml, "/>")
		if end == -1 {
			return ""
		}
	}
	if end < start {
		return ""
	}

	content := xml[start:end]
	// Strip CDATA if present
	if strings.HasPrefix(content, "<![CDATA[") {
		content = strings.TrimPrefix(content, "<![CDATA[")
		content = strings.TrimSuffix(content, "]]>")
	}
	return strings.TrimSpace(content)
}

// parseHTMLItems extracts compliance items from HTML (simplified)
func parseHTMLItems(html, source string) []agents.ComplianceItem {
	var items []agents.ComplianceItem

	// Very simple pattern matching for links
	// In production, use proper HTML parsing
	linkIdx := strings.Index(html, `<a href="`)
	for linkIdx >= 0 {
		linkIdx += 9
		linkEnd := strings.Index(html[linkIdx:], `"`)
		if linkEnd > 0 {
			link := html[linkIdx : linkIdx+linkEnd]
			// Look for title nearby
			titleStart := strings.LastIndex(html[:linkIdx], ">")
			if titleStart > 0 {
				titleEnd := strings.Index(html[titleStart:], "<")
				if titleEnd > 0 {
					title := html[titleStart+1 : titleStart+titleEnd]
					if len(title) > 5 && len(title) < 200 {
						items = append(items, agents.ComplianceItem{
							Title: title,
							Link:  link,
						})
					}
				}
			}
		}
		linkIdx = strings.Index(html[linkIdx:], `<a href="`)
	}

	// Limit to recent items
	if len(items) > 20 {
		items = items[:20]
	}

	return items
}

// TimerScraperHandlerCron provides a cron-compatible timer signature
func TimerScraperHandlerCron(ctx context.Context, timerTriggerFunc interface{}) error {
	// Azure Functions timer trigger calls this function
	return TimerScraperHandler(ctx)
}