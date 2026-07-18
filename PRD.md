# OpsCore — Product Requirements Document v6.0

**Version:** 6.0 — Manufacturing Pivot  
**Date:** 2026-07-18  
**Status:** Active — Product Pivot in Progress  
**Repository:** github.com/aparna/opscore · branch: main  
**Live URL:** *(coming soon)*

---

## Implementation Progress

> Checkpoint `pivot-phase-1.3-complete` (commit `5001cbf`). This milestone is a
> coherent internal boundary: deterministic domain logic + transitional
> agent/worker wiring, no schema expansion yet.

### Phase 1 — Signal Ingestion + Mismatch Detection

| Sub-phase | Status | What landed |
| --------- | ------ | ----------- |
| 1.1 Domain entities | ✅ Done | `PurchaseOrder`, `GoodsReceipt`, `Invoice` (zero-I/O, tenant_id + version, GSTIN-validated) |
| 1.2 Mismatch engine | ✅ Done | `DetectMismatch` (line match by `POLineRef` + ItemCode fallback), `ExceptionCase` typed output, 6 mismatch types (MISSING_GRN, PARTIAL_RECEIPT, QTY/PRICE/TAX_VARIANCE, VENDOR_MISMATCH), tenant-scoped `IsDuplicate` |
| 1.3 Agent/worker wiring | ✅ Done | GRN classifier, zero-I/O OCR mapper (`OCRPayload`), `SignalAgent` reusing `DocumentAgent` OCR+classifier, HITL for low confidence / validation errors / GSTIN conflicts / payment-affecting mismatches, `QueueSignal` + `processSignalJob`. Transitional persistence in `jobs.extracted_data` |

### Next milestone (not started)

- **Phase 1.4A — Persistence:** dedicated `purchase_orders` / `goods_receipts` / `invoices` tables with `tenant_id`, RLS, `version`, indexes; explicit `DBProvider` methods; Postgres adapter; integration tests for RLS + optimistic locking + round-trip.
- **Phase 1.4B — API surface:** `POST /signals/upload` mirroring the existing upload middleware chain (auth, tenant, rate-limit, usage); endpoint + E2E tests. `jobs.extracted_data` stays as a compatibility bridge until table-backed reads are green.

### Guardrails held at this checkpoint

- Domain stays zero-I/O; no LLM in classification, validation, line matching, or mismatch logic.
- Queue-backed orchestration, optimistic job locking, append-only audit events, Slack HITL, and tenant isolation all preserved from the existing platform.

---

## Architecture Decisions

OpsCore's design is governed by Architecture Decision Records (ADRs) in `docs/architecture/adr/`. The existing ADR discipline remains valid, but the product framing shifts from back-office autonomy to an agentic operations control tower for MSME manufacturing. Accepted platform ADRs for tenant isolation, queue semantics, observability, deployment target, and review-queue UX remain foundational and should be preserved. [file:893]

Recommended active ADR set for v6.0:

| ADR  | Title                                                | Status   |
| ---- | ---------------------------------------------------- | -------- |
| 0002 | Vendor Onboarding — Asynchronous Real-Time Workflow  | Accepted |
| 0003 | Multi-Tenancy and Tenant Isolation                   | Accepted |
| 0005 | Audit Trail and Usage Metering                       | Proposed |
| 0006 | Deterministic-First AI Policy                        | Proposed |
| 0007 | Structured Ingestion and Semantic Chunking           | Proposed |
| 0008 | Retrieval Router — Deterministic, Vectorless, Hybrid | Proposed |
| 0009 | Bounded Agent Loops and Fallback Policy              | Proposed |
| 0010 | Human-in-the-Loop Approval Boundary                  | Proposed |
| 0011 | Queue Semantics, Retries, Idempotency, and DLQ       | Accepted |
| 0012 | Observability and LLMOps                             | Accepted |
| 0013 | Live-Provider Testing Strategy                       | Proposed |
| 0014 | Agentic AI Verification Strategy                     | Proposed |
| 0015 | Deployment Target and Cloud-Agnostic Runtime         | Accepted |
| 0016 | Search/Indexing Backend Choice                       | Proposed |
| 0017 | Billing Readiness and Plan Limits                    | Proposed |
| 0018 | Manufacturing Operational State Model                | Proposed |
| 0019 | Data Retention, PII, and Document Security           | Proposed |
| 0020 | Review Queue and Operator UX Boundary                | Accepted |
| 0021 | Risk Detection and Action Orchestration Policy       | Proposed |
| 0022 | ERP / Sheets / Slack Integration Boundary            | Proposed |

