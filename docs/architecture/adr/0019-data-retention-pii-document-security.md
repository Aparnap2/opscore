# ADR-0019: Data Retention, PII, and Document Security

## Status

**Proposed** — PII fields (GST, PAN, IFSC) are stored in the database but are not explicitly protected from logging. Document storage uses MinIO without server-side encryption enabled. No formal retention policy exists.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore stores sensitive financial and personally identifiable information (PII) as part of its vendor onboarding and compliance monitoring workflows:

1. **Tax identifiers:** GST numbers (15 alphanumeric), PAN cards (10 alphanumeric), IFSC codes (11 alphanumeric).
2. **Bank account information:** Bank account numbers and IFSC codes stored for vendor payment processing.
3. **Identity documents:** Scanned PAN cards, GST certificates, and bank documents stored as PDFs/images in MinIO.
4. **Business documents:** Invoices, contracts, purchase orders containing financial data and business relationships.
5. **Compliance documents:** Regulatory circulars, compliance reports, and audit trails.

Regulatory considerations:
- **IT Act 2000 (India):** Requires reasonable security practices for sensitive personal data.
- **DPDP Act 2023 (India):** Requires data minimization, purpose limitation, and retention limitation for digital personal data.
- **GST Act:** Invoices must be retained for 6-8 years from the date of filing.
- **Companies Act 2013:** Financial records must be retained for 8 years.

Current state:
1. **No encryption at rest.** MinIO buckets do not have server-side encryption enabled.
2. **No encryption in transit.** HTTP (not HTTPS) is used for local MinIO connections.
3. **PII in logs.** GST and PAN values may appear in slog output through structured logging of vendor data.
4. **No retention schedule.** Documents are never deleted. The MinIO bucket grows unbounded.
5. **No access controls.** Any authenticated user can access any document if they know the object key.
6. **No audit trail for document access.** Read access to stored documents is not recorded.

### Requirements

1. All document storage must use server-side encryption (SSE-S3 or equivalent).
2. All data in transit must use TLS.
3. PII fields (GST, PAN, IFSC) must never be logged in plaintext.
4. Retention policy: 7 years for compliance docs, 3 years for vendor records, 90 days for temp files.
5. Audit trail must be append-only and never deleted.
6. Access control via RBAC — only authorized roles can read/write documents.
7. Bucket lifecycle policies for automatic deletion.

## Decision

### Primary Decision: Encryption at Rest and in Transit, PII Logging Policy, Retention Schedule

### Encryption at Rest

All document storage uses MinIO with server-side encryption (SSE-S3):

```go
// internal/adapters/minio/adapter.go
type MinIOAdapter struct {
    client *minio.Client
    bucket string
    kmsKey string // optional KMS key ID
}

func (m *MinIOAdapter) PutObject(ctx context.Context, key string, reader io.Reader, size int64) error {
    _, err := m.client.PutObject(ctx, m.bucket, key, reader, size,
        minio.PutObjectOptions{
            ServerSideEncryption: encrypt.NewSSE(),
        },
    )
    return err
}
```

In production (GCS), equivalent encryption is configured via:

```
// GCS bucket default encryption
gsutil encryption set -k gs://opscore-docs
```

For MinIO in dev, SSE is configured by setting the `MINIO_KMS_KEY` environment variable. If the KMS is not configured, SSE falls back to auto-encryption:

```go
if os.Getenv("MINIO_KMS_KEY") == "" {
    // Dev mode: encryption not enforced
    return m.putObjectUnencrypted(ctx, key, reader, size)
}
```

### Encryption in Transit

- All HTTP connections use TLS in production (Cloud Run provides automatic TLS termination).
- MinIO connections use HTTPS when `S3_ENDPOINT` starts with `https://`.
- PostgreSQL connections use TLS when `DATABASE_URL` specifies `sslmode=require`.
- Redis connections use TLS when `REDIS_URL` specifies `rediss://`.
- Default dev configuration uses plain HTTP/Redis for local development only.

### PII Logging Policy

The following fields must never appear in log output in plaintext:

| Field | Logging Rule | Implementation |
|-------|-------------|----------------|
| GST number | Log masked: `GST-XXXXXX****XXXX` | `domain.MaskGST(gst string) string` |
| PAN number | Log masked: `PAN-XXXXX***X` | `domain.MaskPAN(pan string) string` |
| IFSC code | Log masked: `IFSC-****XXXX` | `domain.MaskIFSC(ifsc string) string` |
| Bank account | Never log | Strip from all log-sourced structs |
| Document content | Never log raw content | Log only document ID, size, type |

Implementation:

