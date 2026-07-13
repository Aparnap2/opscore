# ADR-0017: Billing Readiness and Plan Limits

## Status

**Proposed** — plan limits are defined in `domain/usage.go` but there is no billing integration or enforcement middleware. Usage metering exists as domain types but does not emit billing events.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore has three pricing plans (starter, pro, business) with distinct usage limits. The limits are defined as domain constants in `internal/domain/usage.go`:

```go
type Plan string

const (
    PlanStarter  Plan = "starter"
    PlanPro      Plan = "pro"
    PlanBusiness Plan = "business"
)

type PlanLimits struct {
    MaxVendors       int
    MaxDocuments     int
    MaxUsers         int
    MaxAPICalls      int
    MaxStorageGB     int
    MaxLLMCalls      int
    MaxComplianceFeeds int
    MaxMonthlySpendINR float64
}

var PlanLimitsMap = map[Plan]PlanLimits{
    PlanStarter:  {MaxVendors: 50, MaxDocuments: 200, MaxUsers: 3, MaxAPICalls: 1000, MaxStorageGB: 1, MaxLLMCalls: 100, MaxComplianceFeeds: 1, MaxMonthlySpendINR: 0},
    PlanPro:      {MaxVendors: 500, MaxDocuments: 2000, MaxUsers: 10, MaxAPICalls: 10000, MaxStorageGB: 10, MaxLLMCalls: 1000, MaxComplianceFeeds: 5, MaxMonthlySpendINR: 0},
    PlanBusiness: {MaxVendors: 5000, MaxDocuments: 20000, MaxUsers: 50, MaxAPICalls: 100000, MaxStorageGB: 100, MaxLLMCalls: 10000, MaxComplianceFeeds: 20, MaxMonthlySpendINR: 0},
}
```

Currently:
1. **No enforcement** — plans are stored but limits are not checked at any enforcement point.
2. **No billing integration** — there is no integration with Stripe or Razorpay for payment processing.
3. **No usage metering** — usage is not tracked per-tenant in a way that can be billed.
4. **No grace period** — when a tenant exceeds their plan limit, there is no defined behavior (warn, block, or upgrade).

### Requirements

1. Plan limits must be enforced at the application layer (API middleware and agent calls).
2. Usage must be metered and tracked per-tenant, per-billing-cycle.
3. Grace period for exceeded limits: warn first, then block.
4. Billing integration (Stripe/Razorpay) must be deferred but the usage metering system must emit billing events compatible with both.
5. Plan limits are stored as domain constants for fast enforcement — no DB lookup on every request.
6. Monthly rollover of usage counters.

## Decision

### Primary Decision: Application-Layer Enforcement with Billing Event Emission

Plan limits are enforced in the API handler layer and agent layer, not in the domain. Usage is metered and stored in PostgreSQL. Billing events are emitted to a `billing_events` table for future integration with Stripe or Razorpay.

### Enforcement Points

Enforcement happens at two layers:

**Layer 1: API Middleware (rate-limit style)**
- Before accepting a request, check if the tenant's current usage exceeds plan limits.
- Applies to: vendor creation, document upload, API calls, compliance feed subscriptions.
- Returns `429 Resource Exceeded` with a clear error message and link to upgrade.

```go
// internal/adapters/http/middleware.go
func PlanLimitMiddleware(um *UsageMeter) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            tenant := tenant.FromContext(r.Context())
            if tenant.Plan == "" {
                tenant.Plan = "starter" // default plan
            }
            resource := resourceFromPath(r.URL.Path)
            allowed, err := um.CheckLimit(r.Context(), tenant.ID, tenant.Plan, resource)
            if err != nil {
                http.Error(w, `{"error":"usage check failed"}`, http.StatusInternalServerError)
                return
            }
            if !allowed {
                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusTooManyRequests)
                json.NewEncoder(w).Encode(map[string]any{
                    "error": "plan limit exceeded",
                    "plan":  tenant.Plan,
                    "upgrade_url": "/admin/billing",
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

**Layer 2: Agent Layer (guard for async operations)**
- Before processing a job (vendor onboarding, document ingestion), check current usage.
- Applies to: LLM calls, document storage, compliance analysis jobs.
- If limit exceeded, the job is marked as blocked with a clear reason.

### Usage Metering

Usage is tracked in a `usage_metering` table:

```sql
CREATE TABLE usage_metering (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    resource_type TEXT NOT NULL,  -- 'vendor', 'document', 'api_call', 'llm_call', 'storage_gb'
    usage_count BIGINT NOT NULL DEFAULT 0,
    billing_cycle_start DATE NOT NULL,
    billing_cycle_end DATE NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, resource_type, billing_cycle_start)
);
```

The `UsageMeter` domain type tracks and enforces limits:

```go
// internal/domain/usage.go
type UsageMeter struct {
    store UsageStore // interface for DB reads/writes
}

