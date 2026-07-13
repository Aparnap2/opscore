package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aparna/opscore/internal/adapters/postgres"
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

// processDocumentJob handles a document ingestion job.
func (w *Worker) processDocumentJob(ctx context.Context, body string) error {
	var job agents.DocumentJob
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return fmt.Errorf("unmarshal document job: %w", err)
	}

	// Update job to PROCESSING with optimistic locking
	upsertWithVersion := func(j *domain.Job) error {
		existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
		if getErr == nil {
			j.Version = existingJob.Version
		}
		if err := w.db.UpsertJob(ctx, j); err != nil {
			if errors.Is(err, postgres.ErrVersionConflict) {
				existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
				if getErr == nil {
					j.Version = existingJob.Version
				}
				return w.db.UpsertJob(ctx, j)
			}
			return err
		}
		return nil
	}

	dbJob := &domain.Job{
		ID:        job.JobID,
		TenantID:  job.TenantID,
		Status:    domain.JobStatusProcessing,
		UpdatedAt: time.Now(),
	}
	if err := upsertWithVersion(dbJob); err != nil {
		slog.Error("Failed to update job to PROCESSING", "jobID", job.JobID, "err", err)
	}

	// Process the document
	result, err := w.docAgent.ProcessDocument(ctx, &job)
	if err != nil {
		// Mark as retryable failed with version
		failedJob := &domain.Job{
			ID:        job.JobID,
			TenantID:  job.TenantID,
			Status:    domain.JobStatusRetryableFailed,
			Error:     err.Error(),
			UpdatedAt: time.Now(),
		}
		_ = upsertWithVersion(failedJob)
		_ = w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   job.TenantID,
			Actor:      "system",
			Action:     "PROCESSING_FAILED",
			TargetType: "job",
			TargetID:   job.JobID,
			OldState:   string(domain.JobStatusProcessing),
			NewState:   string(domain.JobStatusRetryableFailed),
			Error:      err.Error(),
			Timestamp:  time.Now(),
		})
		return err
	}

	// If HITL needed, send Slack approval (worker owns HITLRequest creation)
	if needsHITL, ok := result["needs_hitl"].(bool); ok && needsHITL {
		// Build detailed reason with confidence and validation error count
		reason := fmt.Sprintf("Document %s requires approval", job.FileName)
		if confidence, ok := result["confidence"].(float64); ok {
			reason = fmt.Sprintf("%s | confidence=%.2f", reason, confidence)
		}
		if validations, ok := result["validations"].(*domain.ValidationResult); ok && validations != nil {
			reason = fmt.Sprintf("%s | validation_errors=%d", reason, len(validations.Errors))
		}

		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   reason,
			Status:   domain.HITLStatusPending,
			SentAt:   time.Now(),
		}

		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			slog.Error("Failed to send Slack approval", "jobID", job.JobID, "err", err)
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

	// Helper for version-aware upsert
	upsertWithVersion := func(j *domain.Job) error {
		existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
		if getErr == nil {
			j.Version = existingJob.Version
		}
		if err := w.db.UpsertJob(ctx, j); err != nil {
			if errors.Is(err, postgres.ErrVersionConflict) {
				existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
				if getErr == nil {
					j.Version = existingJob.Version
				}
				return w.db.UpsertJob(ctx, j)
			}
			return err
		}
		return nil
	}

	dbJob := &domain.Job{
		ID:        job.JobID,
		TenantID:  job.TenantID,
		Status:    domain.JobStatusProcessing,
		UpdatedAt: time.Now(),
	}
	if err := upsertWithVersion(dbJob); err != nil {
		slog.Error("Failed to update job to PROCESSING", "jobID", job.JobID, "err", err)
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
		_ = upsertWithVersion(failedJob)
		_ = w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   job.TenantID,
			Actor:      "system",
			Action:     "PROCESSING_FAILED",
			TargetType: "job",
			TargetID:   job.JobID,
			OldState:   string(domain.JobStatusProcessing),
			NewState:   string(domain.JobStatusRetryableFailed),
			Error:      err.Error(),
			Timestamp:  time.Now(),
		})
		return err
	}

	if needsHITL, ok := result["needs_hitl"].(bool); ok && needsHITL {
		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   fmt.Sprintf("Vendor %s requires approval: risk_score=%v", job.VendorData.Name, result["risk_score"]),
			Status:   domain.HITLStatusPending,
			SentAt:   time.Now(),
		}
		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			slog.Error("Failed to send Slack approval", "jobID", job.JobID, "err", err)
		} else {
			hitlReq.SentAt = time.Now()
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
