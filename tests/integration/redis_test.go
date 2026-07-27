package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/queue"
	"github.com/aparna/opscore/internal/providers"
)

func TestRedisQueueAdapter(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	ctx := context.Background()
	adapter, err := queue.NewRedisAdapter(addr, "", 1)
	if err != nil {
		t.Skip("REDIS_ADDR not set or redis unavailable; skipping")
	}
	defer adapter.Close()
	t.Log("✓ NewRedisAdapter OK")

	queueName := "test-queue-int"

	// Test Enqueue
	msgID, err := adapter.Enqueue(ctx, queueName, map[string]string{"hello": "world"})
	if err != nil {
		t.Skip("REDIS_ADDR not set or redis unavailable; skipping")
	}
	if msgID == "" {
		t.Fatal("Enqueue returned empty message ID")
	}
	t.Logf("✓ Enqueue OK: id=%s", msgID)

	// Test Dequeue - use a short timeout context to avoid hanging
	dqCtx, dqCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dqCancel()
	msg, err := adapter.Dequeue(dqCtx, queueName)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if msg == nil {
		t.Fatal("Dequeue returned nil message")
	}
	t.Logf("✓ Dequeue OK: id=%s, body=%s", msg.ID, msg.Body)

	t.Log("\n✅ All Redis integration tests passed")

	var _ providers.QueueProvider = adapter
}
