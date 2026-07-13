# ADR-0003: Multi-Tenancy and Tenant Isolation

## Status

**Accepted** — implemented and deployed.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore serves multiple B2B businesses (tenants). Each tenant operates independently and must never see another tenant's data. The data that must be isolated includes:

1. **Vendors** — each tenant manages its own vendor roster with GST numbers, PAN cards, bank accounts, and trust evaluations.
2. **Documents** — invoices, contracts, purchase orders, and compliance documents uploaded by or on behalf of a tenant.
3. **Jobs** — processing jobs (vendor onboarding, document ingestion, compliance monitoring) are scoped to a tenant.
4. **Audit events** — every state transition, approval, and system action must be attributable to a tenant.
5. **Compliance data** — compliance chunks, gap analyses, and regulatory feeds are tenant-specific.
6. **Usage metering** — documents uploaded, OCR pages, LLM calls, and compliance checks are counted per tenant and enforced per plan.

The system must satisfy these requirements:

1. **Strong data isolation** — a tenant's data must be invisible to all other tenants at the database level, the application level, and the API level.
2. **Tenant-aware authentication** — API keys are scoped to a tenant; authenticated users carry their tenant identity.
3. **Role-based access control (RBAC)** — within a tenant, users have distinct roles with different permissions.
4. **Usage-based plan enforcement** — each tenant has a plan (starter, pro, business) with per-metric limits that are enforced at write time.
5. **Auditability** — every access and mutation must be traceable to a tenant and a user.
6. **Operational simplicity** — the isolation mechanism must not require per-tenant infrastructure (separate databases, separate deployments).

### Current Architecture Before Multi-Tenancy

Before this decision, OpsCore was single-tenant:

```
POST /vendors → parse → create DB record (no tenant_id) → return
```

There was no `tenant_id` column on any table, no auth middleware, no RBAC, no usage metering. The system assumed a single implicit tenant ("default").

## Decision

All OpsCore data will use **row-level tenant isolation** with `tenant_id` on every table, enforced at three layers:

1. **Application layer** — tenant-aware middleware extracts tenant from request context and injects it into every query.
2. **Database layer** — PostgreSQL Row-Level Security (RLS) provides defense-in-depth, ensuring cross-tenant leakage is impossible even if the application layer is bypassed.
3. **API layer** — `X-Tenant-ID` header or auth-derived tenant scoping ensures every request is attributed to a tenant.

RBAC with five roles (Owner, OpsAdmin, Reviewer, Auditor, Viewer) controls intra-tenant permissions. Usage metering with monthly rolling counters per tenant enforces plan-based limits.

### Architecture Overview

```
Client Request
    │
    ▼
┌──────────────────────────────┐
│  Auth Middleware              │  ← Reads Authorization: Bearer <token>
│  (middleware/auth/auth.go)    │     or X-API-Key header
│                               │     Resolves via AuthProvider → domain.User
│                               │     If X-Tenant-ID set, verifies match
│                               │     Injects User into context
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Tenant Middleware            │  ← Reads X-Tenant-ID or X-Tenant-Slug
│  (middleware/tenant/tenant.go)│     Resolves via TenantProvider → domain.Tenant
│                               │     Sets PostgreSQL app.tenant_id for RLS
│                               │     Injects Tenant into context
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Handler / Agent              │  ← Extracts tenant/user from context
│                               │     All DB queries include tenant_id=$1
│                               │     UsageProvider checks limits before writes
│                               │     AuditEvent records tenant_id + user
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  PostgreSQL RLS               │  ← Defense-in-depth:
│                               │     Every row has tenant_id
│                               │     RLS policy: WHERE tenant_id =
│                               │       app.current_tenant_id()
│                               │     Bypassed only within transaction
│                               │     that set app.tenant_id
└──────────────────────────────┘
```

### Why Row-Level Tenant Isolation (Shared Table) Instead of Separate Databases or Schemas

