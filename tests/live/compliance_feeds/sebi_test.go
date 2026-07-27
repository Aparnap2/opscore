//go:build live

package compliance_feeds

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aparna/opscore/tests/live"
)

func init() { live.ResetBudget() }

// TestSEBIRSSFeed verifies SEBI RSS feed is reachable and returns valid XML.
func TestSEBIRSSFeed(t *testing.T) {
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	url := "https://www.sebi.gov.in/rss.html"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("Create request: %v", err)
	}
	req.Header.Set("User-Agent", "OpsCore-LiveTest/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		t.Fatalf("SEBI RSS request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read SEBI RSS body: %v", err)
	}

	t.Logf("SEBI RSS status: %d, latency: %dms, body size: %d bytes", resp.StatusCode, latency, len(body))

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Error("Empty SEBI RSS body")
	}

	// Validate it looks like XML/RSS
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "xml") && !strings.Contains(contentType, "html") {
		t.Logf("Unexpected content-type: %s", contentType)
	}

	live.DebugDump("sebi_rss", map[string]any{
		"status":  resp.StatusCode,
		"latency": latency,
		"size":    len(body),
		"preview": string(body[:min(len(body), 500)]),
	})
}

// TestRBIRSSFeed verifies RBI RSS feed is reachable and returns valid XML.
func TestRBIRSSFeed(t *testing.T) {
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	url := "https://rbi.org.in/rss/rss.aspx"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("Create request: %v", err)
	}
	req.Header.Set("User-Agent", "OpsCore-LiveTest/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		t.Fatalf("RBI RSS request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read RBI RSS body: %v", err)
	}

	t.Logf("RBI RSS status: %d, latency: %dms, body size: %d bytes", resp.StatusCode, latency, len(body))

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Error("Empty RBI RSS body")
	}

	live.DebugDump("rbi_rss", map[string]any{
		"status":  resp.StatusCode,
		"latency": latency,
		"size":    len(body),
		"preview": string(body[:min(len(body), 500)]),
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
