package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	callCount int
	mu        sync.Mutex
)

const MaxCalls = 3

type BudgetExhaustedError struct {
	Calls int
}

func (e *BudgetExhaustedError) Error() string {
	return fmt.Sprintf("live test budget exhausted: %d calls used", e.Calls)
}

// ConsumeBudget increments the call counter. Returns error if budget exceeded.
func ConsumeBudget() error {
	mu.Lock()
	defer mu.Unlock()
	callCount++
	if callCount > MaxCalls {
		return &BudgetExhaustedError{Calls: callCount - 1}
	}
	return nil
}

// DebugDump writes raw API artifacts to /tmp/ for debugging.
func DebugDump(tag string, data any) {
	now := time.Now().UnixMilli()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("opscore_live_%s_%d.json", tag, now))
	if b, err := json.MarshalIndent(data, "", "  "); err == nil {
		os.WriteFile(path, b, 0644) //nolint:errcheck
	}
}

// ResetBudget resets the call counter (for testing).
func ResetBudget() {
	mu.Lock()
	defer mu.Unlock()
	callCount = 0
}

// PostJSON sends an authenticated POST request and returns the response body.
func PostJSON(ctx context.Context, url, token string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// GetJSON sends an authenticated GET request and returns the response body.
func GetJSON(ctx context.Context, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if token == "" {
		req.Header.Set("User-Agent", "OpsCore-LiveTest/1.0")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// MeasureLatency wraps a function call and records its duration in milliseconds.
func MeasureLatency(fn func() error) (int64, error) {
	start := time.Now()
	err := fn()
	latency := time.Since(start).Milliseconds()
	return latency, err
}