1. **Operational simplicity.** One PostgreSQL cluster, one connection pool, one migration set. No per-tenant connection management, no per-tenant schema migrations to coordinate.
2. **Cross-tenant operations.** Admin dashboards, system-wide metrics, and ops tooling can query across tenants (e.g., `SELECT * FROM usage_records WHERE plan = 'starter'`) without N separate queries.
3. **Shared connection pool.** pgxpool efficiently manages connections for all tenants. Separate databases would require N pools or connection routing middleware.
4. **Migration velocity.** Schema changes apply once and immediately affect all tenants. Schema-per-tenant would require N migration runs and complicate rollback coordination.
5. **RLS as defense-in-depth.** PostgreSQL RLS policies ensure that even if application-level tenant checks are accidentally omitted, cross-tenant leakage is prevented at the database level.

### Why RBAC with Fixed Roles Instead of Custom Permissions

1. **Simplicity of audit.** Five well-defined roles with explicit permission sets are easier to audit than custom role-permission matrices that drift over time.
2. **Predictable access control.** Every user's capabilities are determined by their role. No need to evaluate permission combinations.
3. **Testability.** The permission matrix is a single map of `map[Role][]Permission` that can be tested exhaustively in under 100 lines of test code.
4. **Sufficient granularity.** For B2B back-office operations, the five roles (Owner → OpsAdmin → Reviewer → Auditor → Viewer) map naturally to organizational hierarchies. Finer-grained permissions can be added later as new `Permission` constants.

### Why API-Key-Based Authentication with Role Scoping

1. **Machine-to-machine suitability.** OpsCore's primary consumers are scripts, webhooks, and integration tools, not interactive user sessions. API keys are the natural auth mechanism.
2. **Role scoping.** Each API key is bound to a user record with a role. This means a reviewer's API key cannot upload documents, even if the key is leaked.
3. **Stateless verification.** Token lookup is O(1) via hash map or DB index. No session state, no JWT expiry management.
4. **Dev/test simplicity.** `StaticAPIKeyProvider` maps keys to users in memory — zero infrastructure needed for development.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Tenant model** | `domain.Tenant` struct with ID, Name, Slug, Plan, Status, Config | Simple struct, no ORM dependency; `NewTenant()` sets defaults (plan=starter, status=active) |
| **Tenant resolution** | `X-Tenant-ID` (primary) or `X-Tenant-Slug` (fallback) header | ID is stable and unique; Slug is human-readable for API consumers |
| **Default tenant** | Fallback to `default` when no header is set | Backward compatibility for single-tenant deployments and testing |
| **RLS function** | `app.current_tenant_id()` PostgreSQL function | Returns `current_setting('app.tenant_id')` or `'default'`; set via `SET app.tenant_id` per session |
| **RLS enforcement** | `tenant_isolation` policy on every tenant-scoped table | `USING (tenant_id = app.current_tenant_id())` |
| **RLS scope** | 8 tables: `jobs`, `vendors`, `documents`, `audit_events`, `hitl_requests`, `compliance_chunks`, `usage_records`, `tenants` | Tenants table is readable without tenant context for bootstrap |
| **Auth tokens** | `Authorization: Bearer <token>` or `X-API-Key` header | Bearer is standard; X-API-Key is a common fallback for legacy clients |
| **Auth provider** | `AuthProvider` interface with `StaticAPIKeyProvider` (dev) and DB-backed (prod) | Interface-based, swappable; static provider enables zero-infra development |
| **Roles** | 5 fixed roles: Owner (admin:*), OpsAdmin (9 permissions), Reviewer (4), Auditor (5), Viewer (3) | Maps to B2B organizational hierarchy — owner, ops, reviewer, auditor, viewer |
| **Permission model** | `Permission` constants + `RolePermissions` map + `HasPermission()` method | Simple enum, no role hierarchy to evaluate; `admin:*` acts as wildcard |
| **Usage metering** | `domain.UsageRecord` + `PlanLimits` map + `UsageProvider` interface | Deterministic limit check before writes; monthly rolling periods |
| **Usage metrics** | 4 metrics: `documents_uploaded`, `ocr_pages`, `llm_calls`, `compliance_checks` | Covers all billable operations in the current system |
| **Plans** | 3 plans: starter (100 docs, 0 LLM), pro (1000 docs, 100 LLM), business (10000 docs, unlimited LLM) | Starter is free tier with LLM disabled; business is unlimited in LLM |
| **Audit trail** | `domain.AuditEvent` with TenantID, Actor, Action, OldState, NewState, Timestamp | Every state transition is recorded; queryable by tenant |

