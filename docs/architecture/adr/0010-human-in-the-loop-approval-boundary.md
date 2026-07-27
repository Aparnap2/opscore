# ADR-0010: Human-in-the-Loop Approval Boundary

## Status

**Proposed** — HITL is currently triggered for specific conditions (high risk score, low OCR confidence) but there is no formal boundary classification defining when HITL is mandatory, conditional, or bypassed.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore routes decisions to human operators through Slack Block Kit and the Admin API when automated processing cannot resolve an outcome with sufficient confidence. Currently, HITL triggers are defined ad-hoc in each agent:

- `vendor_agent.go`: HITL if `riskScore >= 60 || len(validationErrors) > 0`
- `document_agent.go`: HITL if OCR confidence < 0.7
- `compliance_agent.go`: HITL if LLM gap analysis confidence < 0.6

These thresholds exist as magic numbers scattered across agents. There is no:

1. **Formal classification** — what makes a decision mandatory vs. optional vs. bypassed for HITL.
2. **Consistent trigger matrix** — which conditions in which workflows require human review.
3. **State machine** for HITL lifecycle (PENDING → APPROVED/REJECTED/EXPIRED) with SLA expectations.
4. **Audit trail** for HITL decisions, including who approved/rejected, when, and why.
5. **Operator SLA** — how long operators have to respond before a decision auto-escalates or auto-expires.

### Current State

| Agent | Trigger Condition | Channel | SLA | Timeout Behavior |
|-------|-------------------|---------|-----|-----------------|
| VendorAgent | `riskScore >= 60 \|\| validationErrors > 0` | Slack Block Kit | None | No timeout — HITLRequest stays PENDING indefinitely |
| DocumentAgent | OCR confidence < 0.7 | Slack Block Kit | None | No timeout |
| ComplianceAgent | LLM gap confidence < 0.6 | Admin API only | None | No timeout |

### Requirements

1. Define a three-tier HITL classification: mandatory, conditional, bypassed.
2. Enforce classification in the agent layer, not in domain.
3. Provide a consistent HITL state machine with explicit transitions.
4. Operator SLA with configurable timeout and auto-escalation.
5. Audit every HITL decision with actor, action, reason, and timestamp.
6. Support Slack and Admin API channels, with the Admin API as the canonical source of truth.

## Decision

### Three-Tier HITL Classification

Every decision point in an agent workflow is classified into one of three tiers:

| Tier | Classification | Definition | Effect | Examples |
|------|---------------|------------|--------|----------|
| **Tier 1** | **Mandatory** | Decision cannot proceed without human review. No automated alternative is acceptable. | Workflow blocks until operator approves/rejects. | Fraud flag detected, conflicting verification results, high-risk vendor (score ≥ 80), document forgery suspicion |
| **Tier 2** | **Conditional** | Decision should be reviewed by a human, but has an automated fallback if SLA expires. | Workflow blocks for SLA duration, then proceeds with degraded action. | Low OCR confidence (0.5-0.7), first-time vendor, missing optional documents, borderline risk score (60-79) |
| **Tier 3** | **Bypassed** | Decision is fully automated. No human review needed. | Workflow continues without HITL. | Low risk score (< 60), all checks pass, high OCR confidence (> 0.7), routine compliance match |

### HITL Trigger Conditions Matrix

