package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/aparna/opscore/internal/providers"
)

// RedisAdapter implements providers.QueueProvider using Redis lists
// with a hash for O(1) message lookup on Delete/Poison.
type RedisAdapter struct {
	client *redis.Client
}

// NewRedisAdapter creates a new Redis queue adapter.
func NewRedisAdapter(addr, password string, db int) (*RedisAdapter, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// Verify connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connecting to redis: %w", err)
	}

	return &RedisAdapter{client: client}, nil
}

// Ping verifies Redis connectivity.
func (r *RedisAdapter) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close shuts down the Redis client.
func (r *RedisAdapter) Close() error {
	return r.client.Close()
}

// Enqueue marshals the message to JSON, stores it in a hash for O(1) lookup,
// and pushes only the message ID to the queue list.
func (r *RedisAdapter) Enqueue(ctx context.Context, queueName string, message any) (string, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("marshaling message: %w", err)
	}

	msgID := uuid.New().String()

	// Wrap in a structure with an ID so we can track it.
	wrapper := map[string]any{
		"id":   msgID,
		"body": json.RawMessage(body),
	}

	wrapperJSON, err := json.Marshal(wrapper)
	if err != nil {
		return "", fmt.Errorf("marshaling wrapper: %w", err)
	}

	msgHashKey := queueName + ":messages"

	// Store full message in hash for O(1) Delete/Poison lookup.
	if err := r.client.HSet(ctx, msgHashKey, msgID, wrapperJSON).Err(); err != nil {
		return "", fmt.Errorf("storing message in hash: %w", err)
	}

	// Push only the message ID to the queue list.
	if err := r.client.LPush(ctx, queueName, msgID).Err(); err != nil {
		return "", fmt.Errorf("enqueueing message: %w", err)
	}

	return msgID, nil
}

// Dequeue blocks and retrieves a message from the queue. Uses BRPopLPush to
// atomically move the message ID from the main queue to a processing list,
// providing lease semantics without losing messages on crash.
func (r *RedisAdapter) Dequeue(ctx context.Context, queueName string) (*providers.QueueMessage, error) {
	processingKey := queueName + ":processing"
	msgHashKey := queueName + ":messages"

	result, err := r.client.BRPopLPush(ctx, queueName, processingKey, 30*time.Second).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("dequeueing message: %w", err)
	}

	// Handle old-format messages (full JSON wrappers) still in the queue
	// during migration. These contain the full body inline.
	var oldWrapper struct {
		ID   string          `json:"id"`
		Body json.RawMessage `json:"body"`
	}
	if json.Unmarshal([]byte(result), &oldWrapper) == nil && oldWrapper.ID != "" {
		// Upgrade: store in hash for future O(1) operations.
		_ = r.client.HSet(ctx, msgHashKey, oldWrapper.ID, result).Err()
		return &providers.QueueMessage{
			ID:   oldWrapper.ID,
			Body: string(oldWrapper.Body),
		}, nil
	}

	// New format — result is the message ID string.
	msgID := result

	wrapperJSON, err := r.client.HGet(ctx, msgHashKey, msgID).Result()
	if err != nil {
		if err == redis.Nil {
			// Hash entry missing — return a minimal message with just the ID.
			return &providers.QueueMessage{ID: msgID}, nil
		}
		return nil, fmt.Errorf("reading message from hash: %w", err)
	}

	var wrapper struct {
		ID   string          `json:"id"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(wrapperJSON), &wrapper); err != nil {
		return &providers.QueueMessage{ID: msgID}, nil
	}

	return &providers.QueueMessage{
		ID:   wrapper.ID,
		Body: string(wrapper.Body),
	}, nil
}

// Delete acknowledges and removes a message. Uses LRem on the processing list
// (typically small — only in-flight messages) for O(processing_size) instead
// of scanning the entire queue. Falls back to the main queue if the message
// was never dequeued.
func (r *RedisAdapter) Delete(ctx context.Context, queueName, messageID string) error {
	processingKey := queueName + ":processing"
	msgHashKey := queueName + ":messages"

	// Remove from processing list (in-flight messages — small set).
	n, err := r.client.LRem(ctx, processingKey, 1, messageID).Result()
	if err != nil {
		return fmt.Errorf("removing from processing list: %w", err)
	}

	// Also try the main queue (message may not have been dequeued yet).
	if n == 0 {
		n, err = r.client.LRem(ctx, queueName, 1, messageID).Result()
		if err != nil {
			return fmt.Errorf("removing from queue: %w", err)
		}
	}

	// Remove from hash regardless.
	if err := r.client.HDel(ctx, msgHashKey, messageID).Err(); err != nil {
		return fmt.Errorf("deleting message from hash: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("message %s not found in queue %s", messageID, queueName)
	}
	return nil
}

// Poison moves a message to the poison queue. O(1) — looks up the full message
// from the hash, removes from the processing list by message ID, and pushes
// the full JSON to the poison queue. The hash entry is cleaned up after the
// poison queue receives the message so DLQ consumers have the full payload.
func (r *RedisAdapter) Poison(ctx context.Context, queueName, messageID string) error {
	poisonQueue := queueName + "-poison"
	processingKey := queueName + ":processing"
	msgHashKey := queueName + ":messages"

	// Look up the full message from the hash.
	wrapperJSON, err := r.client.HGet(ctx, msgHashKey, messageID).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("message %s not found in queue %s", messageID, queueName)
		}
		return fmt.Errorf("reading message from hash: %w", err)
	}

	// Remove from processing list.
	if _, err := r.client.LRem(ctx, processingKey, 1, messageID).Result(); err != nil {
		return fmt.Errorf("removing from processing list: %w", err)
	}

	// Push full message JSON to poison queue (so DLQ consumers have the body).
	if err := r.client.LPush(ctx, poisonQueue, wrapperJSON).Err(); err != nil {
		return fmt.Errorf("pushing to poison queue: %w", err)
	}

	// Remove from hash last — poison queue entry is independent.
	if err := r.client.HDel(ctx, msgHashKey, messageID).Err(); err != nil {
		return fmt.Errorf("deleting message from hash: %w", err)
	}

	return nil
}

// Compile-time interface check.
var _ providers.QueueProvider = (*RedisAdapter)(nil)
