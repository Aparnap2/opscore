package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// SignalAgent is the transitional agent that routes PO/GRN/Invoice documents
// through the existing document-ingestion pipeline (classify -> OCR -> map ->
// validate -> mismatch-detect -> HITL/persist). It reuses the DocumentAgent's
// OCR and storage plumbing and stays deterministic: no LLM is used for
// classification, validation, line matching, or mismatch detection.
type SignalAgent struct {
	doc *DocumentAgent
}

// NewSignalAgent wraps a DocumentAgent to provide the manufacturing signal path.
func NewSignalAgent(doc *DocumentAgent) *SignalAgent {
	return &SignalAgent{doc: doc}
}

// SignalJob is the job envelope for a manufacturing document signal.
type SignalJob struct {
	TenantID string `json:"tenant_id"`
	JobID    string `json:"job_id"`
	BlobURL  string `json:"blob_url"`
	FileName string `json:"file_name"`
	Type     string `json:"type"`
	JobType  string `json:"job_type"`
}

// SignalResult is the deterministic outcome attached to the job record.
type SignalResult struct {
	DocumentType string                 `json:"document_type"`
	Confidence   float64                `json:"confidence"`
	Validations  []string               `json:"validations"`
	Mapped       interface{}            `json:"mapped,omitempty"`
	Exceptions   []domain.ExceptionCase `json:"exceptions,omitempty"`
	NeedsHITL    bool                   `json:"needs_hitl"`
	HITLReason   string                 `json:"hitl_reason,omitempty"`
}

// ProcessSignal classifies and extracts a manufacturing document, maps it into
// the deterministic domain model, and detects mismatches against any related
// documents supplied inline with the job (transitional: full cross-record
// matching lands when PO/GRN/Invoice persistence is added in a later phase).
func (a *SignalAgent) ProcessSignal(ctx context.Context, job *SignalJob, related *SignalRelated) (*SignalResult, error) {
	// Step 1: classify (classifier returns uppercase literals; normalize to
	// the canonical domain.DocumentType lowercase values).
	rawType, _ := a.doc.classifier.Classify(ctx, job.FileName, "pdf")
	docType := normalizeDocType(rawType)

	// Step 2: OCR (delegated to DocumentAgent's provider)
	if a.doc.ocr == nil {
		return nil, fmt.Errorf("OCR provider not configured (missing SARVAM_API_KEY)")
	}
	ocrResult, err := a.doc.ocr.Extract(ctx, job.BlobURL)
	if err != nil {
		return nil, fmt.Errorf("OCR failed: %w", err)
	}

	// Step 3: translate provider OCRResult -> domain OCRPayload (no import cycle)
	payload := translateOCR(ocrResult)

	// Step 4: map into the right domain entity deterministically
	res := &SignalResult{DocumentType: docType, Confidence: ocrResult.Confidence}
	var validations []string

	switch domain.DocumentType(docType) {
	case domain.DocumentTypePurchaseOrder:
		po, mErr := domain.MapOCRToPurchaseOrder(payload, job.TenantID, job.JobID)
		if mErr != nil {
			validations = append(validations, mErr.Error())
		} else if vErr := po.Validate(); vErr != nil {
			validations = append(validations, vErr.Error())
		} else {
			res.Mapped = po
		}
	case domain.DocumentTypeGoodsReceipt:
		gr, mErr := domain.MapOCRToGoodsReceipt(payload, job.TenantID, job.JobID)
		if mErr != nil {
			validations = append(validations, mErr.Error())
		} else if vErr := gr.Validate(); vErr != nil {
			validations = append(validations, vErr.Error())
		} else {
			res.Mapped = gr
		}
	case domain.DocumentTypeInvoice:
		inv, mErr := domain.MapOCRToInvoice(payload, job.TenantID, job.JobID)
		if mErr != nil {
			validations = append(validations, mErr.Error())
		} else if vErr := inv.Validate(); vErr != nil {
			validations = append(validations, vErr.Error())
		} else {
			res.Mapped = inv
			// Duplicate detection against supplied related invoices (transitional).
			if related != nil && inv.IsDuplicate(related.Invoices) {
				validations = append(validations, fmt.Sprintf("duplicate invoice detected: %s", inv.DocumentNo))
			}
		}
	default:
		validations = append(validations, "unsupported document type for signal path: "+docType)
	}
	res.Validations = validations

	// Step 5: mismatch detection. The currently-mapped document is folded into
	// the related set so the engine always compares the uploaded doc against its
	// siblings (transitional: full cross-record matching lands with persistence).
	if related != nil || res.Mapped != nil {
		var po *domain.PurchaseOrder
		var gr *domain.GoodsReceipt
		var inv *domain.Invoice
		if related != nil {
			po, gr, inv = related.PO, related.GRN, related.Invoice
		}
		switch m := res.Mapped.(type) {
		case *domain.PurchaseOrder:
			po = m
		case *domain.GoodsReceipt:
			gr = m
		case *domain.Invoice:
			inv = m
		}
		if po != nil {
			res.Exceptions = domain.DetectMismatch(po, gr, inv, domain.DefaultMismatchTolerance())
		}
	}

	// Step 6: HITL policy — deterministic triggers only.
	res.NeedsHITL, res.HITLReason = signalHITLPolicy(res, ocrResult.Confidence)

	return res, nil
}