func (um *UsageMeter) CheckLimit(ctx context.Context, tenantID, plan, resource string) (bool, error) {
    limits := PlanLimitsMap[Plan(plan)]
    current, err := um.store.GetCurrentUsage(ctx, tenantID, resource)
    if err != nil {
        return false, err
    }
    maxLimit := limits.ForResource(resource)
    return current < maxLimit, nil
}

func (um *UsageMeter) Increment(ctx context.Context, tenantID, resource string) error {
    // Increment usage counter for current billing cycle
    // Emit billing event if approaching limit (80%, 100%)
    current, err := um.store.IncrementUsage(ctx, tenantID, resource)
    if err != nil {
        return err
    }
    limits := PlanLimitsMap[Plan(um.store.GetTenantPlan(ctx, tenantID))]
    threshold := limits.ForResource(resource)
    // Warn at 80%
    if current >= int(float64(threshold)*0.8) {
        um.emitWarningEvent(ctx, tenantID, resource, current, threshold)
    }
    // Record billing event
    um.emitUsageEvent(ctx, tenantID, resource, current)
    return nil
}
```

### Grace Period and Enforcement Flow

```
Usage approaches limit (80%) → Warning event emitted
     ↓
Usage reaches limit (100%) → Warning + block on new operations
     ↓
Grace period (7 days for starter, 3 days for pro) → Reads still work, writes blocked
     ↓
Grace period expires → All operations blocked until plan upgrade or cycle reset
     ↓
New billing cycle → Usage counters reset, operations resume
```

### Monthly Rollover

Usage counters are scoped to a billing cycle (`billing_cycle_start` to `billing_cycle_end`). A scheduled job runs at the start of each month:

```go
// internal/agents/billing_agent.go
func (ba *BillingAgent) RolloverUsage(ctx context.Context) error {
    // Archive current cycle's usage
    // Delete or archive usage_metering rows where billing_cycle_end < NOW()
    // Reset counters by inserting new rows for the new cycle
    // Emit billing events for the closed cycle
    // Send monthly usage summary to tenant contacts
}
```

### Billing Event Emission Pattern

Usage events are emitted to a `billing_events` table for future billing integration:

```go
type BillingEvent struct {
    ID            string    `json:"id"`
    TenantID      string    `json:"tenant_id"`
    EventType     string    `json:"event_type"`   // "usage_warning", "usage_blocked", "usage_reset", "plan_upgrade", "plan_downgrade"
    Resource      string    `json:"resource"`      // "vendor", "document", "llm_call"
    UsageCount    int64     `json:"usage_count"`
    Limit         int64     `json:"limit"`
    Timestamp     time.Time `json:"timestamp"`
    Metadata      map[string]any `json:"metadata,omitempty"`
}
```

When billing integration is implemented (Stripe or Razorpay), a worker reads from `billing_events` and:
1. Creates usage records in Stripe (metered billing) or Razorpay (usage-based pricing).
2. Sends invoice events when monthly rollover happens.
3. Handles plan changes (upgrade/downgrade) by updating the tenant's plan in the DB and Stripe/Razorpay.

### Plan Change Flow

```sql
-- Admin upgrades a tenant
UPDATE tenants SET plan = 'pro', updated_at = NOW() WHERE id = $1;

