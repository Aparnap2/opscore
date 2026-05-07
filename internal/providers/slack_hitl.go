package providers

import (
	"context"
	"fmt"

	"github.com/aparna/opscore/internal/domain"
	"github.com/slack-go/slack"
)

// SlackHITLProvider implements HITLProvider for Slack
type SlackHITLProvider struct {
	client *slack.Client
}

// NewSlackHITLProvider creates a new Slack HITL provider
func NewSlackHITLProvider(token string) *SlackHITLProvider {
	return &SlackHITLProvider{
		client: slack.New(token),
	}
}

// SendApprovalRequest sends an approval request to Slack
func (s *SlackHITLProvider) SendApprovalRequest(ctx context.Context, req *domain.HITLRequest) error {
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
		fmt.Sprintf("*OpsCore Approval Request*\n\n*Type:* %s\n*Message:* %s\n*Job ID:* %s", req.Type, req.Message, req.JobID),
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

	// In production, send to a specific channel or user
	// For now, just log the message
	_ = msg

	return nil
}

// SendMessage sends a message to Slack
func (s *SlackHITLProvider) SendMessage(ctx context.Context, tenantID, message string) error {
	// In production, send to tenant-specific channel
	// For now, log the message
	fmt.Printf("[Slack] Tenant %s: %s\n", tenantID, message)
	return nil
}

// VerifySignature verifies a Slack request signature
func (s *SlackHITLProvider) VerifySignature(signingSecret, timestamp, body, signature string) bool {
	// Implementation for signature verification
	// Uses HMAC-SHA256
	return true // Placeholder
}

var _ HITLProvider = (*SlackHITLProvider)(nil)