## Alternatives Considered

### 1. Separate Database Per Tenant

Rejected because:

- **Operational overhead.** N databases means N connection pools, N migration runs, N backup schedules. At 100+ tenants, this becomes unmanageable.
- **No cross-tenant observability.** System-wide metrics require querying every database. Admin dashboards cannot aggregate across tenants.
- **Connection management overhead.** pgxpool cannot be shared; each tenant requires a separate pool or a connection router.
- **Migration coordination.** Schema changes must be applied to every tenant database. Rollback of a failed migration requires reverting N databases.

### 2. Schema-Per-Tenant (PostgreSQL Schemas)

Rejected because:

- **Migration complexity.** DDL changes must be applied to every schema. While this can be automated, it adds failure points (one schema migration fails → tenant is on a different schema version).
- **Connection pool sharing.** pgxpool shares connections, but `SET search_path TO tenant_schema` must be set per session. This requires middleware to reset the search path on every request.
- **Query complexity.** Cross-schema queries for admin tooling require explicit schema qualification or `search_path` juggling.
- **Limited tooling support.** ORMs and migration tools often assume a single schema per database.

### 3. JWT-Based Tenant Claims

Rejected because:

- **Token revocation complexity.** JWT tokens cannot be revoked without a blocklist, adding infrastructure and latency.
- **Tenant change requires token reissue.** If a user's tenant mapping changes, existing tokens are still valid. The application must check tenant status on every request anyway.
- **No advantage over API keys.** For machine-to-machine auth, API keys provide the same scoping with simpler revocation (delete key from DB, next request fails).
- **JWT parsing overhead.** Every request requires signature verification. API key lookup is O(1) with no cryptographic cost.

### 4. Application-Level Only Isolation (No RLS)

Rejected because:

- **Single point of failure.** If a developer forgets a `WHERE tenant_id = $1` clause, data leaks silently. RLS provides defense-in-depth.
- **Difficult to audit.** Proving that every query has a tenant filter requires code review. RLS policy is a single source of truth that can be verified with `SELECT * FROM pg_policies`.
- **Bulk operations are risky.** `UPDATE` or `DELETE` without a tenant filter affects all rows. RLS prevents this even if the application query is wrong.

## Consequences

### Benefits

1. **Strong, multi-layered isolation.** Three layers of enforcement: API middleware rejects requests with wrong tenant, application layer injects `tenant_id` in every query, RLS prevents leakage at the database level. All three must fail simultaneously for a data leak to occur.

2. **Operationally simple.** Single PostgreSQL database, single connection pool, single migration set. Adding a tenant is a row insert into the `tenants` table. No per-tenant infrastructure required.

3. **Testable isolation guarantees.** The `TestRLSIsolation` integration test (in `internal/adapters/postgres/rls_test.go`) inserts data for two tenants within a transaction and verifies that only the current tenant's rows are visible. This test proves the RLS policy is effective.

4. **Exhaustive permission matrix.** The `RolePermissions` map and `HasPermission()` method are tested with 300 lines of test code covering every role-permission combination, including edge cases (unknown role, admin wildcard).

5. **Plan-based usage enforcement.** `UsageProvider.CheckLimit()` is called before every billable write. The `PlanLimits` map makes plan limits visible and auditable. Unlimited metrics (business plan LLM calls = -1) are handled explicitly.

6. **Auditable access.** Every request carries a tenant ID and user identity. Every state transition is recorded as an `AuditEvent` with tenant, actor, old state, new state, and timestamp. Cross-tenant audit queries are possible (admin tooling).

7. **Graceful fallback for single-tenant deployments.** When no `X-Tenant-ID` header is present, the tenant middleware injects a `default` tenant. This means existing single-tenant deployments continue to work without modification.

8. **RLS function tested.** `TestRLSFunctionExists` and `TestRLSFunctionWithContext` verify that `app.current_tenant_id()` returns `'default'` when unset and the correct tenant ID when `app.tenant_id` is configured.

### Trade-offs / Risks

