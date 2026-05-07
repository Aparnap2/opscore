// Package handlers provides Azure Functions HTTP, Queue, and Timer triggers
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/providers"
)

// Context keys for storing adapters
const (
	CtxKeyBlobAdapter     = "blobAdapter"
	CtxKeyQueueAdapter    = "queueAdapter"
	CtxKeyCosmosAdapter   = "cosmosAdapter"
	CtxKeyDocAgent        = "docAgent"
	CtxKeyVendorAgent     = "vendorAgent"
	CtxKeyComplianceAgent = "complianceAgent"
	CtxKeyHITLProvider    = "hitlProvider"
	CtxKeyConfig          = "config"
)

// Queue names
const (
	QueueDocument   = "document-queue"
	QueueVendor     = "vendor-queue"
	QueueCompliance = "compliance-queue"
)

// Container names
const (
	ContainerDocuments = "documents"
	ContainerVendors   = "vendors"
	ContainerCompliance = "compliance"
)

// Helper to extract context values
func getBlobAdapter(ctx context.Context) (providers.StorageProvider, error) {
	if adapter, ok := ctx.Value(CtxKeyBlobAdapter).(providers.StorageProvider); ok && adapter != nil {
		return adapter, nil
	}
	return nil, fmt.Errorf("blob adapter not initialized")
}

func getQueueAdapter(ctx context.Context) (providers.QueueProvider, error) {
	if adapter, ok := ctx.Value(CtxKeyQueueAdapter).(providers.QueueProvider); ok && adapter != nil {
		return adapter, nil
	}
	return nil, fmt.Errorf("queue adapter not initialized")
}

func getCosmosAdapter(ctx context.Context) (providers.DBProvider, error) {
	if adapter, ok := ctx.Value(CtxKeyCosmosAdapter).(providers.DBProvider); ok && adapter != nil {
		return adapter, nil
	}
	return nil, fmt.Errorf("cosmos adapter not initialized")
}

func getDocAgent(ctx context.Context) (*agents.DocumentAgent, error) {
	if agent, ok := ctx.Value(CtxKeyDocAgent).(*agents.DocumentAgent); ok && agent != nil {
		return agent, nil
	}
	return nil, fmt.Errorf("document agent not initialized")
}

func getVendorAgent(ctx context.Context) (*agents.VendorAgent, error) {
	if agent, ok := ctx.Value(CtxKeyVendorAgent).(*agents.VendorAgent); ok && agent != nil {
		return agent, nil
	}
	return nil, fmt.Errorf("vendor agent not initialized")
}

func getComplianceAgent(ctx context.Context) (*agents.ComplianceAgent, error) {
	if agent, ok := ctx.Value(CtxKeyComplianceAgent).(*agents.ComplianceAgent); ok && agent != nil {
		return agent, nil
	}
	return nil, fmt.Errorf("compliance agent not initialized")
}

func getHITLProvider(ctx context.Context) (providers.HITLProvider, error) {
	if provider, ok := ctx.Value(CtxKeyHITLProvider).(providers.HITLProvider); ok && provider != nil {
		return provider, nil
	}
	return nil, nil // Optional
}

// HTTP Response helpers
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		json.NewEncoder(w).Encode(body)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// Request/Response types
type UploadResponse struct {
	JobID      string `json:"job_id"`
	Status    string `json:"status"`
	BlobURL    string `json:"blob_url,omitempty"`
	Message    string `json:"message,omitempty"`
}

type JobStatusResponse struct {
	ID           string      `json:"id"`
	WorkflowType string      `json:"workflow_type"`
	Status       string      `json:"status"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Output       interface{} `json:"output,omitempty"`
	Error        string      `json:"error,omitempty"`
}

type JobSubmitRequest struct {
	TenantID string `json:"tenant_id"`
	Type     string `json:"type"` // "document", "vendor", "compliance"
}

type VendorSubmitRequest struct {
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	GSTNumber   string `json:"gst_number,omitempty"`
	PANNumber   string `json:"pan_number,omitempty"`
	IFSCCode    string `json:"ifsc_code,omitempty"`
	BankAccount string `json:"bank_account,omitempty"`
	Address     string `json:"address,omitempty"`
}

type SlackWebhookRequest struct {
	Type    string `json:"type"`
	Challenge string `json:"challenge,omitempty"`
	Event   map[string]any `json:"event,omitempty"`
}

type SlackWebhookResponse struct {
	Challenge string `json:"challenge,omitempty"`
}

// ErrorResponse represents an error in JSON format
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}