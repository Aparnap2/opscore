# OpsCore Field-by-Field Validation Matrix

This document defines every field that OpsCore validates, the validation rules applied, whether an authoritative external source is consulted, and what happens when validation fails.

## Legend

| Column | Meaning |
|--------|---------|
| **Field** | The data field being validated |
| **Syntactic** | Format, pattern, checksum, length — deterministic Go |
| **Business** | Cross-field consistency — deterministic Go |
| **External** | Authoritative external API/lookup (optional) |
| **On failure** | Trust downgrade, HITL escalation, or terminal failure |
| **On unavailable** | Behavior when external source is unreachable |

---

## 1. GSTIN (Goods and Services Tax Identification Number)

| Property | Value |
|----------|-------|
| Format | `^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$` |
| Length | 15 characters |
| Source code | `internal/domain/india_validator.go` |

### Validation Rules

| Rule | Type | Logic |
|------|------|-------|
| State code prefix | Syntactic | First 2 digits must match a valid Indian state code (01-35, excluding 21 for old Jharkhand) |
| PAN embedded | Syntactic | Characters 3-14 must form a valid PAN structure |
| Check digit | Syntactic | Last character is a mod-36 checksum of preceding 14 characters |
| Registration type | Syntactic | 11th character must be 1-9 or A-Z (entity type code) |
| State vs. address | Business | GSTIN state code must match vendor registered state |
| PAN consistency | Business | PAN in GSTIN must match vendor-provided PAN |
| Legal name match | Business | Registered legal name should match document/vendor name |
| Registration status | External | Lookup via GST Developer Portal (when available) |
| Return filing status | External | Check recent return filing compliance via GST portal |

### On Failure

| Failure | Action |
|---------|--------|
| Format invalid | Mark terminal failure, return error message |
| State code invalid | Downgrade trust, flag for manual review |
| Checksum invalid | Downgrade trust, flag for manual review |
| PAN mismatch | Downgrade trust, escalate to HITL |
| External lookup unavailable | Log unavailability, downgrade trust, preserve audit reason |

### On External Source Unavailable

```
Trust score -= 20
Risk tier → HIGH if below threshold
Audit reason: "GSTIN verification — external source unavailable"
Route to HITL if confidence < 0.7
```

---

## 2. PAN (Permanent Account Number)

| Property | Value |
|----------|-------|
| Format | `^[A-Z]{5}[0-9]{4}[A-Z]{1}$` |
| Length | 10 characters |
| Source code | `internal/domain/india_validator.go` |

### Validation Rules

| Rule | Type | Logic |
|------|------|-------|
| Character pattern | Syntactic | 5 letters + 4 digits + 1 letter |
| 4th character | Syntactic | Must be P (individual), C (company), H (HUF), etc. |
| 10th character | Syntactic | Alphabetic check digit |
| PAN-GSTIN link | Business | PAN should match GSTIN chars 3-12 |
| PAN-PAN link | Business | PAN should match any other PAN on file for same entity |
| Name consistency | External | Verify holder name via income tax portal (when available) |

### On Failure

| Failure | Action |
|---------|--------|
| Format invalid | Mark terminal failure |
| PAN/GSTIN mismatch | Downgrade trust, route to HITL |
| Duplicate PAN across entities | Flag for review |

---

## 3. IFSC (Indian Financial System Code)

| Property | Value |
|----------|-------|
| Format | `^[A-Z]{4}0[A-Z0-9]{6}$` |
| Length | 11 characters |
| Source code | `internal/domain/india_validator.go` |

### Validation Rules

| Rule | Type | Logic |
|------|------|-------|
| Bank prefix | Syntactic | First 4 characters must be letters (bank code) |
| 5th character | Syntactic | Must be '0' (reserved) |
| Branch code | Syntactic | Last 6 characters alphanumeric |
| Bank-account linkage | Business | IFSC bank prefix should match bank account pattern |
| Bank name lookup | External | Verify via RBI master data (when available) |

### On Failure

| Failure | Action |
|---------|--------|
| Format invalid | Mark terminal failure |
| Unknown bank code | Downgrade trust, route to HITL |

