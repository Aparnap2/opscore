//go:build live

package llm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/sarvam"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/tests/live"
)

func init() { live.ResetBudget() }

func TestSarvamLLMExtractFields(t *testing.T) {
	apiKey := os.Getenv("SARVAM_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := sarvam.NewLLMAdapter(sarvam.LLMConfig{APIKey: apiKey})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schema := map[string]string{
		"invoice_number": "string",
		"total_amount":   "string",
		"vendor_name":    "string",
	}
	text := "Invoice INV-001 from ABC Corp. Total amount: ₹1,50,000.00"

	result, confidence, err := adapter.ExtractFields(ctx, text, schema)
	if err != nil {
		t.Fatalf("ExtractFields failed: %v", err)
	}
	t.Logf("Result: %s", string(result))
	t.Logf("Confidence: %.2f", confidence)
	if len(result) == 0 || string(result) == "{}" || string(result) == "null" {
		t.Error("ExtractFields returned empty result")
	}
	live.DebugDump("llm_extract", map[string]any{
		"result":     string(result),
		"confidence": confidence,
	})
}

func TestSarvamLLMChat(t *testing.T) {
	apiKey := os.Getenv("SARVAM_API_KEY")
	if apiKey == "" {
		t.Skip("SARVAM_API_KEY not set")
	}
	if err := live.ConsumeBudget(); err != nil {
		t.Skipf("Budget exhausted: %v", err)
	}

	adapter := sarvam.NewLLMAdapter(sarvam.LLMConfig{APIKey: apiKey})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []providers.ChatMessage{
		{Role: "system", Content: "Respond with a single word."},
		{Role: "user", Content: "Say 'ok'"},
	}

	resp, _, err := adapter.Chat(ctx, messages)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp == "" {
		t.Fatal("Empty response from LLM")
	}
	t.Logf("Chat response: %s", resp)
	live.DebugDump("llm_chat", map[string]string{"response": resp})
}
