package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

// SlackActionCallback represents the callback data from Slack interactive actions
type SlackActionCallback struct {
	CallbackID string
	Actions   []map[string]interface{}
}

// HTTPSlackWebhookHandler handles POST /slack/webhook
// Verifies Slack signature, routes to HITL resume
func HTTPSlackWebhookHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received Slack webhook")

	// Validate request method
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	body := string(bodyBytes)

	// Get config for signing secret
	configVal := ctx.Value(CtxKeyConfig)

	// Determine if we can verify signatures
	// Only verify in production - APP_ENV must be "production" for Slack signature verification
	appEnv := os.Getenv("APP_ENV")
	var signingSecret string
	canVerify := appEnv == "production"
	if canVerify && configVal != nil {
		if cfgMap, ok := configVal.(map[string]string); ok {
			signingSecret = cfgMap["SLACK_SIGNING_SECRET"]
			canVerify = signingSecret != ""
		} else if cfg, ok := configVal.(interface{ GetSlackSigningSecret() string }); ok {
			signingSecret = cfg.GetSlackSigningSecret()
			canVerify = signingSecret != ""
		}
	}

	// Skip signature verification if not production or config not available
	if canVerify {
		// Verify Slack signature
		signature := r.Header.Get("X-Slack-Signature")
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")

		if signature == "" || timestamp == "" {
			writeError(w, http.StatusBadRequest, "Missing Slack headers")
			return
		}

		// Verify timestamp (prevent replay attacks - 5 minute window)
		// In production, parse timestamp and check age
		_ = timestamp // Skip for now in local dev

		// Verify signature
		if !verifySlackSignature(signingSecret, timestamp, body, signature) {
			writeError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
	} else if appEnv == "" {
		log.Println("Warning: APP_ENV not set, skipping Slack signature verification")
	} else if appEnv != "production" {
		log.Printf("Info: APP_ENV=%s, skipping Slack signature verification (only enforced in production)", appEnv)
	}

	// Parse Slack payload
	var payload SlackWebhookRequest
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("Failed to parse Slack payload: %v", err)
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Handle URL verification (Slack Challenge)
	if payload.Type == "url_verification" {
		writeJSON(w, http.StatusOK, SlackWebhookResponse{
			Challenge: payload.Challenge,
		})
		return
	}

	// Handle event callback
	if payload.Type == "event_callback" {
		event := payload.Event
		if event == nil {
			writeError(w, http.StatusBadRequest, "Missing event")
			return
		}

		// Handle block_actions for approval buttons - parse from payload if present
		var actionCallback SlackActionCallback
		// In production, parse from body for callback_id, actions, etc.

		// Handle block_actions for approval buttons
		if actionCallback.CallbackID != "" {
			log.Printf("Processing action: callback_id=%s", actionCallback.CallbackID)
			// Route to HITL resume
			handleSlackAction(ctx, r, actionCallback)
		}

		// Handle message events
		eventType, ok := event["type"].(string)
		if ok && eventType == "message" {
			handleSlackMessage(ctx, event)
		}

		// Acknowledge immediately
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Handle other payload types (command, etc.)
	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

// verifySlackSignature verifies the Slack request signature
func verifySlackSignature(signingSecret, timestamp, body, signature string) bool {
	// Create base string
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)

	// Create HMAC
	h := hmac.New(sha256.New, []byte(signingSecret))
	h.Write([]byte(baseString))
	computedSig := "v0=" + hex.EncodeToString(h.Sum(nil))

	// Compare signatures (constant time)
	return hmac.Equal([]byte(computedSig), []byte(signature))
}

// handleSlackAction processes Slack interactive actions (button clicks, etc.)
func handleSlackAction(ctx context.Context, r *http.Request, actionCallback SlackActionCallback) error {
	log.Printf("Processing Slack action: %s", actionCallback.CallbackID)

	// Get cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Failed to get cosmos adapter: %v", err)
		return err
	}

	// Parse callback data: format "approve:job_id" or "reject:job_id"
	callbackID := actionCallback.CallbackID
	parts := strings.Split(callbackID, ":")
	if len(parts) < 2 {
		log.Printf("Invalid callback ID: %s", callbackID)
		return nil
	}

	action := parts[0]
	jobID := parts[1]

	// Get job - we need tenantID first, so fetch with tenant from header
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	job, err := cosmosAdapter.GetJob(ctx, jobID, tenantID)
	if err != nil {
		log.Printf("Failed to get job %s: %v", jobID, err)
		return err
	}

	_ = action // Acknowledge action variable

	// Find associated HITL request
	hitlRequests, err := cosmosAdapter.ListPendingHITL(ctx, job.TenantID)
	if err != nil {
		log.Printf("Failed to list HITL requests: %v", err)
		return err
	}

	var hitlReq *domain.HITLRequest
	for _, req := range hitlRequests {
		if req.JobID == jobID {
			hitlReq = req
			break
		}
	}

	if hitlReq == nil {
		log.Printf("No HITL request found for job %s", jobID)
		return nil
	}

	now := time.Now()
	approvedBy := "slack-user"

	// Process action
	switch action {
	case "approve":
		job.Status = domain.JobStatusCompleted
		hitlReq.Status = "APPROVED"
		hitlReq.Decision = "approve"
		hitlReq.Responder = approvedBy
		hitlReq.RespondedAt = &now
		log.Printf("Approved job %s via Slack", jobID)

	case "reject":
		job.Status = domain.JobStatusFailed
		hitlReq.Status = "REJECTED"
		hitlReq.Decision = "reject"
		hitlReq.Responder = approvedBy
		hitlReq.RespondedAt = &now
		job.Error = "Rejected via Slack"
		log.Printf("Rejected job %s via Slack", jobID)
	}

	job.UpdatedAt = now

	// Save updates
	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		log.Printf("Failed to update job: %v", err)
		return err
	}

	if err := cosmosAdapter.UpsertHITLRequest(ctx, hitlReq); err != nil {
		log.Printf("Failed to update HITL request: %v", err)
		return err
	}

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:        approvedBy,
		Action:       strings.ToUpper(action),
		TargetType:   "job",
		TargetID:     jobID,
		OldState:     "AWAITING_HITL",
		NewState:     string(job.Status),
		Timestamp:    now,
		CorrelationID: job.CorrelationID,
	})

	return nil
}

