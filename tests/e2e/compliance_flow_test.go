//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestComplianceMetricsEndpoint(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(apiBase + "/compliance/recent")
	if err != nil {
		t.Fatalf("Compliance recent failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	t.Logf("Compliance response: %v", result)
}

func TestMetricsEndpoints(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	endpoints := []string{
		"/metrics/llm-summary",
		"/metrics/workflow-summary",
	}

	for _, ep := range endpoints {
		resp, err := client.Get(apiBase + ep)
		if err != nil {
			t.Errorf("GET %s failed: %v", ep, err)
			continue
		}
		if resp.StatusCode != 200 {
			t.Errorf("GET %s expected 200, got %d", ep, resp.StatusCode)
		}
		resp.Body.Close()
		t.Logf("GET %s: 200 OK", ep)
	}
}
