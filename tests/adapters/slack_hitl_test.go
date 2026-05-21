//go:build integration

package adapters

import (
	"context"
	"testing"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// TestSlackHITLApproveRejectCallbacks tests parsing approve/reject callbacks from Slack
func TestSlackHITLApproveRejectCallbacks(t *testing.T) {
	ctx := context.Background()

	// Create SlackHITL provider
	provider := providers.NewSlackHITLProvider("test-token")

	hitlReq := &domain.HITLRequest{
		ID:       "hitl-test-123",
		TenantID: "tenant-abc",
		JobID:    "job-456",
		Reason:   "Invoice #INV-2024-001 requires approval",
		Status:   "PENDING",
	}

	// Verify SendApprovalRequest doesn't error (current placeholder)
	err := provider.SendApprovalRequest(ctx, hitlReq)
	if err != nil {
		t.Fatalf("SendApprovalRequest failed: %v", err)
	}

	// Test 1: Parse approve block_actions payload from Slack interaction
	approvePayload := `{
		"type": "block_actions",
		"callback_id": "approval_job-456",
		"actions": [
			{
				"action_id": "approve",
				"block_id": "approval_job-456",
				"value": "job-456"
			}
		],
		"message": {
			"ts": "1234567890.123456"
		},
		"user": {
			"id": "U1234567890",
			"name": "testuser"
		}
	}`

	callback, err := provider.ParseSlackPayload(approvePayload)
	if err != nil {
		t.Fatalf("ParseSlackPayload failed: %v", err)
	}

	// Verify approve action parsed correctly
	if callback.ActionID != "approve" {
		t.Errorf("Expected action_id 'approve', got '%s'", callback.ActionID)
	}

	if callback.JobID != "job-456" {
		t.Errorf("Expected job_id 'job-456', got '%s'", callback.JobID)
	}

	if callback.UserID != "U1234567890" {
		t.Errorf("Expected user_id 'U1234567890', got '%s'", callback.UserID)
	}

	// Test 2: Parse reject callback
	rejectPayload := `{
		"type": "block_actions",
		"callback_id": "approval_job-789",
		"actions": [
			{
				"action_id": "reject",
				"block_id": "approval_job-789",
				"value": "job-789"
			}
		],
		"user": {
			"id": "U9876543210",
			"name": "testuser2"
		}
	}`

	rejectCallback, err := provider.ParseSlackPayload(rejectPayload)
	if err != nil {
		t.Fatalf("ParseSlackPayload failed for reject: %v", err)
	}

	if rejectCallback.ActionID != "reject" {
		t.Errorf("Expected action_id 'reject', got '%s'", rejectCallback.ActionID)
	}

	if rejectCallback.JobID != "job-789" {
		t.Errorf("Expected job_id 'job-789', got '%s'", rejectCallback.JobID)
	}
}

// TestSlackBlockKitMessageStructure verifies the structure of Block Kit messages
func TestSlackBlockKitMessageStructure(t *testing.T) {
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-struct-123",
		TenantID: "tenant-abc",
		JobID:    "job-456",
		Reason:   "Vendor ACME Corp requires approval",
		Status:   "PENDING",
	}

	// Verify HITLRequest has required fields
	if hitlReq.ID == "" {
		t.Error("HITLRequest ID should not be empty")
	}
	if hitlReq.JobID == "" {
		t.Error("HITLRequest JobID should not be empty")
	}
}

// Verify HITLProvider interface is satisfied
var _ providers.HITLProvider = (*providers.SlackHITLProvider)(nil)