// SignalRelated carries inline related documents for transitional mismatch
// detection before dedicated persistence tables exist.
type SignalRelated struct {
	PO       *domain.PurchaseOrder
	GRN      *domain.GoodsReceipt
	Invoice  *domain.Invoice
	Invoices []domain.Invoice
}

// PersistSignalResult writes the deterministic manufacturing entities produced by
// ProcessSignal into the real persistence tables (PO / GRN / Invoice /
// ExceptionCase). It is the PHASE 2 bridge that moves the signal workflow off the
// transitional jobs.extracted_data bridge and onto the dedicated manufacturing
// tables.
//
// This is an orchestration helper (not domain logic): it routes a SignalResult to
// its storage sink via the DBProvider interface. Domain stays zero-I/O; the
// routing decision (which table, idempotency, tenant scoping) is deterministic and
// lives here rather than in cmd/*. The job record remains the traceability summary
// and is persisted separately by the worker.
//
// Tenant scoping is enforced defensively: any ExceptionCase whose TenantID is
// blank inherits the job's tenant, and a blank Status defaults to
// ExceptionStatusOpen, so the write is always correctly RLS-scoped and carries a
// valid lifecycle state even when the domain mapper left those fields unset.
func PersistSignalResult(ctx context.Context, db providers.DBProvider, job *SignalJob, result *SignalResult) error {
	switch entity := result.Mapped.(type) {
	case *domain.PurchaseOrder:
		if err := db.UpsertPurchaseOrder(ctx, entity); err != nil {
			return fmt.Errorf("upsert purchase order: %w", err)
		}
	case *domain.GoodsReceipt:
		if err := db.UpsertGoodsReceipt(ctx, entity); err != nil {
			return fmt.Errorf("upsert goods receipt: %w", err)
		}
	case *domain.Invoice:
		if err := db.UpsertInvoice(ctx, entity); err != nil {
			return fmt.Errorf("upsert invoice: %w", err)
		}
	}

	for i := range result.Exceptions {
		ec := &result.Exceptions[i]
		if ec.TenantID == "" {
			ec.TenantID = job.TenantID
		}
		if ec.Status == "" {
			ec.Status = domain.ExceptionStatusOpen
		}
		if err := db.UpsertExceptionCase(ctx, ec); err != nil {
			return fmt.Errorf("upsert exception case: %w", err)
		}
	}

	return nil
}

// signalHITLPolicy decides human review deterministically:
//   - OCR confidence below threshold
//   - any validation error (e.g. invalid GSTIN, missing fields)
//   - vendor mismatch or payment-affecting exception (any mismatch type)
func signalHITLPolicy(res *SignalResult, confidence float64) (bool, string) {
	if confidence < 0.85 {
		return true, fmt.Sprintf("low OCR confidence=%.2f", confidence)
	}
	if len(res.Validations) > 0 {
		return true, fmt.Sprintf("validation errors: %v", res.Validations)
	}
	for _, e := range res.Exceptions {
		if e.Severity == domain.SeverityHigh || e.Severity == domain.SeverityCritical {
			return true, fmt.Sprintf("payment-affecting exception: %s", e.Type)
		}
	}
	return false, ""
}

// translateOCR converts a provider OCR result into the domain-local OCRPayload
// to avoid an import cycle (providers imports domain).
func translateOCR(r *providers.OCRResult) *domain.OCRPayload {
	if r == nil {
		return &domain.OCRPayload{}
	}
	tables := make([]domain.OCRTable, 0, len(r.Tables))
	for _, t := range r.Tables {
		tables = append(tables, domain.OCRTable{Headers: t.Headers, Rows: t.Rows})
	}
	return &domain.OCRPayload{
		KeyValues:  r.KeyValues,
		Text:       r.Text,
		Tables:     tables,
		Confidence: r.Confidence,
	}
}

// normalizeDocType maps the classifier wrapper's uppercase literals to the
// canonical domain.DocumentType lowercase constants.
func normalizeDocType(raw string) string {
	switch strings.ToLower(raw) {
	case "invoice":
		return string(domain.DocumentTypeInvoice)
	case "purchase_order", "po":
		return string(domain.DocumentTypePurchaseOrder)
	case "goods_receipt", "grn":
		return string(domain.DocumentTypeGoodsReceipt)
	case "contract":
		return string(domain.DocumentTypeContract)
	case "gst_notice":
		return string(domain.DocumentTypeGSTNotice)
	default:
		return string(domain.DocumentTypeUnknown)
	}
}