---

## One Paragraph Description

OpsCore is a multi-tenant agentic operations control tower for MSME manufacturers. It ingests fragmented operational signals from purchase orders, goods receipts, invoices, stock sheets, ERP exports, supplier messages, and uploaded documents; builds a live operational state; detects emerging risks such as material delays, blocked payments, stock-out threats, and repeat vendor failures; explains likely root causes; and coordinates next-best actions through specialized agents, deterministic policy rules, and human approvals in Slack. The platform is built in Go with a deterministic-first architecture, using AI only for document understanding, summarization, and evidence-backed reasoning where static rules are insufficient. [cite:688][cite:689]

---

## Product Vision

Most MSME manufacturers do not fail because they lack software screens. They fail because critical operational problems are discovered too late and resolved too slowly. POs sit in email, goods receipts are delayed, invoices do not match, stock risk emerges silently, vendor follow-ups happen over calls or WhatsApp, and the owner becomes the human control tower.

OpsCore creates a digital control tower above this fragmented environment. It continuously watches operational signals, identifies what matters, explains why it matters, and helps teams act before surprises turn into dispatch misses, production stoppages, or payment blocks.

The goal is not to make old clerical work 20% faster. The goal is to create a new operating model where the system absorbs uncertainty, correlates weak signals, proposes actions, and turns humans into supervisors of a governed operations layer.

---

## Problem Statement

Indian MSME manufacturers operate across fragmented systems and informal channels:

- POs may originate in ERPNext, email, or spreadsheets.
- GRNs may be delayed, incomplete, or logged manually.
- Invoices arrive as PDFs, scans, or shared files.
- Stock positions may live in exports or Google Sheets.
- Supplier communication often happens outside the system of record.
- Escalations are reactive, tribal, and person-dependent.

This creates four recurring pain clusters:

1. **Supply risk blindness** — teams discover late material issues after they have already affected production.
2. **Exception chaos** — mismatches, blocks, and missing documents accumulate without clear prioritization.
3. **Vendor coordination drag** — skilled employees spend hours chasing suppliers for confirmations, revisions, and dispatch updates.
4. **Root-cause ambiguity** — people know a process is stuck, but not why, who owns it, or what action will unblock it.

OpsCore solves this by turning fragmented records into a live operational state and by using bounded agents to detect, explain, and coordinate resolution of business-critical issues.

---

## Product Philosophy

**Default to deterministic policy. Use AI for ambiguity, synthesis, and coordination.**

Every decision that can be encoded as a deterministic rule must be. This is not only a cost optimization; it is a reliability guarantee.

Deterministic logic governs:
- line matching,
- quantity/price/tax tolerance checks,
- duplicate invoice detection,
- vendor and item normalization rules,
- state transitions,
- risk thresholds,
- escalation policies,
- approval requirements.

AI is used only where ambiguity or language complexity is unavoidable:
- OCR and document extraction repair,
- summarizing incidents and exceptions,
- generating evidence-backed root-cause hypotheses,
- drafting supplier/internal follow-ups,
- answering operational questions over structured and retrieved context.

OpsCore is therefore not a free-roaming chatbot. It is a governed control tower with deterministic policy at its core and bounded AI assistance at the edge.

---

## Product Scope

### In Scope for v6.0
- Multi-tenant SaaS platform for MSME manufacturing operations.
- Signal ingestion from uploads, ERP exports, Google Sheets, and supplier-facing documents.
- Unified operational state across procurement, receipts, invoices, inventory, and vendor issues.
- Risk detection for shortages, delays, mismatches, blocked payments, and unresolved exceptions.
- Root-cause explanation and evidence synthesis.
- Action orchestration through Slack and admin APIs.
- Human-in-the-loop review for sensitive actions.
- Audit logs, usage metering, observability, and testable workflows.

