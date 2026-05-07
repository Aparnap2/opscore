package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
)

// QueueComplianceHandler handles Azure Queue trigger for compliance-queue
// Runs ComplianceAgent for compliance processing
func QueueComplianceHandler(ctx context.Context, msg json.RawMessage) error {
	log.Println("Processing compliance queue message")

	// Parse message
	var complianceJob agents.ComplianceJob
	if err := json.Unmarshal(msg, &complianceJob); err != nil {
		log.Printf("Failed to parse compliance job: %v", err)
		return fmt.Errorf("invalid message format: %w", err)
	}

	log.Printf("Compliance job: tenant=%s, job=%s, type=%s", complianceJob.TenantID, complianceJob.JobID, complianceJob.JobType)

	// Get adapters
	complianceAgent, err := getComplianceAgent(ctx)
	if err != nil {
		log.Printf("Compliance agent not available: %v", err)
		return err
	}

	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Cosmos adapter not available: %v", err)
		return err
	}

	// Update job status
	job, err := cosmosAdapter.GetJob(ctx, complianceJob.JobID)
	if err != nil {
		// Create new job
		job = &domain.Job{
			ID:             complianceJob.JobID,
			TenantID:       complianceJob.TenantID,
			WorkflowType:   domain.WorkflowCompliance,
			Status:         domain.JobStatusRunning,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			CorrelationID:  complianceJob.CorrelationID,
		}
	}

	job.Status = domain.JobStatusRunning
	job.UpdatedAt = time.Now()
	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		log.Printf("Failed to update job: %v", err)
		return err
	}

	// Process compliance
	result, err := complianceAgent.ProcessCompliance(ctx, &complianceJob)
	if err != nil {
		log.Printf("Compliance processing failed: %v", err)

		job.Status = domain.JobStatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		cosmosAdapter.UpsertJob(ctx, job)

		// Log audit event
		cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
			Actor:        "compliance_agent",
			Action:       "PROCESS_FAILED",
			TargetType:   "job",
			TargetID:     complianceJob.JobID,
			OldState:     "RUNNING",
			NewState:     "FAILED",
			Timestamp:    time.Now(),
			CorrelationID: job.CorrelationID,
		})

		return fmt.Errorf("compliance processing failed: %w", err)
	}

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:        "compliance_agent",
		Action:       "PROCESS_COMPLETED",
		TargetType:   "job",
		TargetID:     complianceJob.JobID,
		NewState:     string(job.Status),
		Timestamp:    time.Now(),
		CorrelationID: job.CorrelationID,
	})

	log.Printf("Compliance job completed: job=%s, result=%v", complianceJob.JobID, result)
	return nil
}

// QueueComplianceHandlerLegacy provides a handler function compatible with Azure Functions SDK
func QueueComplianceHandlerLegacy(ctx context.Context, queueMessage []byte) error {
	return QueueComplianceHandler(ctx, queueMessage)
}