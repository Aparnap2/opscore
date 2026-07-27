package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

// slackWebhookHandler handles POST /slack/webhook.
func (s *ServerDeps) slackWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	body := string(bodyBytes)

	// Verify Slack signature in production.
	appEnv := os.Getenv("APP_ENV")
	slackSigningSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if appEnv == "production" && slackSigningSecret != "" {
		signature := r.Header.Get("X-Slack-Signature")
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")
		if signature == "" || timestamp == "" {
			writeError(w, http.StatusBadRequest, "Missing Slack headers")
			return
		}
		if !verifySlackSignature(slackSigningSecret, timestamp, body, signature) {
			writeError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
	}

	// Slack interactive components may send URL-encoded payloads with a "payload" field.
	// Since we consumed r.Body with io.ReadAll above, parse the raw body string.
	payloadBytes := bodyBytes
	payloadBody := body
	if strings.HasPrefix(body, "payload=") {
		values, err := url.ParseQuery(body)
		if err == nil {
			if pf := values.Get("payload"); pf != "" {
				payloadBytes = []byte(pf)
				payloadBody = pf
			}
		}
	}

	var payload SlackWebhookRequest
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		slog.Error("Failed to parse Slack payload", "err", err)
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// URL verification.
	if payload.Type == "url_verification" {
		writeJSON(w, http.StatusOK, SlackWebhookResponse{Challenge: payload.Challenge})
		return
	}

	// Event callback.
	if payload.Type == "event_callback" {
		event := payload.Event
		if event == nil {
			writeError(w, http.StatusBadRequest, "Missing event")
			return
		}
		eventType, _ := event["type"].(string)
		if eventType == "message" {
			slog.Info("Slack message event received")
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Interactive callback (block_actions).
	if payload.Type == "block_actions" {
		if s.slack == nil {
			slog.Error("Slack HITL provider not initialized, cannot handle callback")
			writeError(w, http.StatusInternalServerError, "Slack integration not configured")
			return
		}
		// Slack can send interactive payloads as direct JSON or a URL-encoded form field.
		// We already handled the form-encoded case above with payloadBytes/payloadBody,
		// so payloadBody is guaranteed to hold the JSON representation at this point.
		callback, err := s.slack.ParseSlackPayload(payloadBody)
		if err != nil {
			slog.Error("Failed to parse Slack callback", "err", err)
			writeError(w, http.StatusBadRequest, "Invalid callback payload")
			return
		}

		ctx := r.Context()

		// Determine action: approve:<jobID> or reject:<jobID>
		parts := strings.SplitN(callback.ActionID, ":", 2)
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "Invalid action ID")
			return
		}
		action := parts[0]
		jobID := parts[1]

		var newStatus domain.JobStatus
		var hitlStatus domain.HITLRequestStatus
		switch action {
		case "approve":
			newStatus = domain.JobStatusCompleted
			hitlStatus = domain.HITLStatusApproved
		case "reject":
			newStatus = domain.JobStatusFailed
			hitlStatus = domain.HITLStatusRejected
		default:
			writeError(w, http.StatusBadRequest, "Unknown action")
			return
		}

		// Update job status.
		job, err := s.db.GetJob(ctx, jobID, "default")
		if err != nil {
			slog.Error("Failed to get job", "jobID", jobID, "err", err)
			writeError(w, http.StatusNotFound, "Job not found")
			return
		}
		job.Status = newStatus
		job.UpdatedAt = time.Now()
		_ = s.db.UpsertJob(ctx, job)

		// Update HITL request.
		hitlReq, err := s.db.GetHITLRequest(ctx, "hitl-"+jobID, "default")
		if err == nil && hitlReq != nil {
			hitlReq.Status = hitlStatus
			t := time.Now()
			hitlReq.RespondedAt = &t
			_ = s.db.UpsertHITLRequest(ctx, hitlReq)
		}

		// Audit event.
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			Actor:      callback.UserID,
			Action:     string(hitlStatus),
			TargetType: "job",
			TargetID:   jobID,
			OldState:   string(domain.JobStatusAwaitingHITL),
			NewState:   string(newStatus),
			Timestamp:  time.Now(),
		})

		slog.Info("HITL decision", "job", jobID, "action", action, "user", callback.UserID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func verifySlackSignature(signingSecret, timestamp, body, signature string) bool {
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)
	h := hmac.New(sha256.New, []byte(signingSecret))
	h.Write([]byte(baseString))
	computedSig := "v0=" + hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(computedSig), []byte(signature))
}