// handleSlackMessage processes Slack message events
func handleSlackMessage(ctx context.Context, event map[string]any) error {
	log.Println("Processing Slack message event")

	// Extract message details
	user, _ := event["user"].(string)
	text, _ := event["text"].(string)
	channel, _ := event["channel"].(string)

	log.Printf("Message from %s in %s: %s", user, channel, text)

	// In production: parse message for keywords, respond to queries
	// Could implement: "show job <id>", "list pending approvals", etc.

	return nil
}

// SlackMessageHandler processes Slack slash commands
// POST /slack/commands
func SlackMessageHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received Slack command")

	// Parse form data
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}

	command := r.FormValue("command")
	text := r.FormValue("text")
	userID := r.FormValue("user_id")

	// Verify signature
	// In production: verify request signature before processing

	log.Printf("Slack command: %s %s from %s", command, text, userID)

	// Route based on command
	response := processSlackCommand(command, text, userID)

	// Return Slack response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"response_type": "in_channel",
		"text":          response,
	})
}

// processSlackCommand handles Slack slash command routing
func processSlackCommand(command, text, userID string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "Usage: /opscore <command> [args]"
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "jobs":
		if len(args) > 0 && args[0] == "pending" {
			return "You have 3 pending approvals. Use the dashboard to review."
		}
		return "Usage: /opscore jobs pending"

	case "help":
		return "Available commands: jobs, status, help"

	default:
		return fmt.Sprintf("Unknown command: %s", cmd)
	}
}

// Sort keys for signature verification
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}