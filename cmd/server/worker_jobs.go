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
)

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

		// Persist HITL request to DB BEFORE sending Slack notification.
		// This ensures the request survives even when Slack is unconfigured.
		if err := w.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return fmt.Errorf("persist hitl request for document: %w", err)
		}
		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			slog.Error("Failed to send Slack approval, but HITL request is in DB", "jobID", job.JobID, "err", err)
		}
	}

	return nil
}

// processSignalJob handles a manufacturing signal job (PO/GRN/Invoice) through
// the transitional Signal Agent path. It reuses the document workflow's
// version-aware upsert and HITL routing.
func (w *Worker) processSignalJob(ctx context.Context, body string) error {
	var job agents.SignalJob
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return fmt.Errorf("unmarshal signal job: %w", err)
	}

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
		slog.Error("Failed to update signal job to PROCESSING", "jobID", job.JobID, "err", err)
	}

	result, err := w.signalAgent.ProcessSignal(ctx, &job, nil)
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

	// Converge the signal workflow onto first-class manufacturing tables
	// (Phase 2): persist the mapped entity + any exception cases via DBProvider.
	// The job record below remains the traceability summary (PHASE 7 retires the
	// extracted_data bridge as the primary store).
	//
	// The write is wrapped in a transaction with the job's tenant context so
	// that PostgreSQL Row-Level Security (RLS) allows the manufacturing upserts
	// (SET LOCAL app.tenant_id is only visible inside an explicit transaction).
	if err := w.db.WithTx(ctx, job.TenantID, func(txCtx context.Context) error {
		return agents.PersistSignalResult(txCtx, w.db, &job, result)
	}); err != nil {
		slog.Error("Failed to persist manufacturing entities for signal", "jobID", job.JobID, "err", err)
		_ = w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   job.TenantID,
			Actor:      "system",
			Action:     "MANUFACTURING_PERSIST_FAILED",
			TargetType: "job",
			TargetID:   job.JobID,
			NewState:   string(domain.JobStatusRetryableFailed),
			Error:      err.Error(),
			Timestamp:  time.Now(),
		})
		return err
	}

	// Persist the deterministic signal result on the job record (traceability
	// summary — retained through PHASE 7 which retires the extracted_data bridge).
	outJob := &domain.Job{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		Status:       domain.JobStatusCompleted,
		DocumentType: result.DocumentType,
		Confidence:   result.Confidence,
		Extracted:    result,
		UpdatedAt:    time.Now(),
	}
	if result.NeedsHITL {
		outJob.Status = domain.JobStatusAwaitingHITL
		outJob.HITLReason = result.HITLReason
	}
	if err := upsertWithVersion(outJob); err != nil {
		slog.Error("Failed to persist signal job result", "jobID", job.JobID, "err", err)
	}

	if result.NeedsHITL {
		reason := fmt.Sprintf("Signal %s requires approval", job.FileName)
		if result.HITLReason != "" {
			reason = fmt.Sprintf("%s | %s", reason, result.HITLReason)
		}
		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   reason,
			Status:   domain.HITLStatusPending,
			SentAt:   time.Now(),
		}
		// Persist HITL request to DB BEFORE sending Slack notification.
		if err := w.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return fmt.Errorf("persist hitl request for signal: %w", err)
		}
		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			slog.Error("Failed to send Slack approval, but HITL request is in DB", "jobID", job.JobID, "err", err)
		}
	}

	_ = w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:      "system",
		Action:     "PROCESSED",
		TargetType: "job",
		TargetID:   job.JobID,
		NewState:   string(outJob.Status),
		Timestamp:  time.Now(),
	})

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
		// Persist HITL request to DB BEFORE sending Slack notification.
		if err := w.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return fmt.Errorf("persist hitl request for vendor: %w", err)
		}
		if err := w.slack.SendApprovalRequest(ctx, hitlReq); err != nil {
			slog.Error("Failed to send Slack approval, but HITL request is in DB", "jobID", job.JobID, "err", err)
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