| Workflow / Condition | Tier | Rationale |
|---------------------|------|-----------|
| **Vendor Onboarding** | | |
| Risk score ≥ 80 | Mandatory | High-risk vendor requires human judgment to approve |
| Risk score 60-79 | Conditional | Borderline; auto-approve if no response within SLA (2h) |
| Risk score < 60, all IDs valid | Bypassed | Low risk, deterministic checks pass |
| Duplicate GST detected | Mandatory | Potential fraud — must be reviewed |
| Conflicting verification (PAN vs GST name mismatch) | Mandatory | Legal entity mismatch requires human verification |
| Missing mandatory documents | Conditional | Auto-reject if SLA expires |
| First-time vendor from new state/region | Conditional | Review on first occurrence; auto-approve subsequent vendors from same region |
| **Document Ingestion** | | |
| OCR confidence < 0.5 | Mandatory | Text likely unrecoverable; need human transcription or document resubmission |
| OCR confidence 0.5-0.7 | Conditional | LLM-assisted extraction as fallback; HITL if LLM also low confidence |
| OCR confidence > 0.7 | Bypassed | Reliable extraction |
| Document type not classified | Conditional | Operator assigns type; auto-assign "unknown" if SLA expires |
| **Compliance Analysis** | | |
| LLM gap analysis confidence < 0.5 | Mandatory | Compliance gap is uncertain; legal risk requires human review |
| LLM gap analysis confidence 0.5-0.7 | Conditional | Automated gap report with confidence warning; operator can override |
| LLM gap analysis confidence > 0.7 | Bypassed | High-confidence automated gap report |
| Circular applies to > 50% of vendors | Conditional | Bulk impact notification; operator reviews before mass compliance update |
| **Trust Battery** | | |
| Fraud flag raised | Mandatory | BLOCKED tier transition requires human confirmation |
| Multiple disputes filed (≥ 3 in 30 days) | Conditional | Operator reviews dispute pattern; auto-escalate to STRATEGIC → PROBATION if SLA expires |

### HITL State Machine

```
                    ┌─────────────────────────────────────────┐
                    │            PENDING                       │
                    │  (created by agent, notified to Slack)   │
                    └──────────┬──────────────────────────────┘
                               │
              ┌────────────────┼────────────────────┐
              │                │                     │
              ▼                ▼                     ▼
     ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐
     │  APPROVED    │  │  REJECTED    │  │   EXPIRED        │
     │  (operator)  │  │  (operator)  │  │  (SLA timeout)   │
     └──────────────┘  └──────────────┘  └──────────────────┘
                                               │
                                    ┌──────────┴──────────┐
                                    │                     │
                                    ▼                     ▼
                            ┌──────────────┐    ┌──────────────────┐
                            │ AUTO-APPROVED│    │ AUTO-REJECTED    │
                            │ (conditional)│    │ (mandatory)      │
                            └──────────────┘    └──────────────────┘
```

State transitions:
- **PENDING**: HITL request created, Slack notification sent. Timer starts.
- **APPROVED**: Operator approves via Slack button or Admin API. Workflow continues with approved outcome.
- **REJECTED**: Operator rejects via Slack button or Admin API. Workflow terminates or routes to escalation.
- **EXPIRED**: SLA timeout reached without operator response.
  - Conditional tier → **AUTO-APPROVED**: Workflow proceeds with automated fallback decision.
  - Mandatory tier → **AUTO-REJECTED**: Workflow routes to DLQ for offline operator recovery.

### HITL Data Model

```go
// internal/domain/hitl.go
type HITLTier int

const (
    HITLTierMandatory  HITLTier = 1
    HITLTierConditional HITLTier = 2
    HITLTierBypassed   HITLTier = 3
)

type HITLStatus string

const (
    HITLStatusPending      HITLStatus = "PENDING"
    HITLStatusApproved     HITLStatus = "APPROVED"
    HITLStatusRejected     HITLStatus = "REJECTED"
    HITLStatusExpired      HITLStatus = "EXPIRED"
    HITLStatusAutoApproved HITLStatus = "AUTO_APPROVED"
    HITLStatusAutoRejected HITLStatus = "AUTO_REJECTED"
)

type HITLRequest struct {
    ID             string                 `json:"id"`
    JobID          string                 `json:"job_id"`
    TenantID       string                 `json:"tenant_id"`
    Workflow       string                 `json:"workflow"`
    Tier           HITLTier              `json:"tier"`
    Status         HITLStatus            `json:"status"`
    Context        map[string]any        `json:"context"`        // Workflow-specific context shown to operator
    Decision       string                `json:"decision,omitempty"`
    DecisionReason string                `json:"decision_reason,omitempty"`
    DecidedBy      string                `json:"decided_by,omitempty"`
    DecidedAt      *time.Time            `json:"decided_at,omitempty"`
    SLAExpiresAt   time.Time             `json:"sla_expires_at"`
    CreatedAt      time.Time             `json:"created_at"`
    UpdatedAt      time.Time             `json:"updated_at"`
}
```