-- Usage limits adjust automatically (PlanLimitsMap lookup)
-- No data migration needed — limits are enforced at runtime
-- Billing event emitted for Stripe/Razorpay sync
```

### Plan Limits Storage Rationale

Plan limits are stored as domain constants rather than in the database:

1. **Fast enforcement.** No DB query needed to determine limits. The map lookup is O(1).
2. **Single source of truth.** Limits are defined in code, not split between code and DB.
3. **Deploy-time changes.** Updating limits requires a deployment, which ensures review and testing.
4. **No DB migration for limit changes.** Adding a new plan or adjusting limits is a code change, not a schema migration.

When limits need to be overridden per-tenant (e.g., custom enterprise deal), the override is stored in the tenant's `Config` JSONB field.

## Alternatives Considered

### 1. Stripe Integration Immediately

Rejected because:
- OpsCore is pre-revenue. Integrating Stripe before product-market fit adds maintenance burden without revenue.
- Stripe's API changes frequently — integrating now would require updates before launch.
- Usage metering and enforcement are needed now to collect data on actual tenant usage patterns.

### 2. Database-Stored Plan Limits

Rejected because:
- Every request would require a DB query to fetch limits, adding latency.
- Limits are rarely changed — code constants are appropriate for the expected change frequency (quarterly).
- DB-stored limits can drift from code-intended limits (manual edits, migrations).

### 3. Enforcement in Domain Layer

Rejected because:
- Domain code must be pure Go with zero I/O. Usage metering requires DB reads/writes for current usage.
- Plan enforcement is an infrastructure concern (rate limiting), not business logic.
- The domain defines the limits (`PlanLimitsMap`) but does not enforce them.

### 4. Hard Block on First Violation

Rejected because:
- Immediate blocking without warning frustrates users who briefly exceed limits (e.g., bulk upload).
- Grace period allows users to reduce usage or upgrade before being blocked.
- Warning at 80% gives users time to adjust behavior.

## Consequences

### Benefits

1. **Enforceable limits from day one.** Plans have teeth — tenants cannot exceed their plan's resource limits even without billing integration.

2. **Data-driven plan adjustments.** Usage metering provides real data on how tenants use resources. This informs pricing, plan tiers, and infrastructure scaling decisions.

3. **Graceful user experience.** Warning at 80%, blocking only after grace period. Users are not surprised by sudden blocks.

4. **Billing-ready events.** When Stripe or Razorpay integration is implemented, the `billing_events` table provides a complete history of tenant usage. No retroactive data collection needed.

5. **Fast enforcement.** Plan limit lookup is a map access, not a DB query. Usage counter increment is the only DB write, and it is async-friendly.

6. **Plan changes are instant.** Updating the tenant's plan in the DB immediately adjusts their limits. No data migration, no reprovisioning.

### Trade-offs / Risks

1. **Usage counter writes add DB load.** Every API call and agent operation increments a usage counter. Mitigation: usage counters use a small, indexed table with upserts (`INSERT ON CONFLICT DO UPDATE`). The write is lightweight (< 1ms).

2. **Grace period creates edge cases.** If a tenant is in grace period and exceeds a second limit, what happens? Mitigation: grace period applies per-resource. The first exceeded resource triggers the warning; exceeding additional resources during grace period does not extend the grace period.

3. **Plan limits as code constants require deploys to change.** If a sales deal requires custom limits, a code deploy is needed. Mitigation: per-tenant overrides in `Tenant.Config` JSONB field override the code constants for specific tenants.

4. **No prorated billing on plan change.** If a tenant upgrades mid-cycle, their usage counters do not reset. They may hit the old plan's limits in the same cycle. Mitigation: on plan upgrade, the usage counter for the new plan starts from the tenant's current usage. If they are at 80% of starter limits, they are at 8% of pro limits. No data loss.

5. **Billing events are not yet consumed.** The `billing_events` table grows without a consumer until Stripe/Razorpay integration is implemented. Mitigation: add a TTL-based cleanup for unprocessed events older than 90 days. The Streamlit admin UI can display recent billing events for manual review.

## Related ADRs

- **ADR-0005**: Audit Trail and Usage Metering — establishes the audit event pattern that billing events follow.
- **ADR-0006**: Deterministic-First AI Policy — LLM call limits are enforced by the same usage metering system.
- **ADR-0019**: Data Retention, PII, and Document Security — storage limits interact with document retention policies and deletion schedules.