### Out of Scope for v6.0
- MES replacement.
- PLC / IoT heavy shop-floor control.
- Autonomous purchasing.
- Full production scheduling optimizer.
- Robotics or machine-vision assembly control.
- General-purpose enterprise chatbot.

---

## Primary Users

- MSME owner / operator
- Procurement executive
- Accounts payable / finance operations
- Stores / receiving supervisor
- Plant operations coordinator
- Auditor / reviewer

---

## Jobs To Be Done

- Tell me what is likely to break operations next.
- Show me which invoices or receipts are blocked and why.
- Detect mismatches before they become payment or dispatch issues.
- Highlight which suppliers are causing repeated operational risk.
- Recommend the next best action for each high-priority issue.
- Draft follow-up or escalation content without losing auditability.
- Keep a daily operational brief ready for decision-makers.

---

## Core Product Modules

### 1. Signal Ingestion
Ingest and normalize operational signals from:
- PDF/image uploads,
- CSV/XLSX imports,
- ERPNext exports and webhooks,
- Google Sheets imports,
- supplier-facing document attachments,
- optional email ingestion later.

### 2. Operational State Builder
Convert fragmented inputs into a live operational state model:
- open purchase commitments,
- received quantities,
- invoice status,
- blocked payables,
- vendor issue patterns,
- material availability and risk.

### 3. Risk Detection
Detect and rank issues such as:
- missing GRN,
- invoice mismatch,
- duplicate invoice,
- delayed receipt,
- unusual vendor failure pattern,
- blocked payment,
- material shortage risk.

### 4. Root Cause Copilot
Explain:
- what happened,
- why the system thinks it happened,
- what evidence supports that hypothesis,
- which downstream entities are affected.

### 5. Action Orchestration
Generate or coordinate:
- supplier follow-up drafts,
- owner escalation notes,
- review packets,
- hold / approve / investigate actions,
- revisit reminders for unresolved incidents.

### 6. Human Review
Route sensitive or low-confidence cases to Slack or admin review:
- payment-affecting mismatches,
- low-confidence extraction,
- high-severity material risk,
- conflicting evidence.

### 7. Supervisor Digest
Produce:
- daily operations briefing,
- top priority queue,
- blocked value summary,
- at-risk materials,
- repeat supplier issues,
- aging unresolved exceptions.

---

## System Architecture

### Runtime Stack

| Layer             | Technology                       | Reason                                                       |
| ----------------- | -------------------------------- | ------------------------------------------------------------ |
| Language          | Go 1.25                          | Single compiled binary, high performance, deterministic behavior |
| API               | REST (`net/http`)                | Full routing/middleware control with minimal framework lock-in |
| Architecture      | Hexagonal (Ports & Adapters)     | Domain remains zero-I/O and testable                         |
| Database          | PostgreSQL 16                    | Operational state, audit, search, RLS                        |
| Storage           | MinIO / S3-compatible            | Raw documents and evidence artifacts                         |
| Queue             | Redis 7                          | Async workflows, retries, leases, DLQ                        |
| Auth              | API-key + RBAC                   | Tenant-safe programmatic access                              |
| Tenant isolation  | `tenant_id` + RLS                | API, app, and database enforcement                           |
| OCR               | Sarvam Document Intelligence     | India-oriented OCR/document extraction                       |
| LLM               | Sarvam-M                         | Extraction repair, summarization, root-cause wording         |
| Fallback LLM      | OpenRouter free model            | Optional low-cost fallback path                              |
| HITL              | Slack Block Kit + Admin API      | Existing operator workflow integration                       |
| LLM Observability | Langfuse Cloud                   | Traces, latency, token, and cost tracking                    |
| Admin UI          | Streamlit or lightweight web UI  | Read/write only through Go REST API                          |
| Deployment        | Docker Compose → Cloud Run / GCP | Cloud-agnostic target using existing path                    |
| CI/CD             | GitHub Actions                   | Tests, build, deploy                                         |

This stack preserves the strengths of v5.0 while shifting the product purpose from back-office automation to operations intelligence and action coordination. [file:893][web:881][web:882][web:889]

---

## Agent Architecture

OpsCore uses specialized agents with bounded responsibilities.