1. **RLS overhead on every query.** Every query on an RLS-enabled table evaluates the `tenant_isolation` policy. For queries that already include `WHERE tenant_id = $1`, this is redundant overhead (the policy is evaluated and passes). Mitigation: the overhead is negligible — PostgreSQL RLS adds < 5% overhead for equality checks on indexed columns. The `tenant_id` column is indexed (as part of composite indexes) on all tables.

2. **Static tenant provider in development.** In `cmd/server/main.go`, the tenant middleware uses a `staticTenantProvider` (hardcoded to know only about the `default` tenant). A database-backed `TenantProvider` is not yet implemented. Mitigation: the provider interface (`providers.TenantProvider`) is defined and ready; a DB-backed implementation can be added without changing the middleware or handler code.

3. **Static API key provider in development.** The auth middleware defaults to `StaticAPIKeyProvider` with hardcoded dev keys. Production deployments must implement a database-backed `AuthProvider`. Mitigation: the `AuthProvider` interface is defined; `StaticAPIKeyProvider` is explicitly documented as dev-only.

4. **Tenant middleware interacts with auth middleware order.** The auth middleware checks `X-Tenant-ID` against the user's tenant. The tenant middleware also resolves `X-Tenant-ID`. If both are applied in the wrong order, the tenant middleware might resolve a tenant that the auth middleware then rejects. Mitigation: standard middleware order is auth → tenant → handler. The auth middleware runs first, validates the token, and optionally checks `X-Tenant-ID` against the token's user. The tenant middleware then resolves the tenant from the header (which is already validated to match).

5. **Usage record contention.** Multiple concurrent processes may try to increment the same usage counter. Mitigation: use atomic `UPDATE usage_records SET count = count + $1` with `ON CONFLICT` for insertion. PostgreSQL row-level locking ensures correctness without application-level mutexes.

6. **Static plan limits in domain code.** `PlanLimits` is a map in `internal/domain/usage.go`. Changing plan limits requires a code deploy. Mitigation: plan limits change infrequently. When they do change, the change is visible in version control and auditable. Future enhancement: make limits configurable per-tenant via the `Tenant.Config` JSONB field.

## Implementation Details

### Tenant Model

```go
// internal/domain/tenant.go
type Tenant struct {
    ID        string         `json:"id"`
    Name      string         `json:"name"`
    Slug      string         `json:"slug"`
    Plan      string         `json:"plan"`
    Status    string         `json:"status"`
    Config    map[string]any `json:"config,omitempty"`
    CreatedAt time.Time      `json:"created_at"`
    UpdatedAt time.Time      `json:"updated_at"`
}
```

- `NewTenant(id, name, slug)` creates a tenant with default plan (`starter`) and status (`active`).
- `Config` is a free-form JSONB map for per-tenant feature flags and settings.
- Plans: `starter`, `pro`, `business`. Statuses: `active`, `suspended`.

### Auth / RBAC Model

```go
// internal/domain/auth.go
type Role string    // RoleOwner, RoleOpsAdmin, RoleReviewer, RoleAuditor, RoleViewer
type Permission string // document:upload, vendor:create, admin:*, etc.

var RolePermissions = map[Role][]Permission{
    RoleOwner:    {PermissionAdmin},                          // admin:* wildcard
    RoleOpsAdmin: {document:upload, document:view, vendor:create, ...},
    RoleReviewer: {document:view, vendor:view, vendor:approve, compliance:view, ...},
    RoleAuditor:  {document:view, vendor:view, compliance:view, audit:view, metrics:view},
    RoleViewer:   {document:view, vendor:view, compliance:view},
}

type User struct {
    ID       string `json:"id"`
    TenantID string `json:"tenant_id"`
    Email    string `json:"email"`
    Role     Role   `json:"role"`
    Name     string `json:"name"`
}

func (u *User) HasPermission(perm Permission) bool {
    // admin:* acts as wildcard — grants all permissions
}
```

### Auth Middleware Flow

```
1. extractToken(r) → reads Authorization: Bearer <token> or X-API-Key
2. m.provider.Authenticate(ctx, token) → domain.User or nil
3. If X-Tenant-ID header set AND user.TenantID != header → 403 Tenant Mismatch
4. WithUser(ctx, user) → inject into context
5. RequirePermission(perm) → middleware that checks User.HasPermission
   → 401 if no user in context
   → 403 if user lacks permission
```

### Tenant Middleware Flow

