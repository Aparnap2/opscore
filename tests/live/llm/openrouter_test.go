//go:build live

package llm

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/openrouter"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/tests/live"
)

func init() { live.ResetBudget() }

func TestOpenRouterChat(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := openrouter.NewAdapter(openrouter.Config{
		APIKey: apiKey,
		Model:  "tencent/hy3:free",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []providers.ChatMessage{
		{Role: "system", Content: "Respond with exactly one word."},
		{Role: "user", Content: "Say 'approved'"},
	}

	resp, reasoning, err := adapter.Chat(ctx, messages)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp == "" {
		t.Fatal("empty response from LLM")
	}
	t.Logf("Chat response: %s", resp)
	if reasoning != nil {
		t.Logf("Reasoning details present: %s", string(*reasoning))
	}
	live.DebugDump("openrouter_chat", map[string]any{
		"response":  resp,
		"reasoning": reasoning,
	})
}

func TestOpenRouterChatReasoningEnabled(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := openrouter.NewAdapter(openrouter.Config{
		APIKey:           apiKey,
		Model:            "tencent/hy3:free",
		ReasoningEnabled: true,
		MaxTokens:        512,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	messages := []providers.ChatMessage{
		{Role: "system", Content: "You are a compliance analyst for Indian B2B businesses."},
		{Role: "user", Content: "Analyze this vendor risk: GSTIN 22AAAAA0000A1Z5, PAN ABCDE1234F, recent transaction volume ₹50L. Is this vendor high risk? Respond with a single JSON object."},
	}

	resp, reasoning, err := adapter.Chat(ctx, messages)
	if err != nil {
		t.Fatalf("Chat (reasoning) failed: %v", err)
	}
	if resp == "" {
		t.Fatal("empty response from LLM with reasoning")
	}
	t.Logf("Chat response: %s", resp)
	if reasoning != nil {
		t.Logf("Reasoning details: %s", string(*reasoning))
	}

	var parsed any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Logf("Response is not JSON (may be expected): %v", err)
	} else {
		t.Logf("Response is valid JSON: %+v", parsed)
	}
	live.DebugDump("openrouter_reasoning", map[string]any{
		"response":  resp,
		"reasoning": reasoning,
	})
}

func TestOpenRouterExtractFields(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := openrouter.NewAdapter(openrouter.Config{
		APIKey: apiKey,
		Model:  "tencent/hy3:free",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schema := map[string]string{
		"invoice_number": "string",
		"total_amount":   "string",
		"vendor_name":    "string",
		"gstin":          "string",
	}
	text := "Invoice INV-2026-0421 from TechCorp India Pvt Ltd. GSTIN: 27AABCT1234C1ZP. Total: ₹3,25,000.00"

	result, confidence, err := adapter.ExtractFields(ctx, text, schema)
	if err != nil {
		t.Fatalf("ExtractFields failed: %v", err)
	}
	t.Logf("Result: %s", string(result))
	t.Logf("Confidence: %.2f", confidence)
	if len(result) == 0 || string(result) == "{}" || string(result) == "null" {
		t.Error("ExtractFields returned empty result")
	}

	var fields map[string]string
	if err := json.Unmarshal(result, &fields); err == nil {
		if fields["invoice_number"] == "" {
			t.Log("invoice_number field missing (expected for some models)")
		}
		if fields["gstin"] != "" {
			t.Logf("GSTIN extracted: %s", fields["gstin"])
		}
	}
	live.DebugDump("openrouter_extract", map[string]any{
		"result":     string(result),
		"confidence": confidence,
	})
}

func TestOpenRouterReason(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := openrouter.NewAdapter(openrouter.Config{
		APIKey: apiKey,
		Model:  "tencent/hy3:free",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := adapter.Reason(ctx, "Is GSTIN 22AAAAA0000A1Z5 a valid format? Answer yes or no.")
	if err != nil {
		t.Fatalf("Reason failed: %v", err)
	}
	if result == "" {
		t.Fatal("empty response from Reason")
	}
	t.Logf("Reason response: %s", result)
	live.DebugDump("openrouter_reason", map[string]string{"result": result})
}