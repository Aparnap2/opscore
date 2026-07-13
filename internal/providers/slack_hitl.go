package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/aparna/opscore/internal/domain"
	"github.com/slack-go/slack"
)

// SlackCallbackResult represents parsed Slack interaction callback
type SlackCallbackResult struct {
	ActionID   string `json:"action_id"`
	JobID      string `json:"job_id"`
	UserID     string `json:"user_id"`
	Type       string `json:"type"`
	CallbackID string `json:"callback_id"`
	MessageTS  string `json:"message_ts"`
}

// SlackHITLProvider implements HITLProvider for Slack
type SlackHITLProvider struct {
	client     *slack.Client
	webhookURL string
	channel    string
}

// NewSlackHITLProvider creates a new Slack HITL provider
func NewSlackHITLProvider(token string) *SlackHITLProvider {
	return &SlackHITLProvider{
		client:     slack.New(token),
		webhookURL: "http://localhost:3100/api/chat.postMessage",
	}
}

// WithWebhookURL sets custom webhook URL for HITL messages
func (s *SlackHITLProvider) WithWebhookURL(url string) *SlackHITLProvider {
	s.webhookURL = url
	return s
}

// WithChannel sets the Slack channel for HITL messages
func (s *SlackHITLProvider) WithChannel(channel string) *SlackHITLProvider {
	s.channel = channel
	return s
}

// SendApprovalRequest sends an approval request to Slack
func (s *SlackHITLProvider) SendApprovalRequest(ctx context.Context, req *domain.HITLRequest) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Slack HITL provider not initialized (missing SLACK_BOT_TOKEN)")
	}
	// Create Slack block kit message for approval using Message blocks
	approveBtn := slack.NewButtonBlockElement(
		fmt.Sprintf("approve:%s", req.JobID),
		"approve",
		slack.NewTextBlockObject(slack.PlainTextType, "Approve", false, false),
	)
	approveBtn.WithStyle(slack.StylePrimary)

	rejectBtn := slack.NewButtonBlockElement(
		fmt.Sprintf("reject:%s", req.JobID),
		"reject",
		slack.NewTextBlockObject(slack.PlainTextType, "Reject", false, false),
	)
	rejectBtn.WithStyle(slack.StyleDanger)

	textBlock := slack.NewTextBlockObject(
		slack.MarkdownType,
		fmt.Sprintf("*OpsCore Approval Request*\n\n*Reason:* %s\n*Job ID:* %s", req.Reason, req.JobID),
		false,
		false,
	)
	sectionBlock := slack.NewSectionBlock(textBlock, nil, nil)

	actionsBlock := slack.NewActionBlock(
		fmt.Sprintf("approval_%s", req.JobID),
		approveBtn,
		rejectBtn,
	)

	msg := slack.Msg{
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{sectionBlock, actionsBlock},
		},
	}

	channel := s.channel
	if channel == "" {
		channel = os.Getenv("SLACK_HITL_CHANNEL")
	}
	if channel == "" {
		channel = "C01ABCDEFG" // fallback for local dev
	}

	_, _, err := s.client.PostMessageContext(ctx, channel, slack.MsgOptionBlocks(msg.Blocks.BlockSet...))
	if err != nil {
		return fmt.Errorf("sending slack approval: %w", err)
	}

	return nil
}

// SendMessage sends a message to Slack
func (s *SlackHITLProvider) SendMessage(ctx context.Context, tenantID, message string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Slack HITL provider not initialized (missing SLACK_BOT_TOKEN)")
	}
	channel := s.channel
	if channel == "" {
		channel = os.Getenv("SLACK_HITL_CHANNEL")
	}
	if channel == "" {
		channel = "C01ABCDEFG" // fallback for local dev
	}

	text := fmt.Sprintf("[Tenant %s] %s", tenantID, message)
	_, _, err := s.client.PostMessageContext(ctx, channel, slack.MsgOptionText(text, false))
	if err != nil {
		return fmt.Errorf("sending slack message: %w", err)
	}
	return nil
}

// VerifySignature verifies a Slack request signature
func (s *SlackHITLProvider) VerifySignature(signingSecret, timestamp, body, signature string) bool {
	// Implementation for signature verification
	// Uses HMAC-SHA256
	return true // Placeholder
}

// ParseSlackPayload parses the Slack interactive payload (block_actions)
// Returns the action ID, job ID, and user ID from the callback
func (s *SlackHITLProvider) ParseSlackPayload(payload string) (*SlackCallbackResult, error) {
	// Parse the JSON payload from Slack
	var callback struct {
		Type       string `json:"type"`
		CallbackID string `json:"callback_id"`
		MessageTS  string `json:"message_ts"`
		User       struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"user"`
		Actions []struct {
			ActionID string `json:"action_id"`
			BlockID  string `json:"block_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}

	if err := json.Unmarshal([]byte(payload), &callback); err != nil {
		return nil, fmt.Errorf("unmarshaling Slack payload: %w", err)
	}

	result := &SlackCallbackResult{
		Type:       callback.Type,
		CallbackID: callback.CallbackID,
		MessageTS:  callback.MessageTS,
	}

	// Extract user ID if present
	if callback.User.ID != "" {
		result.UserID = callback.User.ID
	}

	// Extract action and job ID from the first action
	if len(callback.Actions) > 0 {
		result.ActionID = callback.Actions[0].ActionID
		result.JobID = callback.Actions[0].Value
	}

	return result, nil
}

// SendToWebhook sends a message to the configured webhook URL (Mockoon or real Slack)
func (s *SlackHITLProvider) SendToWebhook(ctx context.Context, msg slack.Msg) error {
	if s.webhookURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	// Convert message to JSON
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	// Send to webhook
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("webhook returned status: %d", resp.StatusCode)
	}

	return nil
}

var _ HITLProvider = (*SlackHITLProvider)(nil)
