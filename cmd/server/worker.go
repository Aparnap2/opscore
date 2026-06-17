package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// Worker processes queued jobs.
type Worker struct {
	db        providers.DBProvider
	queue     providers.QueueProvider
	docAgent  *agents.DocumentAgent
	vendAgent *agents.VendorAgent
	slack     providers.HITLProvider
	tracer    providers.TracingProvider
}

// Start launches goroutines for each queue.
func (w *Worker) Start(ctx context.Context) {
	go w.pollQueue(ctx, QueueDocument, w.processDocumentJob)
	go w.pollQueue(ctx, QueueVendor, w.processVendorJob)
	// QueueCompliance can be added later
}

// pollQueue continuously polls a queue and dispatches to handler.
func (w *Worker) pollQueue(ctx context.Context, queueName string, handler func(context.Context, string) error) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("Worker stopped polling %s", queueName)
			return
		default:
		}

		msg, err := w.queue.Dequeue(ctx, queueName)
		if err != nil {
			log.Printf("Error dequeueing from %s: %v", queueName, err)
			time.Sleep(2 * time.Second)
			continue
		}
		if msg == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		log.Printf("Processing message from %s: id=%s", queueName, msg.ID)

		// Wrap handler in a closure with recover to prevent a single panic
		// from crashing the entire server.
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("PANIC in %s handler: %v", queueName, r)
					log.Printf("Recovered from %s", err)
				}
			}()
			err = handler(ctx, msg.Body)
		}()
		if err != nil {
			log.Printf("Handler failed for %s/%s: %v — sending to DLQ", queueName, msg.ID, err)
			if poisonErr := w.queue.Poison(ctx, queueName, msg.ID); poisonErr != nil {
				log.Printf("Failed to poison %s/%s: %v", queueName, msg.ID, poisonErr)
			}
		} else {
			if ackErr := w.queue.Delete(ctx, queueName, msg.ID); ackErr != nil {
				log.Printf("Failed to ack %s/%s: %v", queueName, msg.ID, ackErr)
			}
		}
	}
}

// processDocumentJob handles a document ingestion job.
func (w *Worker) processDocumentJob(ctx context.Context, body string) error {
	var job agents.DocumentJob
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return fmt.Errorf("unmarshal document job: %w", err)
	}

	// Update job to PROCESSING
	dbJob := &domain.Job{
		ID:        job.JobID,
		TenantID:  job.TenantID,
		Status:    domain.JobStatusProcessing,
		UpdatedAt: time.Now(),
	}
	if err := w.db.UpsertJob(ctx, dbJob); err != nil {
		log.Printf("Failed to update job %s to PROCESSING: %v", job.JobID, err)
	}

	// Process the document
	result, err := w.docAgent.ProcessDocument(ctx, &job)
	if err != nil {
		// Mark as retryable failed
		failedJob := &domain.Job{
			ID:        job.JobID,
			TenantID:  job.TenantID,
			Status:    domain.JobStatusRetryableFailed,
			Error:     err.Error(),
			UpdatedAt: time.Now(),
		}
		_ = w.db.UpsertJob(ctx, failedJob)
		return err
	}

	// If HITL needed, send Slack approval
	if needsHITL, ok := result["needs_hitl"].(bool); ok && needsHITL {
		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   fmt.Sprintf("Document %s requires approval", job.FileName),
			Status:   "PENDING",
			SentAt:   time.Now(),
		}

		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			log.Printf("Failed to send Slack approval for %s: %v", job.JobID, err)
		} else {
			hitlReq.SentAt = time.Now()
			_ = w.db.UpsertHITLRequest(ctx, hitlReq)
		}
	}

	return nil
}

// processVendorJob handles a vendor onboarding job.
func (w *Worker) processVendorJob(ctx context.Context, body string) error {
	var job agents.VendorJob
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return fmt.Errorf("unmarshal vendor job: %w", err)
	}

	dbJob := &domain.Job{
		ID:        job.JobID,
		TenantID:  job.TenantID,
		Status:    domain.JobStatusProcessing,
		UpdatedAt: time.Now(),
	}
	if err := w.db.UpsertJob(ctx, dbJob); err != nil {
		log.Printf("Failed to update job %s to PROCESSING: %v", job.JobID, err)
	}

	result, err := w.vendAgent.ProcessVendor(ctx, &job)
	if err != nil {
		failedJob := &domain.Job{
			ID:        job.JobID,
			TenantID:  job.TenantID,
			Status:    domain.JobStatusRetryableFailed,
			Error:     err.Error(),
			UpdatedAt: time.Now(),
		}
		_ = w.db.UpsertJob(ctx, failedJob)
		return err
	}

	if needsHITL, ok := result["needs_hitl"].(bool); ok && needsHITL {
		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   fmt.Sprintf("Vendor %s requires approval", job.VendorData.Name),
			Status:   "PENDING",
			SentAt:   time.Now(),
		}
		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			log.Printf("Failed to send Slack approval for %s: %v", job.JobID, err)
		} else {
			_ = w.db.UpsertHITLRequest(ctx, hitlReq)
		}
	}

	_ = w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:      "system",
		Action:     "PROCESSED",
		TargetType: "job",
		TargetID:   job.JobID,
		NewState:   "COMPLETED",
		Timestamp:  time.Now(),
	})

	return nil
}