---

## 4. Invoice Fields

| Field | Validation | Type | On Failure |
|-------|------------|------|------------|
| Invoice number | Non-empty, alphanumeric | Syntactic | Terminal failure |
| Invoice date | Valid date, not in future | Syntactic | Route to HITL |
| Due date | After invoice date | Business | Route to HITL |
| Vendor GSTIN | Valid GSTIN format + state match | Syntactic + Business | Terminal failure |
| Customer GSTIN | Valid GSTIN format (if provided) | Syntactic | Warning |
| Subtotal | Non-negative, numeric | Syntactic | Terminal failure |
| CGST rate | 0%, 2.5%, 6%, 9%, 14% (valid slab) | Business | Route to HITL |
| SGST rate | Same as CGST | Business | Route to HITL |
| IGST rate | 0%, 5%, 12%, 18%, 28% (valid slab) | Business | Route to HITL |
| CGST + SGST | Must equal IGST for inter-state | Business | Route to HITL |
| Total | Subtotal + taxes = total | Business | Route to HITL |
| Tax breakup | CGST + SGST + IGST matches total tax | Business | Route to HITL |
| Duplicate check | Same invoice number + same vendor | Business | Flag duplicate, route to HITL |
| Amount threshold | Amount > ₹1,00,000 | Business | Route to HITL |
| Currency | INR only | Business | Warning if foreign currency |
| Line items | Each must have description + amount | Syntactic | Warning |
| Quantity × Rate | Must match line item total | Business | Route to HITL |
| HSN/SAC code | 4-8 digit code if present | Syntactic | Warning if invalid |

### Threshold Tiers

| Amount Range | Trust Tier | Auto-approve? |
|--------------|------------|---------------|
| ₹0 - ₹10,000 | STRATEGIC | Yes |
| ₹0 - ₹50,000 | PREFERRED | Yes |
| ₹0 - ₹1,00,000 | STANDARD | Yes (with duplicate check) |
| Any amount | PROBATION | Always HITL |
| > ₹1,00,000 | Any | Always HITL |
| > ₹10,00,000 | Any | Always HITL + secondary approval |

---

## 5. Vendor Onboarding Fields

| Field | Validation | Type | On Failure |
|-------|------------|------|------------|
| Legal name | Non-empty, 2-200 chars | Syntactic | Terminal failure |
| Trade name | Optional | — | — |
| GSTIN | See Section 1 | Composite | See Section 1 |
| PAN | See Section 2 | Composite | See Section 2 |
| IFSC | See Section 3 | Composite | See Section 3 |
| Bank account | 9-18 digit numeric pattern | Syntactic | Route to HITL |
| Bank account + IFSC | Bank code in IFSC matches bank | Business | Route to HITL |
| Registered address | Non-empty, 10-500 chars | Syntactic | Route to HITL |
| Pincode | 6-digit Indian PIN | Syntactic | Route to HITL |
| State | Must match GSTIN state code | Business | Route to HITL |
| Email | Valid email format | Syntactic | Warning |
| Phone | 10-digit Indian mobile | Syntactic | Warning |
| MSME registration | Optional Udyam format | Syntactic | Warning if invalid |
| Director names | For company-type vendors | Business | Route to HITL if missing |
| Duplicate vendor | Same PAN, GSTIN, or name+address | Business | Flag, route to HITL |
| Risk score | See risk scorer | Composite | See risk tier mapping |

### Risk Score → Tier Mapping

| Score Range | Risk Tier | Default Trust | Auto-approve? |
|-------------|-----------|---------------|---------------|
| 0-20 | HIGH | PROBATION | No |
| 21-40 | HIGH | PROBATION | No |
| 41-60 | MEDIUM | STANDARD | Yes (with conditions) |
| 61-80 | LOW | PREFERRED | Yes |
| 81-100 | LOW | STRATEGIC | Yes |

### Trust Battery State Machine

See `internal/domain/trust_battery.go` for the full state machine.