```
1. Read X-Tenant-ID or X-Tenant-Slug from request headers
2. Resolve via TenantProvider.GetTenant() or GetTenantBySlug()
3. If no header set → fall back to "default" tenant
4. If setTenantContext hook configured → SET app.tenant_id = resolved.ID
   (enables PostgreSQL RLS for this session)
5. WithTenant(ctx, tenant) → inject into context
```

### PostgreSQL RLS Implementation

The `app.current_tenant_id()` function:

```sql
CREATE SCHEMA IF NOT EXISTS app;
CREATE FUNCTION app.current_tenant_id() RETURNS text AS $$
    SELECT COALESCE(current_setting('app.tenant_id', TRUE), 'default');
$$ LANGUAGE SQL STABLE;
```

Each tenant-scoped table has:

```sql
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON jobs
    USING (tenant_id = app.current_tenant_id());
```

Tables with RLS policies (verified by `TestRLSPolicyExists`):
- `jobs`
- `vendors`
- `documents`
- `audit_events`
- `hitl_requests`
- `compliance_chunks`
- `usage_records`
- `tenants` (readable without tenant context — `allowEmpty: true`)

The RLS isolation is tested in `TestRLSIsolation`: within a transaction, data for two tenants is inserted; only the current tenant's rows are visible when queried.

### Usage Metering

```go
// internal/domain/usage.go
type Metric string   // documents_uploaded, ocr_pages, llm_calls, compliance_checks

type UsageRecord struct {
    TenantID    string    `json:"tenant_id"`
    Metric      Metric    `json:"metric"`
    Count       int64     `json:"count"`
    PeriodStart time.Time `json:"period_start"`
    PeriodEnd   time.Time `json:"period_end"`
    UpdatedAt   time.Time `json:"updated_at"`
}

var PlanLimits = map[string]map[Metric]int64{
    "starter":  {documents_uploaded: 100, ocr_pages: 100, llm_calls: 0, compliance_checks: 50},
    "pro":      {documents_uploaded: 1000, ocr_pages: 1000, llm_calls: 100, compliance_checks: 500},
    "business": {documents_uploaded: 10000, ocr_pages: 10000, llm_calls: -1, compliance_checks: 5000},
}
```

- `GetLimit(plan, metric)` returns the limit; -1 means unlimited (`IsUnlimited()`).
- `UsageProvider` interface (`providers/interfaces.go:107-113`) defines `IncrementUsage`, `GetUsage`, `GetCurrentPeriodUsage`, `CheckLimit`.
- Limits are checked before write operations. Exceeding a limit returns an error that should be surfaced to the caller.

### Queue Tenant Propagation

All queue messages carry `TenantID`:

```go
// internal/agents/vendor_agent.go
type VendorJob struct {
    VendorData *VendorData `json:"vendor_data"`
    TenantID   string      `json:"tenant_id"`
    JobID      string      `json:"job_id"`
}
```

Same pattern for `ComplianceJob` (`tenant_id` field) and document jobs. The worker extracts `TenantID` from the message and passes it through every database call. This ensures tenant isolation is maintained through the asynchronous workflow.

### Data Flow for a Multi-Tenant Request

```
POST /vendors (X-Tenant-ID: tenant-alpha, Authorization: Bearer <ops-admin-key>)

1. Auth Middleware:
   - Extract token: "ops-admin-key"
   - Resolve to User{ID: "user-opsadmin", TenantID: "tenant-alpha", Role: RoleOpsAdmin}
   - X-Tenant-ID check: "tenant-alpha" == user.TenantID → pass
   - Inject User into context

2. Tenant Middleware:
   - Read X-Tenant-ID: "tenant-alpha"
   - Resolve to Tenant{ID: "tenant-alpha", Plan: "pro", Status: "active"}
   - SET app.tenant_id = 'tenant-alpha' (for RLS)
   - Inject Tenant into context

3. Handler:
   - Check RequirePermission(vendor:create) → ops-admin has this permission
   - Extract tenant from tenant.FromContext(ctx)
   - Extract user from auth.FromContext(ctx)
   - Parse vendor data, create Vendor with TenantID = "tenant-alpha"

4. Usage Check:
   - CheckLimit("tenant-alpha", "documents_uploaded") → 50/1000 used → pass
   - IncrementUsage("tenant-alpha", "documents_uploaded", 1)

5. Database:
   - INSERT INTO vendors (id, tenant_id, name, ...) VALUES (...)
   - RLS automatically restricts to tenant-alpha

6. Queue:
   - Enqueue VendorJob{TenantID: "tenant-alpha", ...}

7. Audit:
   - AppendAuditEvent{TenantID: "tenant-alpha", Actor: "user-opsadmin", Action: "VENDOR_CREATED", ...}
```