### Enforcement in Agent Layer

The HITL classification is enforced in the agent layer, not in domain, because:
1. Domain code must be pure Go with zero I/O. HITL involves sending Slack messages, writing to the DB, and managing timeouts.
2. The classification logic uses domain types (risk score, OCR confidence) but the HITL dispatch is an infrastructure concern.
3. Different tenants may configure different HITL tiers for the same condition (pro tenants auto-approve conditional; business tenants require human for everything).

Agent pattern for HITL classification:

```go
// internal/agents/vendor_agent.go
func (a *VendorAgent) determineHITLTier(result *domain.RiskResult) domain.HITLTier {
    switch {
    case result.RiskScore >= 80 || result.DuplicateGST:
        return domain.HITLTierMandatory
    case result.RiskScore >= 60 || result.FirstVendorInRegion:
        return domain.HITLTierConditional
    default:
        return domain.HITLTierBypassed
    }
}

func (a *VendorAgent) ProcessVendor(ctx context.Context, job *domain.Job) error {
    result := a.scorer.ComputeRisk(job.Input)
    tier := a.determineHITLTier(result)

    if tier == domain.HITLTierBypassed {
        return a.completeVendor(ctx, job, result)
    }

    // Create HITL request and notify operator
    hitl := a.createHITLRequest(ctx, job, tier, result)
    if err := a.dispatchHITL(ctx, hitl); err != nil {
        return fmt.Errorf("dispatch hitl: %w", err)
    }

    if tier == domain.HITLTierMandatory {
        // Block until operator responds or SLA expires
        return a.waitForHITLDecision(ctx, hitl)
    }

    // Conditional: start SLA timer, proceed with fallback on timeout
    return a.startConditionalTimer(ctx, hitl, job)
}
```

### Operator SLA Configuration

SLA durations are defined per tier and can be overridden per tenant:

```go
// internal/domain/hitl_config.go
type HITLConfig struct {
    MandatorySLA     time.Duration // default: 4 hours
    ConditionalSLA   time.Duration // default: 2 hours
    ConditionalAction string       // "auto_approve" or "auto_reject"
    EscalationTarget string       // email or Slack channel for missed SLA
}
```

The SLA timer starts when the HITL request is created and the Slack notification is sent. On expiry:
- **Mandatory**: HITLStatus → AUTO_REJECTED. Job → DEAD_LETTERED. Escalation notification sent to `EscalationTarget`.
- **Conditional**: HITLStatus → AUTO_APPROVED or AUTO_REJECTED per `ConditionalAction`. Workflow proceeds.

### HITL Channels

| Channel | Use Case | Implementation |
|---------|----------|---------------|
| **Slack Block Kit** | Instant operator actions | `providers.SlackHITL` sends message with Approve/Reject buttons. `POST /slack/webhook` receives callback. |
| **Admin API** | Programmatic / non-Slack environments | `GET /admin/review-queue` lists pending. `POST /admin/review-queue` approves/rejects. |
| **Streamlit Admin UI** | Dashboard review | `ops-ui/app.py` queries Admin API and displays pending requests with approve/reject buttons. |

The Admin API is the canonical source of truth. Slack is a notification channel that delegates to the Admin API for final state change.

### Audit Trail

Every HITL decision is recorded in the `audit_events` table:

```json
{
    "actor": "slack:U12345",
    "action": "HITL_APPROVED",
    "target_type": "hitl_request",
    "target_id": "hitl-vendor-abc123",
    "changes": {
        "status": "APPROVED",
        "tier": "MANDATORY",
        "decision_reason": "Vendor verified via phone call"
    },
    "timestamp": "2026-07-13T10:30:00Z"
}
```