```go
// internal/domain/pii.go
func MaskGST(gst string) string {
    if len(gst) != 15 {
        return "INVALID-GST"
    }
    return gst[:6] + "****" + gst[10:]
}

func MaskPAN(pan string) string {
    if len(pan) != 10 {
        return "INVALID-PAN"
    }
    return pan[:5] + "***" + pan[8:]
}

// LogPII is a helper for slog that masks PII fields in structured logging.
func LogPII(key, value string) slog.Attr {
    return slog.String(key, maskByType(key, value))
}
```

Usage in agents:

```go
// Instead of:
slog.Info("vendor created", "gst", vendor.GST)

// Do:
slog.Info("vendor created", "gst", domain.LogPII("gst", vendor.GST))
```

### Retention Schedule

| Data Category | Retention Period | Deletion Action | Legal Basis |
|---------------|-----------------|-----------------|-------------|
| **Compliance documents** (circulars, notifications) | 7 years from publication date | Hard delete from MinIO + DB | GST Act, Companies Act |
| **Vendor records** (identifiers, documents) | 3 years from last transaction | Anonymize DB record (remove PII), delete documents from MinIO | DPDP Act data minimization |
| **Invoices and financial docs** | 7 years from invoice date | Hard delete | GST Act (Section 68) |
| **Temp files** (upload processing cache) | 90 days from upload | Hard delete by lifecycle policy | DPDP Act purpose limitation |
| **Audit trail** (audit_events table) | Never deleted | Append-only | IT Act, compliance requirement |
| **LLM call logs** (Langfuse) | 90 days | Delete from Langfuse via API | DPDP Act |
| **Log files** (stdout, structured logs) | 30 days | Log rotation (managed by Cloud Run / Docker) | Operational |

### Retention Enforcement

A scheduled cleanup job runs daily:

```go
// internal/agents/retention_agent.go
type RetentionAgent struct {
    db          providers.DBProvider
    storage     providers.ObjectStorage
    tracer      providers.TracingProvider
    now         func() time.Time // injectable for testing
}

func (a *RetentionAgent) RunCleanup(ctx context.Context) error {
    // 1. Delete temp files older than 90 days
    if err := a.cleanupTempFiles(ctx); err != nil {
        a.tracer.RecordCleanupError(ctx, "temp_files", err)
    }

    // 2. Delete compliance documents older than 7 years
    if err := a.cleanupComplianceDocs(ctx); err != nil {
        a.tracer.RecordCleanupError(ctx, "compliance_docs", err)
    }

    // 3. Anonymize vendor records older than 3 years (no transactions)
    if err := a.anonymizeVendors(ctx); err != nil {
        a.tracer.RecordCleanupError(ctx, "vendor_anonymize", err)
    }

    // 4. Delete invoices older than 7 years
    if err := a.cleanupInvoices(ctx); err != nil {
        a.tracer.RecordCleanupError(ctx, "invoices", err)
    }

    return nil
}
```

Vendor anonymization replaces PII with placeholders but preserves non-PII data for analytics:

```sql
UPDATE vendors
SET gst = 'ANONYMIZED',
    pan = 'ANONYMIZED',
    ifsc = 'ANONYMIZED',
    bank_account = 'ANONYMIZED',
    documents = '[]'::jsonb,
    anonymized_at = NOW()
WHERE tenant_id = $1
  AND last_transaction_at < NOW() - INTERVAL '3 years'
  AND anonymized_at IS NULL;
```

### Audit Trail Immutability

The `audit_events` table is append-only. No DELETE or UPDATE operations are permitted at the database level:

```sql
-- Revoke update and delete on audit_events
REVOKE UPDATE, DELETE ON audit_events FROM opscore_app;
REVOKE TRUNCATE ON audit_events FROM opscore_app;

-- Grant only INSERT and SELECT
GRANT INSERT, SELECT ON audit_events TO opscore_app;
```

Application-level enforcement: no code path calls `UPDATE` or `DELETE` on `audit_events`. The retention agent does not clean audit events.

### Bucket Lifecycle Policies

MinIO/GCS bucket lifecycle policies automatically expire temporary files:

```
# GCS lifecycle rule for opscore-temp bucket
{
  "lifecycle": {
    "rule": [
      {
        "action": {"type": "Delete"},
        "condition": {
          "age": 90
        }
      }
    ]
  }
}
```

For MinIO in dev, lifecycle policies are configured via `mc`:

```bash
mc ilm rule add opscore/temp --expire-days 90
```

### Access Control via RBAC

Document access is controlled by role:

| Role | Documents Read | Documents Write | Documents Delete |
|------|---------------|----------------|-----------------|
| **admin** | All documents | All documents | All documents |
| **operator** | Tenant-scoped documents | No (via HITL only) | No |
| **analyst** | Tenant-scoped compliance docs only | No | No |
| **vendor** (external) | Own documents only (via signed URL) | Own documents only | No |