## File Map

### Existing Files

| File | Purpose |
|------|---------|
| `internal/domain/tenant.go` | `Tenant` struct, `NewTenant()` constructor |
| `internal/domain/tenant_test.go` | Tests for tenant creation and default values |
| `internal/domain/auth.go` | `Role`, `Permission`, `User`, `RolePermissions` map, `HasPermission()` |
| `internal/domain/auth_test.go` | Exhaustive tests for all role-permission combinations |
| `internal/domain/usage.go` | `Metric`, `UsageRecord`, `PlanLimits`, `GetLimit()`, `IsUnlimited()` |
| `internal/domain/usage_test.go` | Tests for plan limits and unlimited detection |
| `internal/domain/types.go` | All domain types with `TenantID` fields (Job, Vendor, Document, AuditEvent, HITLRequest, ComplianceChunk, ComplianceRecord) |
| `internal/middleware/auth/auth.go` | `AuthMiddleware`, `AuthProvider` interface, `RequirePermission`, `FromContext`, `StaticAPIKeyProvider` |
| `internal/middleware/auth/auth_test.go` | Auth middleware tests (valid tokens, missing tokens, tenant mismatch, permission checks, benchmarks) |
| `internal/middleware/tenant/tenant.go` | `TenantMiddleware`, `TenantProvider` interface, `FromContext`, RLS set-context hook |
| `internal/middleware/tenant/tenant_test.go` | Tenant middleware tests (ID resolution, slug resolution, unknown tenants, default fallback) |
| `internal/adapters/postgres/rls_test.go` | RLS integration tests (function exists, function with context, policy exists on all tables, actual isolation test) |
| `internal/providers/interfaces.go` | `TenantProvider` (lines 94-100), `UsageProvider` (lines 107-113), `AuthProvider` (referenced in middleware) |

## RLS Policy Verification

The `TestRLSPolicyExists` test (in `internal/adapters/postgres/rls_test.go`) verifies every tenant-scoped table:

| Table | RLS Enabled | tenant_isolation Policy |
|-------|-------------|------------------------|
| `jobs` | ✓ | ✓ |
| `vendors` | ✓ | ✓ |
| `documents` | ✓ | ✓ |
| `audit_events` | ✓ | ✓ |
| `hitl_requests` | ✓ | ✓ |
| `compliance_chunks` | ✓ | ✓ |
| `usage_records` | ✓ | ✓ |
| `tenants` | ✓ | ✓ (allowEmpty: true) |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

Tenant isolation is entirely deterministic:

- **Tenant resolution**: Pure Go struct lookup by ID or slug — no LLM, no external API.
- **Auth token resolution**: Map lookup by API key — deterministic, O(1).
- **Permission checks**: `HasPermission()` iterates over a fixed map — no LLM, no external dependencies.
- **RLS policy evaluation**: PostgreSQL equality check on `tenant_id` — deterministic, indexed.
- **Usage limit checks**: Map lookup by plan + metric — no LLM, no external API.
- **Audit events**: Recorded as structured data — no LLM interpretation.

### Observability

- Every request carries tenant ID and user identity through the context.
- `AuditEvent` records every state transition with tenant, actor, old state, new state, and timestamp.
- Usage metering records per-tenant metrics with monthly periods.
- RLS tests verify isolation programmatically — `TestRLSIsolation` proves that cross-tenant queries return empty results.

## Related ADRs

- **ADR-0001**: Retrieval Architecture for Compliance & Document Search — uses tenant isolation (`WHERE tenant_id = $1` as first filter) and RLS as defense-in-depth for compliance chunk queries.
- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow — all queue messages carry `TenantID`, all DB queries include tenant filter, RLS provides defense-in-depth.
