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

// QueueDocumentHandler handles Azure Queue trigger for document-queue
// Pulls job from queue, runs DocumentAgent, updates Cosmos
func QueueDocumentHandler(ctx context.Context, msg json.RawMessage) error {
	log.Println("Processing document queue message")

	// Parse message
	var docJob agents.DocumentJob
	if err := json.Unmarshal(msg, &docJob); err != nil {
		log.Printf("Failed to parse document job: %v", err)
		return fmt.Errorf("invalid message format: %w", err)
	}

	log.Printf("Document job: tenant=%s, job=%s, file=%s", docJob.TenantID, docJob.JobID, docJob.FileName)

	// Get adapters
	docAgent, err := getDocAgent(ctx)
	if err != nil {
		log.Printf("Document agent not available: %v", err)
		return err
	}

	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Cosmos adapter not available: %v", err)
		return err
	}

	// Update job status to running
	job, err := cosmosAdapter.GetJob(ctx, docJob.JobID, docJob.TenantID)
	if err != nil {
		log.Printf("Failed to get job: %v", err)
		return fmt.Errorf("job not found: %w", err)
	}

	job.Status = domain.JobStatusProcessing
	job.UpdatedAt = time.Now()
	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		log.Printf("Failed to update job status: %v", err)
		return err
	}

	// Process document
	result, err := docAgent.ProcessDocument(ctx, &docJob)
	if err != nil {
		log.Printf("Document processing failed: %v", err)

		// Update job as failed
		job.Status = domain.JobStatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		cosmosAdapter.UpsertJob(ctx, job)

		// Log audit event
		cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
			Actor:        "document_agent",
			Action:       "PROCESS_FAILED",
			TargetType:   "job",
			TargetID:     docJob.JobID,
			OldState:     "RUNNING",
			NewState:     "FAILED",
			Timestamp:    time.Now(),
			CorrelationID: job.CorrelationID,
		})

		// Return error to trigger retry (up to Azure Functions retry policy)
		return fmt.Errorf("document processing failed: %w", err)
	}

	// Log audit event for successful processing
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:        "document_agent",
		Action:       "PROCESS_COMPLETED",
		TargetType:   "job",
		TargetID:     docJob.JobID,
		NewState:     string(job.Status),
		Timestamp:    time.Now(),
		CorrelationID: job.CorrelationID,
	})

	log.Printf("Document job completed: job=%s, result=%v", docJob.JobID, result)
	return nil
}

// QueueDocumentHandlerLegacy provides a handler function compatible with Azure Functions SDK
func QueueDocumentHandlerLegacy(ctx context.Context, queueMessage []byte) error {
	return QueueDocumentHandler(ctx, queueMessage)
}

// Retry configuration (in host.json):
// {
//   "queues": {
//     "maxDequeueCount": 3,
//     "visibilityTimeout": "00:00:30"
//   }
// }