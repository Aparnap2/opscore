package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/providers"
)

// Worker processes queued jobs.
type Worker struct {
	db          providers.DBProvider
	queue       providers.QueueProvider
	docAgent    *agents.DocumentAgent
	signalAgent *agents.SignalAgent
	vendAgent   *agents.VendorAgent
	slack       providers.HITLProvider
	tracer      providers.TracingProvider
}

// Start launches goroutines for each queue.
func (w *Worker) Start(ctx context.Context) {
	go w.pollQueue(ctx, QueueDocument, w.processDocumentJob)
	go w.pollQueue(ctx, QueueVendor, w.processVendorJob)
	go w.pollQueue(ctx, QueueSignal, w.processSignalJob)
	// QueueCompliance can be added later
}

// pollQueue continuously polls a queue and dispatches to handler.
func (w *Worker) pollQueue(ctx context.Context, queueName string, handler func(context.Context, string) error) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("Worker stopped polling", "queue", queueName)
			return
		default:
		}

		msg, err := w.queue.Dequeue(ctx, queueName)
		if err != nil {
			slog.Error("Error dequeueing", "queue", queueName, "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if msg == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		slog.Info("Processing message", "queue", queueName, "id", msg.ID)

		// Wrap handler in a closure with recover to prevent a single panic
		// from crashing the entire server.
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("PANIC in %s handler: %v", queueName, r)
					slog.Error("Recovered from error", "err", err)
				}
			}()
			err = handler(ctx, msg.Body)
		}()
		if err != nil {
			slog.Error("Handler failed — sending to DLQ", "queue", queueName, "id", msg.ID, "err", err)
			if poisonErr := w.queue.Poison(ctx, queueName, msg.ID); poisonErr != nil {
				slog.Error("Failed to poison", "queue", queueName, "id", msg.ID, "err", poisonErr)
			}
		} else {
			if ackErr := w.queue.Delete(ctx, queueName, msg.ID); ackErr != nil {
				slog.Error("Failed to ack", "queue", queueName, "id", msg.ID, "err", ackErr)
			}
		}
	}
}
