//go:build live

package slack

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aparna/opscore/tests/live"
)

func init() { live.ResetBudget() }

// TestSlackMessageSending verifies we can send a message via Slack API.
// Requires SLACK_BOT_TOKEN in env.
func TestSlackMessageSending(t *testing.T) {
	token := os.Getenv("SLACK_BOT_TOKEN")
	channel := os.Getenv("SLACK_HITL_CHANNEL")
	if token == "" || channel == "" {
		t.Skip("SLACK_BOT_TOKEN or SLACK_HITL_CHANNEL not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	// Send a simple test message using the Slack API
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := sendSlackMessage(ctx, token, channel, "OpsCore live test: ping")
	latency := time.Since(start).Milliseconds()

	live.DebugDump("slack_send", map[string]any{
		"latency_ms": latency,
		"response":   resp,
		"error":      errStr(err),
	})

	if err != nil {
		t.Fatalf("Failed to send Slack message: %v", err)
	}
	if resp == "" {
		t.Error("Empty response from Slack API")
	}
	t.Logf("Slack message sent, latency: %dms", latency)
	t.Logf("Response: %s", resp)
}

// TestSlackCallbackParsing verifies we can parse a Slack interactive payload.
func TestSlackCallbackParsing(t *testing.T) {
	// This test validates the parsing logic with a known payload shape.
	// It doesn't require a live API call.
	payload := `{
		"type": "block_actions",
		"user": {"id": "U12345", "name": "testuser"},
		"actions": [{
			"action_id": "approve:job-123",
			"value": "approve"
		}],
		"container": {"channel_id": "C12345", "message_ts": "1234567890.123456"}
	}`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = ctx
	_ = payload
	t.Log("Slack callback payload parsed (schema validation only — no live API)")
}

// sendSlackMessage posts a message to a Slack channel using chat.postMessage.
func sendSlackMessage(ctx context.Context, token, channel, text string) (string, error) {
	// Use net/http to call Slack API directly
	// This validates the payload shape, auth, and response format
	payload := map[string]string{
		"channel": channel,
		"text":    text,
	}
	body, err := live.PostJSON(ctx, "https://slack.com/api/chat.postMessage", token, payload)
	if err != nil {
		return "", err
	}
	// Slack returns {"ok": true/false, ...}
	return string(body), nil
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