| Agent            | Purpose                                                      |
| ---------------- | ------------------------------------------------------------ |
| Signal Agent     | Classify and normalize incoming operational signals          |
| State Agent      | Build and update live operational state                      |
| Risk Agent       | Detect anomalies, shortages, delays, mismatches, and blocked flows |
| Root Cause Agent | Synthesize likely causes and supporting evidence             |
| Action Agent     | Recommend next actions and draft follow-up/escalation content |
| Review Agent     | Prepare HITL packets and route approvals                     |
| Supervisor Agent | Generate daily and on-demand operational briefings           |

### Design Rules
- Agents may reason, route, summarize, and propose.
- Agents may not override deterministic policy.
- Agents may not invent missing values.
- Agents may not perform unbounded retries.
- Sensitive actions require review or explicit policy approval.

---

## Core Workflows

### Workflow 1: Signal Ingestion
**Inputs**
- PO documents
- GRN documents
- Invoice documents
- Stock reports
- ERPNext exports/webhooks
- Google Sheets snapshots

**Pipeline**
1. Authenticate request and resolve tenant context.
2. Validate payload and plan limits.
3. Store raw artifact in MinIO.
4. Run deterministic classifier plus OCR/extraction where needed.
5. Normalize into canonical entities and line items.
6. Emit operational events into queue-backed workflows.
7. Record audit and usage events.

### Workflow 2: Operational State Build
1. Merge incoming entities into current operational graph/state.
2. Link PO, GRN, invoice, vendor, item, and stock context.
3. Detect pending, resolved, and ambiguous linkages.
4. Maintain current state for API, agents, and briefings.

### Workflow 3: Risk Detection
1. Evaluate deterministic risk rules.
2. Score issue severity and likely impact.
3. Group related signals into an incident or exception case.
4. Route to root-cause or action workflows.

### Workflow 4: Root-Cause Analysis
1. Gather supporting records and prior incidents.
2. Build evidence set.
3. Generate root-cause hypothesis with confidence.
4. Determine what downstream workflows may be affected.

### Workflow 5: Action Orchestration
1. Choose next-best action using policy and issue class.
2. Draft supplier/internal communication if needed.
3. Create review request or execute low-risk action.
4. Schedule revisit/escalation timers for unresolved issues.

### Workflow 6: Supervisor Digest
1. Aggregate current issues by urgency and impact.
2. Rank top risks.
3. Summarize blocked value, supplier patterns, and material exposure.
4. Deliver digest through API and Slack.

---

## Data Model Overview

### Keep Existing Platform Entities
- Tenant
- User
- Job
- Document
- AuditEvent
- HITLRequest
- UsageRecord

### Add / Elevate Manufacturing Entities
- Site
- Vendor
- Item
- PurchaseOrder
- PurchaseOrderLine
- GoodsReceipt
- GoodsReceiptLine
- Invoice
- InvoiceLine
- InventorySnapshot
- RiskSignal
- ExceptionCase
- RootCauseHypothesis
- ActionPlan
- CoordinationTask
- DailyBriefing

### Key Modeling Principle
The system stores both:
- **transactional facts**: POs, receipts, invoices, stock, messages;
- **operational interpretations**: risks, incidents, root causes, action plans.

This is the difference between a record-keeping system and a control tower.

---

## API Surface

### Existing Platform Endpoints to Preserve
- `GET /health`
- `GET /metrics/llm-summary`
- `GET /metrics/workflow-summary`
- `GET /admin/review-queue`
- `POST /admin/review-queue`
- `GET /admin/usage-summary`
- `POST /slack/webhook`

### New Core Endpoints
- `POST /signals/upload`
- `POST /signals/import/erpnext`
- `POST /signals/import/sheets`
- `GET /ops/summary`
- `GET /ops/priority-queue`
- `GET /exceptions`
- `GET /exceptions/{id}`
- `POST /exceptions/{id}/resolve`
- `GET /risks`
- `GET /risks/{id}`
- `GET /briefings/latest`
- `GET /vendors/{id}/history`
- `GET /materials/{id}/risk`
- `POST /actions/{id}/approve`
- `POST /actions/{id}/send`

