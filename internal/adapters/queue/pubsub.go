package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/aparna/opscore/internal/providers"
)

// pubsubClient abstracts the core Pub/Sub operations so the adapter can be
// tested without a real Pub/Sub emulator.
type pubsubClient interface {
	// Publish publishes data to topic and returns a server-assigned message ID.
	Publish(ctx context.Context, topic string, data []byte) (string, error)
	// Subscribe pulls up to maxMessages messages from the given subscription
	// (attached to the specified topic for auto-creation).
	Subscribe(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error)
	// Ack acknowledges a message by its ack ID.
	Ack(ctx context.Context, ackID string) error
	// Nack negatively acknowledges a message by its ack ID.
	Nack(ctx context.Context, ackID string) error
}

// pubsubMessage is the internal representation of a Pub/Sub message.
type pubsubMessage struct {
	ID           string
	Data         []byte
	DequeueCount int
}

// PubSubAdapter implements providers.QueueProvider using Google Cloud Pub/Sub.
type PubSubAdapter struct {
	client pubsubClient
}

// messageHandle stores ack/nack closures for a pulled message.
type messageHandle struct {
	ack  func()
	nack func()
}

// NewPubSubAdapter creates a PubSubAdapter backed by a real Google Cloud
// Pub/Sub client for the given GCP project.
func NewPubSubAdapter(ctx context.Context, projectID string) (*PubSubAdapter, error) {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("creating pubsub client: %w", err)
	}
	real := &realPubSubClient{
		client:  client,
		msgRefs: make(map[string]messageHandle),
	}
	return &PubSubAdapter{
		client: real,
	}, nil
}

// ---------------------------------------------------------------------------
// providers.QueueProvider implementation
// ---------------------------------------------------------------------------

// Enqueue marshals the message to JSON and publishes it to the topic named
// after queueName. Returns the server-assigned message ID.
func (a *PubSubAdapter) Enqueue(ctx context.Context, queueName string, message any) (string, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("marshaling message: %w", err)
	}

	id, err := a.client.Publish(ctx, queueName, data)
	if err != nil {
		return "", fmt.Errorf("publishing to topic %s: %w", queueName, err)
	}
	return id, nil
}

// Dequeue pulls a single message from the subscription named queueName-sub
// (auto-creating it if needed). Returns nil when no messages are available.
func (a *PubSubAdapter) Dequeue(ctx context.Context, queueName string) (*providers.QueueMessage, error) {
	subName := queueName + "-sub"

	msgs, err := a.client.Subscribe(ctx, subName, queueName, 1)
	if err != nil {
		return nil, fmt.Errorf("subscribing to %s: %w", subName, err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	msg := msgs[0]
	return &providers.QueueMessage{
		ID:           msg.ID,
		Body:         string(msg.Data),
		DequeueCount: msg.DequeueCount,
	}, nil
}

// Delete acknowledges a message by its ack ID, removing it from the
// subscription.
func (a *PubSubAdapter) Delete(ctx context.Context, queueName, messageID string) error {
	return a.client.Ack(ctx, messageID)
}

// Poison negatively acknowledges a message, making it available for
// redelivery.
func (a *PubSubAdapter) Poison(ctx context.Context, queueName, messageID string) error {
	return a.client.Nack(ctx, messageID)
}

// ---------------------------------------------------------------------------
// realPubSubClient — wraps cloud.google.com/go/pubsub.Client
// ---------------------------------------------------------------------------

// realPubSubClient adapts *pubsub.Client to the pubsubClient interface.
type realPubSubClient struct {
	client  *pubsub.Client
	mu      sync.Mutex
	msgRefs map[string]messageHandle
}

// Publish ensures the topic exists, then publishes data and returns the
// server-assigned message ID.
func (r *realPubSubClient) Publish(ctx context.Context, topicName string, data []byte) (string, error) {
	topic := r.client.Topic(topicName)
	exists, err := topic.Exists(ctx)
	if err != nil {
		return "", fmt.Errorf("checking topic %s: %w", topicName, err)
	}
	if !exists {
		var createErr error
		topic, createErr = r.client.CreateTopic(ctx, topicName)
		if createErr != nil {
			return "", fmt.Errorf("creating topic %s: %w", topicName, createErr)
		}
	}

	result := topic.Publish(ctx, &pubsub.Message{Data: data})
	id, err := result.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("publish result: %w", err)
	}
	return id, nil
}

// Subscribe ensures the subscription exists and pulls up to maxMessages
// messages.
func (r *realPubSubClient) Subscribe(ctx context.Context, subName, topicName string, maxMessages int) ([]*pubsubMessage, error) {
	sub := r.client.Subscription(subName)
	exists, err := sub.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking subscription %s: %w", subName, err)
	}
	if !exists {
		topic := r.client.Topic(topicName)
		sub, err = r.client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
			Topic: topic,
		})
		if err != nil {
			return nil, fmt.Errorf("creating subscription %s: %w", subName, err)
		}
	}

	var mu sync.Mutex
	var msgs []*pubsubMessage

	pullCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = sub.Receive(pullCtx, func(ctx context.Context, m *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()

		id := m.ID
		deliveryAttempt := 0
		if m.DeliveryAttempt != nil {
			deliveryAttempt = *m.DeliveryAttempt
		}

		msgs = append(msgs, &pubsubMessage{
			ID:           id,
			Data:         m.Data,
			DequeueCount: deliveryAttempt,
		})

		// Store ack/nack handles for later use by Ack/Nack.
		r.mu.Lock()
		r.msgRefs[id] = messageHandle{ack: m.Ack, nack: m.Nack}
		r.mu.Unlock()

		// Do NOT ack — ownership transfers to the caller.
		cancel()
	})
	if err != nil && err != context.Canceled {
		return nil, fmt.Errorf("receiving messages: %w", err)
	}

	// Drain any remaining msg refs that were never explicitly acked/nacked
	// (e.g. when the context was cancelled mid-receive). Nacking orphans
	// prevents the memory leak and tells Pub/Sub to redeliver them.
	r.mu.Lock()
	for id, h := range r.msgRefs {
		h.nack()
		delete(r.msgRefs, id)
	}
	r.mu.Unlock()

	return msgs, nil
}

// Ack looks up the stored message handle, deletes it from the refs map, and
// calls Ack on the handle.
func (r *realPubSubClient) Ack(ctx context.Context, ackID string) error {
	r.mu.Lock()
	h, ok := r.msgRefs[ackID]
	delete(r.msgRefs, ackID)
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("message %s not found or already acknowledged", ackID)
	}
	h.ack()
	return nil
}

// Nack looks up the stored message handle, deletes it from the refs map, and
// calls Nack on the handle.
func (r *realPubSubClient) Nack(ctx context.Context, ackID string) error {
	r.mu.Lock()
	h, ok := r.msgRefs[ackID]
	delete(r.msgRefs, ackID)
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("message %s not found or already acknowledged", ackID)
	}
	h.nack()
	return nil
}

// Compile-time check: PubSubAdapter satisfies providers.QueueProvider.
var _ providers.QueueProvider = (*PubSubAdapter)(nil)
