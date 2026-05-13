package domain

import "fmt"

const (
	ChunkSizeTokens               = 500  // approximate tokens per chunk
	ChunkSizeChars                = 2000 // characters (approximates tokens)
	PageThresholdForChunking      = 10   // pages > 10 get chunked
)

// ShouldChunk returns true if document should be chunked
// Per spec: ONLY chunk when page_count > 10 OR document_type IN (contract, gst_notice, compliance)
// Never chunk invoices or POs
func ShouldChunk(docType DocumentType, pageCount int) bool {
	// Only chunk long/complex documents
	if pageCount > PageThresholdForChunking {
		return true
	}
	// Always chunk these types regardless of length
	switch docType {
	case DocumentTypeContract, DocumentTypeGSTNotice:
		return true
	default:
		return false
	}
}

// ChunkDocument splits text into chunks
func ChunkDocument(text string, docID, tenantID string, docType DocumentType, pageCount int) []ComplianceChunk {
	if !ShouldChunk(docType, pageCount) {
		return nil
	}

	chunks := []ComplianceChunk{}
	textLen := len(text)
	chunkIndex := 0

	for i := 0; i < textLen; i += ChunkSizeChars {
		end := i + ChunkSizeChars
		if end > textLen {
			end = textLen
		}

		chunk := ComplianceChunk{
			ID:           fmt.Sprintf("%s_chunk_%d", docID, chunkIndex),
			TenantID:     tenantID,
			Content:      text[i:end],
			ChunkIndex:   chunkIndex,
			DocumentType: string(docType),
			PageNumber:   (chunkIndex * ChunkSizeChars / 2000) + 1, // approximate
		}
		chunks = append(chunks, chunk)
		chunkIndex++
	}

	return chunks
}