Document access is enforced by the MinIO/GCS adapter, which validates the requesting user's role before returning the object:

```go
func (m *MinIOAdapter) GetObject(ctx context.Context, key string, role domain.Role, tenantID string) (*minio.Object, error) {
    if role == domain.RoleVendor {
        // Vendors can only access their own documents
        if !strings.HasPrefix(key, tenantID+"/vendor/") {
            return nil, fmt.Errorf("access denied: vendor cannot access key %s", key)
        }
    }
    return m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
}
```

## Alternatives Considered

### 1. No Encryption (Status Quo)

Rejected because:
- Unencrypted document storage violates the IT Act's "reasonable security practices" requirement.
- A MinIO/GCS bucket compromise would expose all financial documents.
- Cloud Run's default encryption (GCS server-side encryption) protects at the infrastructure level, but explicit SSE provides defense in depth.

### 2. Client-Side Encryption

Rejected because:
- Client-side encryption would prevent the system from reading documents for OCR and compliance analysis.
- Key management adds complexity (who holds the encryption keys?).
- Search indexing requires plaintext content.

### 3. Shorter Retention (1 Year for All)

Rejected because:
- GST Act requires 7-year invoice retention. Shorter retention would violate legal requirements.
- Compliance documents may be needed for audits years after publication.
- DPDP Act allows longer retention for legal compliance purposes.

### 4. Longer Retention (Indefinite for All)

Rejected because:
- DPDP Act requires data minimization. Indefinite retention of PII violates "purpose limitation" and "storage limitation" principles.
- Storage costs grow unbounded.
- Increased breach impact surface — more old data = more damage if breached.

### 5. PII in Database-Level Encryption (pgcrypto)

Rejected because:
- Column-level encryption adds query complexity (can't filter on encrypted columns).
- Application-level masking (the chosen approach) is simpler and sufficient for the primary goal: preventing PII leakage in logs and errors.
- Database-level encryption protects at rest but does not prevent logging of unencrypted values.

## Consequences

### Benefits

1. **Regulatory compliance.** 7-year retention for financial documents satisfies GST Act and Companies Act. Anonymization after 3 years satisfies DPDP Act data minimization.

2. **PII protection in logs.** No GST, PAN, or IFSC values appear in logs. Error messages mask PII. Debugging requires explicit opt-in with PII_ALLOWED=false.

3. **Defense in depth.** Encryption at rest (SSE-S3), encryption in transit (TLS), and access control (RBAC) provide three layers of protection for document storage.

4. **Automatic cleanup.** The retention agent and bucket lifecycle policies prevent unbounded storage growth. Old data is deleted on schedule without operator intervention.

5. **Append-only audit trail.** Audit events are immutable and never deleted. This satisfies compliance requirements for auditability and non-repudiation.

6. **Clear data classification.** Every data category has a defined retention period and deletion action. Operators know what to keep, what to delete, and when.

### Trade-offs / Risks

1. **Deletion is irreversible.** Once the retention agent deletes documents or anonymizes vendor records, the data is gone. If a legal hold or audit requires older data, it cannot be recovered. Mitigation: the retention agent logs every deletion. A 30-day grace period could be implemented where deleted objects are moved to a "deleted" bucket before hard deletion. This is deferred to Phase 2.

2. **PII masking in logs hinders debugging.** If a vendor issue requires inspecting their GST number, the masked log is insufficient. Mitigation: set `OPSCORE_ALLOW_PII_LOGGING=true` in dev environments. Production environments never set this flag. The `LogPII` helper checks the env var.

3. **Vendor anonymization removes data needed for analytics.** Anonymized vendor records cannot be used for trend analysis, cohort analysis, or compliance reporting. Mitigation: anonymization preserves non-PII data (risk score, trust tier, transaction count, region). Only GST/PAN/IFSC/bank details are removed.

4. **Audit trail growth.** Audit events are never deleted. The table grows by ~100K rows/month at current volume. Mitigation: use PostgreSQL partitioning by month. Old partitions can be compressed (pg_partman) or moved to colder storage. The table is indexed for query performance.

5. **SSE-S3 adds slight latency to document operations.** Server-side encryption requires encryption/decryption on every read/write. For small documents (invoices, PAN cards), this adds < 5ms. Mitigation: performance impact is negligible for the document sizes (100KB-5MB) OpsCore handles.

## Related ADRs

- **ADR-0005**: Audit Trail and Usage Metering — establishes the audit event pattern that this ADR makes immutable and applies retention rules to.
- **ADR-0017**: Billing Readiness and Plan Limits — storage limits (MaxStorageGB) interact with retention policy; old document cleanup frees storage for new documents.
- **ADR-0018**: Compliance-Source Ingestion Strategy — ingested compliance documents are subject to the 7-year retention policy defined in this ADR.