| Transition | Condition | Effect |
|------------|-----------|--------|
| PROBATION → STANDARD | 30 days + 3 successful transactions | Upgrade |
| STANDARD → PREFERRED | 60 days + 3 transactions/month | Upgrade |
| PREFERRED → STRATEGIC | 90 days + 10 transactions/month | Upgrade |
| Any → BLOCKED | 3 consecutive failures or dispute filed | Downgrade (immediate) |
| PREFERRED → STANDARD | 180 days inactive | Downgrade |
| STRATEGIC → PREFERRED | 180 days inactive | Downgrade |
| Any tier → PROBATION | 3 consecutive errors | Downgrade |

---

## 6. Compliance Monitoring Fields

| Field | Validation | Type | On Failure |
|-------|------------|------|------------|
| Source URL | Valid URL, reachable | Syntactic | Log, skip source |
| Source type | SEBI / RBI / GST / MCA | Syntactic | Log, skip record |
| Publish date | Valid date, not in future | Syntactic | Warning |
| Circular number | Non-empty | Syntactic | Warning |
| Title | Non-empty | Syntactic | Log, skip record |
| Content | Non-empty after extraction | Syntactic | Log, skip chunk |
| Severity | CRITICAL / HIGH / MEDIUM / LOW | Business | Default to LOW |
| Gap analysis | LLM-reasoned against policy | LLM | Route to HITL if uncertain |
| Duplicate | Same circular number + source | Business | Skip |
| Feed shape | RSS/XML schema valid | External | Log drift, alert operator |
| Timestamp anomaly | Publish date > 30 days old | Business | Low priority |

### Compliance Severity Thresholds

| Severity | Meaning | Action |
|----------|---------|--------|
| CRITICAL | Direct legal/regulatory impact | Immediate Slack alert |
| HIGH | Significant compliance gap | Slack alert within 1 hour |
| MEDIUM | Procedural gap | Log, include in daily digest |
| LOW | Informational | Log only |
| UNKNOWN | LLM couldn't determine | Route to HITL |

---

## 7. Document Ingestion Fields