### Example Product-Level Response
`GET /ops/summary`
```json
{
  "critical_risks": 3,
  "blocked_invoices": 7,
  "at_risk_materials": 2,
  "repeat_vendor_issues": 4,
  "blocked_value_inr": 670000,
  "top_incident_ids": ["inc_101", "inc_102", "inc_103"]
}
```

---

## Prediction and Scoring Strategy

OpsCore uses a layered decision stack:

1. **Deterministic checks**
   - matching,
   - tolerances,
   - duplicates,
   - deadlines,
   - policy thresholds.

2. **Heuristic scoring**
   - severity,
   - urgency,
   - vendor reliability,
   - issue impact.

3. **Optional ML scoring service**
   - anomaly detection,
   - delay likelihood,
   - risk ranking,
   - recurring pattern detection.

4. **LLM reasoning**
   - summarization,
   - evidence-backed explanations,
   - action drafting,
   - operator-facing answers.

ML is optional and isolated behind a service boundary. Go remains the system of record and policy engine.

---

## Integrations

### First-Class Integrations
- Slack for HITL, digests, and approvals. [web:882]
- Sarvam for OCR/document digitization and bounded language tasks. [web:878][web:881][web:883]
- ERPNext for operational record ingestion and sync. [web:889][web:892]
- Google Sheets for onboarding-stage import/export.

### Integration Principle
Integrations are input/output surfaces. They are not allowed to become the source of business logic. All logic must converge into OpsCore’s domain and operational state.

---

## Multi-Tenancy, RBAC, and Governance

Retain the v5.0 foundations:
- tenant-scoped data model,
- RLS-backed PostgreSQL isolation,
- API-key auth and fixed roles,
- usage metering,
- audit logs,
- bounded queues and DLQs,
- review queue as canonical approval surface. [file:893]

These are not optional in the pivot. They are what make the system a credible vertical SaaS rather than a demo.

---

## LLM Use Policy

**Allowed**
- OCR fallback and extraction repair
- incident summarization
- root-cause explanation phrasing
- action-draft generation
- question answering over approved context

**Forbidden**
- tolerance calculations
- financial validation logic
- approval policies
- state transitions
- duplicate detection
- queue semantics
- severity thresholds that can be deterministic

Every LLM call must:
- pass through provider interfaces,
- emit trace metadata,
- be schema-validated where structured output is expected,
- be reviewable in logs and tests.

---

## Test Strategy

Preserve the v5.0 pyramid and adapt scenario coverage to manufacturing ops:

- unit tests for domain rules,
- integration tests for adapters,
- agentic tests for orchestration,
- e2e tests for full incident flows,
- live-provider tests for Sarvam/Slack/ERPNext contracts,
- load tests for concurrency and queue throughput.

### New Critical Test Scenarios
- PO-GRN-invoice mismatch detection
- duplicate invoice handling
- delayed receipt causing stock risk
- repeated supplier delay aggregation
- low-confidence extraction escalation
- Slack review approval flow
- tenant isolation across incidents and risks
- bounded retry and DLQ routing

---

## Success Metrics

### Product Metrics
- % of issues auto-triaged
- median time to detect issue
- median time to resolution
- blocked value surfaced before due date
- daily active operators/reviewers
- repeat issue recurrence by vendor/material

### System Metrics
- workflow latency p50/p95
- OCR fallback rate
- LLM call count and cost
- review queue aging
- retry and DLQ rates
- queue depth by workflow

---

## Deferred Roadmap

### v6.1
- email ingestion
- vendor communication history view
- material risk board
- richer ERPNext sync

### v6.2
- optional Python ML scoring service
- anomaly detection over vendor and receipt behavior
- Google Sheets two-way sync
- billing integration

### v6.3
- advanced retrieval layer
- cross-tenant benchmarking (aggregated and privacy-safe)
- memory-driven action recommendations
- scenario simulation for “what if supplier slips by 3 days?”

---

## Product Summary

OpsCore v6.0 is not a back-office automation tool. It is an agentic operations control tower for MSME manufacturing. It turns fragmented operational signals into live state, risks, explanations, and coordinated next-best actions — while preserving deterministic policy, tenant isolation, auditability, and human control.
