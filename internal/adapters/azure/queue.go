package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/aparna/opscore/internal/providers"
)

type QueueConfig struct {
	ConnectionString string
	AccountName    string
	AccountKey    string
}

type QueueAdapter struct {
	accountName string
	accountURL string
	credential *azqueue.SharedKeyCredential
}

func NewQueueAdapter(config QueueConfig) (*QueueAdapter, error) {
	if config.ConnectionString != "" {
		parts := strings.Split(config.ConnectionString, ";")
		for _, part := range parts {
			if strings.HasPrefix(part, "AccountName=") {
				if config.AccountName == "" {
					config.AccountName = strings.TrimPrefix(part, "AccountName=")
				}
			}
			if strings.HasPrefix(part, "AccountKey=") {
				if config.AccountKey == "" {
					config.AccountKey = strings.TrimPrefix(part, "AccountKey=")
				}
			}
		}
	}

	cred, err := azqueue.NewSharedKeyCredential(config.AccountName, config.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("creating queue credential: %w", err)
	}

	return &QueueAdapter{
		accountName: config.AccountName,
		accountURL:  fmt.Sprintf("https://%s.queue.core.windows.net/", config.AccountName),
		credential:  cred,
	}, nil
}

func (q *QueueAdapter) getClient(queueName string) (*azqueue.QueueClient, error) {
	client, err := azqueue.NewQueueClientWithSharedKeyCredential(
		fmt.Sprintf("%s%s", q.accountURL, queueName),
		q.credential,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("creating queue client: %w", err)
	}
	return client, nil
}

func (q *QueueAdapter) ensureQueue(ctx context.Context, queueName string) (*azqueue.QueueClient, error) {
	client, err := q.getClient(queueName)
	if err != nil {
		return nil, err
	}
	_, err = client.Create(ctx, nil)
	if err != nil && !strings.Contains(err.Error(), "QueueAlreadyExists") {
		return nil, fmt.Errorf("creating queue %s: %w", queueName, err)
	}
	return client, nil
}

func (q *QueueAdapter) Enqueue(ctx context.Context, queueName string, message any) (string, error) {
	client, err := q.ensureQueue(ctx, queueName)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("marshaling message: %w", err)
	}

	resp, err := client.EnqueueMessage(ctx, string(body), nil)
	if err != nil {
		return "", fmt.Errorf("enqueueing message: %w", err)
	}

	if len(resp.Messages) == 0 {
		return "", fmt.Errorf("no message ID returned from queue")
	}

	return *resp.Messages[0].MessageID, nil
}

func (q *QueueAdapter) Dequeue(ctx context.Context, queueName string) (*providers.QueueMessage, error) {
	client, err := q.getClient(queueName)
	if err != nil {
		return nil, err
	}

	resp, err := client.DequeueMessage(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dequeueing message: %w", err)
	}

	if len(resp.Messages) == 0 {
		return nil, nil
	}

	msg := resp.Messages[0]
	popReceipt := ""
	if msg.PopReceipt != nil {
		popReceipt = *msg.PopReceipt
	}

	return &providers.QueueMessage{
		ID:           fmt.Sprintf("%s|%s", *msg.MessageID, popReceipt),
		Body:         *msg.MessageText,
		DequeueCount: int(*msg.DequeueCount),
	}, nil
}

func (q *QueueAdapter) Delete(ctx context.Context, queueName, messageID string) error {
	client, err := q.getClient(queueName)
	if err != nil {
		return err
	}

	msgID, popReceipt := q.parseMessageID(messageID)

	_, err = client.DeleteMessage(ctx, msgID, popReceipt, nil)
	if err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}

	return nil
}

func (q *QueueAdapter) Poison(ctx context.Context, queueName, messageID string) error {
	poisonQueueName := queueName + "-poison"

	poisonClient, err := q.ensureQueue(ctx, poisonQueueName)
	if err != nil {
		return err
	}

	msgID, popReceipt := q.parseMessageID(messageID)

	client, err := q.getClient(queueName)
	if err != nil {
		return err
	}

	getResp, err := client.PeekMessage(ctx, nil)
	if err != nil {
		return fmt.Errorf("peeking message for poison: %w", err)
	}

	if len(getResp.Messages) == 0 {
		return fmt.Errorf("message not found for poison: %s", msgID)
	}

	poisonBody := *getResp.Messages[0].MessageText

	_, err = poisonClient.EnqueueMessage(ctx, poisonBody, nil)
	if err != nil {
		return fmt.Errorf("enqueueing to poison queue: %w", err)
	}

	_, err = client.DeleteMessage(ctx, msgID, popReceipt, nil)
	if err != nil {
		return fmt.Errorf("deleting original message after poison: %w", err)
	}

	return nil
}

func (q *QueueAdapter) parseMessageID(messageID string) (string, string) {
	parts := strings.SplitN(messageID, "|", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return messageID, ""
}

var _ providers.QueueProvider = (*QueueAdapter)(nil)

type QueueAdapterMock struct {
	EnqueueFunc func(ctx context.Context, queueName string, message any) (string, error)
	DequeueFunc func(ctx context.Context, queueName string) (*providers.QueueMessage, error)
	DeleteFunc func(ctx context.Context, queueName, messageID string) error
	PoisonFunc func(ctx context.Context, queueName, messageID string) error
}

func (m *QueueAdapterMock) Enqueue(ctx context.Context, queueName string, message any) (string, error) {
	if m.EnqueueFunc != nil {
		return m.EnqueueFunc(ctx, queueName, message)
	}
	return "", nil
}

func (m *QueueAdapterMock) Dequeue(ctx context.Context, queueName string) (*providers.QueueMessage, error) {
	if m.DequeueFunc != nil {
		return m.DequeueFunc(ctx, queueName)
	}
	return nil, nil
}

func (m *QueueAdapterMock) Delete(ctx context.Context, queueName, messageID string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, queueName, messageID)
	}
	return nil
}

func (m *QueueAdapterMock) Poison(ctx context.Context, queueName, messageID string) error {
	if m.PoisonFunc != nil {
		return m.PoisonFunc(ctx, queueName, messageID)
	}
	return nil
}

var _ providers.QueueProvider = (*QueueAdapterMock)(nil)
