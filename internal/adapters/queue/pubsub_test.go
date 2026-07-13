package queue

import (
	"context"
	"errors"
	"testing"
)

// mockPubSub implements pubsubClient for testing.
type mockPubSub struct {
	publishFunc   func(ctx context.Context, topic string, data []byte) (string, error)
	subscribeFunc func(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error)
	ackFunc       func(ctx context.Context, ackID string) error
	nackFunc      func(ctx context.Context, ackID string) error
}

func (m *mockPubSub) Publish(ctx context.Context, topic string, data []byte) (string, error) {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, topic, data)
	}
	return "mock-id", nil
}

func (m *mockPubSub) Subscribe(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error) {
	if m.subscribeFunc != nil {
		return m.subscribeFunc(ctx, subscription, topic, maxMessages)
	}
	return nil, nil
}

func (m *mockPubSub) Ack(ctx context.Context, ackID string) error {
	if m.ackFunc != nil {
		return m.ackFunc(ctx, ackID)
	}
	return nil
}

func (m *mockPubSub) Nack(ctx context.Context, ackID string) error {
	if m.nackFunc != nil {
		return m.nackFunc(ctx, ackID)
	}
	return nil
}

// ---------- Tests ----------

func TestPubSubAdapter_Enqueue(t *testing.T) {
	var recordedTopic string
	mock := &mockPubSub{
		publishFunc: func(ctx context.Context, topic string, data []byte) (string, error) {
			recordedTopic = topic
			return "server-id-456", nil
		},
	}

	adapter := &PubSubAdapter{client: mock}
	msgID, err := adapter.Enqueue(context.Background(), "test-queue", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if recordedTopic != "test-queue" {
		t.Errorf("expected topic 'test-queue', got %q", recordedTopic)
	}
	if msgID != "server-id-456" {
		t.Errorf("expected message ID 'server-id-456', got %q", msgID)
	}
}

func TestPubSubAdapter_Dequeue(t *testing.T) {
	mock := &mockPubSub{
		subscribeFunc: func(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error) {
			if subscription != "test-queue-sub" {
				t.Errorf("expected subscription 'test-queue-sub', got %q", subscription)
			}
			if topic != "test-queue" {
				t.Errorf("expected topic 'test-queue', got %q", topic)
			}
			return []*pubsubMessage{
				{ID: "msg-1", Data: []byte(`{"hello":"world"}`), DequeueCount: 1},
			}, nil
		},
	}

	adapter := &PubSubAdapter{client: mock}
	msg, err := adapter.Dequeue(context.Background(), "test-queue")
	if err != nil {
		t.Fatalf("Dequeue returned error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected a message, got nil")
	}
	if msg.ID != "msg-1" {
		t.Errorf("expected message ID 'msg-1', got %q", msg.ID)
	}
	if msg.Body != `{"hello":"world"}` {
		t.Errorf("expected body %q, got %q", `{"hello":"world"}`, msg.Body)
	}
	if msg.DequeueCount != 1 {
		t.Errorf("expected DequeueCount 1, got %d", msg.DequeueCount)
	}
}

func TestPubSubAdapter_Dequeue_Empty(t *testing.T) {
	mock := &mockPubSub{
		subscribeFunc: func(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error) {
			return nil, nil
		},
	}

	adapter := &PubSubAdapter{client: mock}
	msg, err := adapter.Dequeue(context.Background(), "test-queue")
	if err != nil {
		t.Fatalf("Dequeue returned error: %v", err)
	}
	if msg != nil {
		t.Errorf("expected nil for empty queue, got %+v", msg)
	}
}

func TestPubSubAdapter_Delete(t *testing.T) {
	var recordedAckID string
	mock := &mockPubSub{
		ackFunc: func(ctx context.Context, ackID string) error {
			recordedAckID = ackID
			return nil
		},
	}

	adapter := &PubSubAdapter{client: mock}
	err := adapter.Delete(context.Background(), "test-queue", "ack-123")
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if recordedAckID != "ack-123" {
		t.Errorf("expected ack ID 'ack-123', got %q", recordedAckID)
	}
}

func TestPubSubAdapter_Poison(t *testing.T) {
	var recordedNackID string
	mock := &mockPubSub{
		nackFunc: func(ctx context.Context, ackID string) error {
			recordedNackID = ackID
			return nil
		},
	}

	adapter := &PubSubAdapter{client: mock}
	err := adapter.Poison(context.Background(), "test-queue", "nack-456")
	if err != nil {
		t.Fatalf("Poison returned error: %v", err)
	}
	if recordedNackID != "nack-456" {
		t.Errorf("expected nack ID 'nack-456', got %q", recordedNackID)
	}
}

func TestPubSubAdapter_Enqueue_Error(t *testing.T) {
	mock := &mockPubSub{
		publishFunc: func(ctx context.Context, topic string, data []byte) (string, error) {
			return "", errors.New("publish failed")
		},
	}

	adapter := &PubSubAdapter{client: mock}
	_, err := adapter.Enqueue(context.Background(), "test-queue", "data")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