| Field | Validation | Type | On Failure |
|-------|------------|------|------------|
| Filename | Non-empty, safe characters | Syntactic | Reject upload |
| File extension | .pdf, .jpg, .jpeg, .png | Syntactic | Reject upload |
| File size | < 10MB | Syntactic | Reject upload |
| SHA256 | Computed server-side | Syntactic | Used for dedup |
| Content hash | Unique per tenant | Business | Flag duplicate |
| MIME type | application/pdf, image/* | Syntactic | Reject if unsupported |
| OCR confidence | > 0.85 threshold | Business | Route to HITL |
| Extracted fields | All required fields present | Business | Route to HITL |
| Document type | INVOICE / CONTRACT / GST_NOTICE / PO / OTHER | Syntactic | Default to OTHER |
| Page count | > 0 | Syntactic | Reject (empty doc) |
| Encrypted PDF | Detection via header | Syntactic | Reject |
| Malformed PDF | Parsing failure | Syntactic | Reject |

### Document Type → Chunking Behavior

| Document Type | Chunk? | Chunk Size | Notes |
|---------------|--------|------------|-------|
| INVOICE | No | N/A | Short structured data |
| CONTRACT | Yes | 500 tokens | Legal clauses need analysis |
| GST_NOTICE | Yes | 500 tokens | Regulatory content |
| PO | No | N/A | Short structured data |
| OTHER | Yes | 500 tokens | Conservative default |

---

## 8. Bank Account Fields

| Field | Validation | Type | On Failure |
|-------|------------|------|------------|
| Account number | 9-18 digits | Syntactic | Route to HITL |
| IFSC match | Bank prefix matches bank | Business | Route to HITL |
| Name match | Account holder ≈ vendor name | External | Route to HITL (via Penny Drop API) |
| KYC status | Verified via bank lookup | External | Route to HITL |

---

## 9. Date/Time Fields

| Field | Validation | On Failure |
|-------|------------|------------|
| Created at | Auto-set server-side | N/A |
| Updated at | Auto-set on changes | N/A |
| Invoice date | ≤ today, ≥ 1 year ago | Warning if future, reject if > 1 year future |
| Due date | ≥ invoice date | Route to HITL |
| Vendor registration date | Not in future | Route to HITL |
| GST registration date | ≤ today | Route to HITL |
| Trust battery dates | Auto-managed | N/A |
| Compliance publish date | ≤ today, ≥ reasonable range | Warning |

---

## 10. Audit Trail Fields

| Field | Required | Purpose |
|-------|----------|---------|
| Actor | Yes | Who performed the action (system, user ID, Slack user) |
| Action | Yes | What happened (UPLOAD, PROCESS, APPROVE, REJECT, FAIL) |
| Target type | Yes | Entity type (job, vendor, document, compliance) |
| Target ID | Yes | Entity ID |
| Old state | No | Previous state before the action |
| New state | Yes | Resulting state after the action |
| Timestamp | Yes | When the action occurred |
| Trace ID | No | Correlates across services |
| Correlation ID | No | Links related events |

---

## External Source Availability Policy

```go
type ExternalSourceStatus int

const (
    SourceAvailable   ExternalSourceStatus = iota // API responded successfully
    SourceUnreachable                              // Network error or timeout
    SourceAuthFailed                               // 401/403 — credentials expired
    SourceMalformed                                // Response didn't match schema
    SourceStaleData                                // Data older than freshness threshold
    SourceNotConfigured                            // API key/credentials not set up
)
```

### Trust Impact by Source Status

| Status | Trust Score Adjustment | Audit Reason |
|--------|----------------------|--------------|
| Available | +0 | "Verified via [source]" |
| Unreachable | -20 | "[Source] unreachable at [timestamp]" |
| AuthFailed | -30 | "[Source] auth failed — check credentials" |
| Malformed | -25 | "[Source] returned unparseable response" |
| StaleData | -10 | "[Source] data stale since [date]" |
| NotConfigured | -5 | "[Source] not configured — skipping" |

---

## Validation Pipeline Flow

```
Input Data
    │
    ▼
┌─────────────────────┐
│ Syntactic Validation │ ← Pure Go, no I/O
│ (format, checksum)   │
└─────────┬───────────┘
          │ fail → TERMINAL FAILURE
          ▼ pass
┌─────────────────────┐
│ Business Validation  │ ← Pure Go, no I/O
│ (cross-field, math)  │
└─────────┬───────────┘
          │ fail → HITL or WARNING
          ▼ pass
┌─────────────────────┐
│ External Lookup      │ ← Optional, authoritative
│ (GST, MCA, SEBI...) │
└─────────┬───────────┘
          │ unavailable → TRUST DOWNGRADE
          │ fail → HITL
          ▼ pass
┌─────────────────────┐
│ Risk Scoring         │ ← Deterministic formula
│ Trust Tier Decision  │ ← State machine
└─────────┬───────────┘
          │
          ▼
    Final State: COMPLETED / HITL / FAILED
```

---

## Implementation Status

| Area | Syntactic | Business | External | Status |
|------|-----------|----------|----------|--------|
| GSTIN | ✅ `india_validator.go` | ✅ `vendor_agent.go` | 📝 Not yet | Production |
| PAN | ✅ `india_validator.go` | ✅ `vendor_agent.go` | 📝 Not yet | Production |
| IFSC | ✅ `india_validator.go` | ✅ `vendor_agent.go` | 📝 Not yet | Production |
| Invoice | ✅ (partial) | 📝 Not yet | 📝 Not yet | Partial |
| Vendor onboarding | ✅ (partial) | ✅ (partial) | 📝 Not yet | Partial |
| Compliance | 📝 Not yet | 📝 Not yet | ✅ RSS feeds | Partial |
| Trust Battery | N/A | ✅ `trust_battery.go` | N/A | Production |
| Document ingestion | ✅ `main.go` | ✅ `document_agent.go` | ✅ Sarvam OCR | Production |
| Bank account | 📝 Not yet | 📝 Not yet | 📝 Not yet | Not started |
| Audit trail | ✅ `postgres/adapter.go` | N/A | N/A | Production |

**Legend:**
- ✅ = Implemented and tested
- 📝 = Planned / partial implementation
- Blank = Not yet scoped
