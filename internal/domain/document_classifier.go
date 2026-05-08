package domain

import (
	"strings"
)

// DocumentType represents the classified type of a document
type DocumentType string

const (
	DocumentTypeInvoice       DocumentType = "invoice"
	DocumentTypeContract      DocumentType = "contract"
	DocumentTypePurchaseOrder DocumentType = "purchase_order"
	DocumentTypeGSTNotice     DocumentType = "gst_notice"
	DocumentTypeUnknown       DocumentType = "unknown"
)

// ClassificationResult holds the document classification result
type ClassificationResult struct {
	Scores     map[string]int `json:"scores"`
	Type       DocumentType   `json:"type"`
	Confidence float64        `json:"confidence"`
}

// Keyword sets for document classification (PRD v3.0 spec)
var (
	invoiceKeywords = []string{
		"invoice",
		"bill",
		"tax invoice",
		"gst invoice",
		"proforma",
	}

	contractKeywords = []string{
		"agreement",
		"contract",
		"mou",
		"memorandum",
		"terms and conditions",
	}

	gstNoticeKeywords = []string{
		"notice",
		"gst department",
		"demand",
		"scrutiny",
		"show cause",
	}

	purchaseOrderKeywords = []string{
		"purchase order",
		"p.o.",
		"order no",
		"procurement order",
	}
)

// Classify classifies a document based on its text content
func Classify(rawText string) *ClassificationResult {
	// Limit text to first 500 chars for efficiency
	text := strings.ToLower(rawText)
	if len(text) > 500 {
		text = text[:500]
	}

	// Calculate scores for each document type
	scores := map[string]int{
		"invoice":        countKeywordMatches(text, invoiceKeywords),
		"contract":       countKeywordMatches(text, contractKeywords),
		"gst_notice":     countKeywordMatches(text, gstNoticeKeywords),
		"purchase_order": countKeywordMatches(text, purchaseOrderKeywords),
	}

	// Determine document type based on highest score
	docType := DocumentTypeUnknown // default
	confidence := 0.0

	// Priority order: invoice > po > gst_notice > contract
	if scores["invoice"] >= 2 {
		docType = DocumentTypeInvoice
		confidence = float64(scores["invoice"]) / float64(len(invoiceKeywords))
	} else if scores["purchase_order"] >= 2 {
		docType = DocumentTypePurchaseOrder
		confidence = float64(scores["purchase_order"]) / float64(len(purchaseOrderKeywords))
	} else if scores["gst_notice"] >= 2 {
		docType = DocumentTypeGSTNotice
		confidence = float64(scores["gst_notice"]) / float64(len(gstNoticeKeywords))
	} else if scores["contract"] >= 2 {
		docType = DocumentTypeContract
		confidence = float64(scores["contract"]) / float64(len(contractKeywords))
	}

	// Normalize confidence to percentage (0-100)
	confidence = confidence * 100
	if confidence > 100 {
		confidence = 100
	}

	return &ClassificationResult{
		Type:       docType,
		Confidence: confidence,
		Scores:     scores,
	}
}

// countKeywordMatches counts how many keywords are found in the text
func countKeywordMatches(text string, keywords []string) int {
	count := 0
	textLower := text
	for _, kw := range keywords {
		if strings.Contains(textLower, kw) {
			count++
		}
	}
	return count
}

// ValidateGSTNumber validates a GST number format
func ValidateGSTNumber(gst string) bool {
	if gst == "" {
		return false
	}
	return ValidateGST(gst)
}

// ValidatePANNumber validates a PAN number format
func ValidatePANNumber(pan string) bool {
	if pan == "" {
		return false
	}
	return ValidatePAN(pan)
}

// ValidateIFSCCode validates an IFSC code format
func ValidateIFSCCode(ifsc string) bool {
	if ifsc == "" {
		return false
	}
	return ValidateIFSC(ifsc)
}