## Alternatives Considered

### 1. HITL in Domain Layer

Rejected because:
- Domain code must be pure Go with zero I/O. HITL dispatch (Slack, DB writes, timers) violates this.
- HITL is an infrastructure concern — different deployments may use different notification channels.
- The classification logic is simple enough to be a pure function in the domain, but the dispatch must be in the agent.

### 2. Single Tier (All HITL Is Mandatory)

Rejected because:
- Slows down low-risk workflows. A 2-hour wait for every minor decision is unacceptable.
- Overwhelms operators with notifications. If every document ingestion requires human review, the review queue grows unbounded.
- Defeats the purpose of automated scoring.

### 3. Email-Only HITL

Rejected because:
- Email round-trip is unpredictable. Operators may not check email for hours.
- No interactive buttons without complex email parsing.
- Slack provides real-time notifications and instant action.

### 4. No SLA / Indefinite Pending

Rejected because:
- A stale HITLRequest blocks the workflow indefinitely. Operators may not notice the notification.
- The review queue grows with abandoned requests. No cleanup mechanism.
- Vendors wait indefinitely for approval — poor UX.

## Consequences

### Benefits

1. **Clear operator expectations.** Operators know which decisions require human review (mandatory) and which have automated fallbacks (conditional). No ambiguity about urgency.

2. **Predictable workflow timing.** Conditional tier has an SLA-bound timeout. Workflows are never blocked indefinitely. If the operator does not respond, the system makes the best automated decision.

3. **Auditable decision trail.** Every HITL decision — who approved, when, why — is recorded in the audit trail. This satisfies compliance requirements for regulated workflows.

4. **Consistent trigger matrix across agents.** All HITL triggers are defined in one place with the same three-tier classification. No scattered magic numbers.

5. **Tenant-configurable SLA.** Pro tenants can set shorter SLA windows or override conditional tiers to mandatory. Starter tenants have longer default SLAs.

6. **Graceful escalation.** When mandatory HITL SLA expires, the job routes to DLQ with an escalation notification. Operators can review offline and manually replay the job.

### Trade-offs / Risks

1. **Operator SLA depends on human availability.** If no operator is online, even conditional HITL requests may time out before review. Mitigation: Slack notifications are push-based; operators on mobile receive them. The escalation target provides a secondary notification channel.

2. **Auto-approval of conditional tier may be wrong.** If the automated fallback decision is incorrect (e.g., auto-approves a borderline vendor), the system accepts a risk. Mitigation: conditional tier is for low-risk ambiguity. Auto-approval is the best automated guess. The audit trail records that it was auto-approved, not operator-approved.

3. **Slack as primary channel creates dependency.** If Slack is unavailable, HITL notifications are not delivered. Mitigation: the Admin API and Streamlit UI provide alternative channels. The HITL request is always persisted in the DB regardless of Slack delivery success.

4. **SLA timer accuracy.** The SLA timer starts when the HITL request is created. If the Slack notification is delayed, the effective SLA is shorter. Mitigation: the SLA timer starts after the notification is sent (post-dispatch), not at creation time.

5. **Operator context switching.** An operator reviewing a HITL request needs sufficient context (risk score, validation errors, document preview) to make an informed decision. If the context is insufficient, the operator may reject a legitimate vendor. Mitigation: the `Context` field in `HITLRequest` is populated with all relevant workflow data. The Slack Block Kit message includes a summary. The Admin API returns full context.

## Related ADRs

- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow — establishes the Slack-based HITL pattern that this ADR formalizes with three-tier classification.
- **ADR-0009**: Bounded Agent Loops and Fallback Policy — defines the fallback chain escalation that routes to HITL when automated retries are exhausted.
- **ADR-0020**: Review Queue and Operator UX Boundary — defines the Admin API and Streamlit UI that provide the non-Slack HITL interface.
