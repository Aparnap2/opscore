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

// QueueVendorHandler handles Azure Queue trigger for vendor-queue
// Runs VendorAgent to process vendor onboarding
func QueueVendorHandler(ctx context.Context, msg json.RawMessage) error {
	log.Println("Processing vendor queue message")

	// Parse message
	var vendorJob agents.VendorJob
	if err := json.Unmarshal(msg, &vendorJob); err != nil {
		log.Printf("Failed to parse vendor job: %v", err)
		return fmt.Errorf("invalid message format: %w", err)
	}

	log.Printf("Vendor job: tenant=%s, job=%s, vendor=%s", vendorJob.TenantID, vendorJob.JobID, vendorJob.VendorData.Name)

	// Get adapters
	vendorAgent, err := getVendorAgent(ctx)
	if err != nil {
		log.Printf("Vendor agent not available: %v", err)
		return err
	}

	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Cosmos adapter not available: %v", err)
		return err
	}

	// Get or create job
	job, err := cosmosAdapter.GetJob(ctx, vendorJob.JobID, vendorJob.TenantID)
	if err != nil {
		// Create new job if not exists
		job = &domain.Job{
			ID:           vendorJob.JobID,
			TenantID:     vendorJob.TenantID,
			WorkflowType: domain.WorkflowVendorOnboarding,
			Status:       domain.JobStatusProcessing,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
	}

	// Update job status
	job.Status = domain.JobStatusProcessing
	job.UpdatedAt = time.Now()
	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		log.Printf("Failed to update job: %v", err)
		return err
	}

	// Process vendor
	result, err := vendorAgent.ProcessVendor(ctx, &vendorJob)
	if err != nil {
		log.Printf("Vendor processing failed: %v", err)

		// Update job as failed
		job.Status = domain.JobStatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		cosmosAdapter.UpsertJob(ctx, job)

		// Log audit event
		cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
			Actor:        "vendor_agent",
			Action:       "PROCESS_FAILED",
			TargetType:   "job",
			TargetID:     vendorJob.JobID,
			OldState:     "RUNNING",
			NewState:     "FAILED",
			Timestamp:    time.Now(),
			CorrelationID: job.CorrelationID,
		})

		return fmt.Errorf("vendor processing failed: %w", err)
	}

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:        "vendor_agent",
		Action:       "PROCESS_COMPLETED",
		TargetType:   "job",
		TargetID:     vendorJob.JobID,
		NewState:     string(job.Status),
		Timestamp:    time.Now(),
		CorrelationID: job.CorrelationID,
	})

	log.Printf("Vendor job completed: job=%s, result=%v", vendorJob.JobID, result)
	return nil
}

// QueueVendorHandlerLegacy provides a handler function compatible with Azure Functions SDK
func QueueVendorHandlerLegacy(ctx context.Context, queueMessage []byte) error {
	return QueueVendorHandler(ctx, queueMessage)
}