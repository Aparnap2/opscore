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

// RedisAdapter implements providers.QueueProvider using Redis lists.
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

// Enqueue marshals the message to JSON and pushes it to the left side of the list.
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

	if err := r.client.LPush(ctx, queueName, wrapperJSON).Err(); err != nil {
		return "", fmt.Errorf("enqueueing message: %w", err)
	}

	return msgID, nil
}

// Dequeue blocks and retrieves a message from the right side of the list.
func (r *RedisAdapter) Dequeue(ctx context.Context, queueName string) (*providers.QueueMessage, error) {
	result, err := r.client.BRPop(ctx, 30*time.Second, queueName).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("dequeueing message: %w", err)
	}

	// BRPop returns [queueName, value].
	if len(result) < 2 {
		return nil, nil
	}

	raw := result[1]

	var wrapper struct {
		ID   string          `json:"id"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		// Legacy format — raw JSON without wrapper.
		return &providers.QueueMessage{
			ID:   "",
			Body: raw,
		}, nil
	}

	return &providers.QueueMessage{
		ID:   wrapper.ID,
		Body: string(wrapper.Body),
	}, nil
}

// Delete removes a message from the queue by matching its wrapper content.
func (r *RedisAdapter) Delete(ctx context.Context, queueName, messageID string) error {
	// Scan the list for a message with the matching ID and remove it.
	// LRem removes all occurrences of the exact value, so we need to find it first.
	// We use LRange + iterate to find and build the exact string to remove.
	listLen, err := r.client.LLen(ctx, queueName).Result()
	if err != nil {
		return fmt.Errorf("getting list length: %w", err)
	}

	// Read in chunks to avoid pulling the entire list into memory.
	batchSize := int64(100)
	for start := int64(0); start < listLen; start += batchSize {
		end := start + batchSize - 1
		if end >= listLen {
			end = listLen - 1
		}

		items, err := r.client.LRange(ctx, queueName, start, end).Result()
		if err != nil {
			return fmt.Errorf("reading list range: %w", err)
		}

		for _, item := range items {
			var wrapper struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(item), &wrapper) == nil && wrapper.ID == messageID {
				if err := r.client.LRem(ctx, queueName, 1, item).Err(); err != nil {
					return fmt.Errorf("removing message %s: %w", messageID, err)
				}
				return nil
			}
		}
	}

	return fmt.Errorf("message %s not found in queue %s", messageID, queueName)
}

// Poison moves a message from the original queue to a poison queue.
func (r *RedisAdapter) Poison(ctx context.Context, queueName, messageID string) error {
	poisonQueue := queueName + "-poison"

	listLen, err := r.client.LLen(ctx, queueName).Result()
	if err != nil {
		return fmt.Errorf("getting list length: %w", err)
	}

	batchSize := int64(100)
	for start := int64(0); start < listLen; start += batchSize {
		end := start + batchSize - 1
		if end >= listLen {
			end = listLen - 1
		}

		items, err := r.client.LRange(ctx, queueName, start, end).Result()
		if err != nil {
			return fmt.Errorf("reading list range: %w", err)
		}

		for _, item := range items {
			var wrapper struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(item), &wrapper) == nil && wrapper.ID == messageID {
				// Remove from original queue.
				if err := r.client.LRem(ctx, queueName, 1, item).Err(); err != nil {
					return fmt.Errorf("removing message from queue: %w", err)
				}
				// Push to poison queue.
				if err := r.client.LPush(ctx, poisonQueue, item).Err(); err != nil {
					return fmt.Errorf("pushing to poison queue: %w", err)
				}
				return nil
			}
		}
	}

	return fmt.Errorf("message %s not found in queue %s", messageID, queueName)
}

// Compile-time interface check.
var _ providers.QueueProvider = (*RedisAdapter)(nil)
