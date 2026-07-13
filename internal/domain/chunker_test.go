package domain

import (
	"strings"
	"testing"
)

func TestShouldChunk(t *testing.T) {
	tests := []struct {
		name      string
		docType   DocumentType
		pageCount int
		want      bool
	}{
		{"short invoice", DocumentTypeInvoice, 5, false},
		{"short purchase order", DocumentTypePurchaseOrder, 5, false},
		{"short unknown", DocumentTypeUnknown, 5, false},
		{"long invoice - over threshold", DocumentTypeInvoice, 15, true},
		{"short contract", DocumentTypeContract, 5, true},
		{"short gst notice", DocumentTypeGSTNotice, 5, true},
		{"empty page count contract", DocumentTypeContract, 0, true},
		{"exact threshold invoice", DocumentTypeInvoice, 10, false},
		{"exact threshold + 1 contract", DocumentTypeContract, 11, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldChunk(tt.docType, tt.pageCount)
			if got != tt.want {
				t.Errorf("ShouldChunk(%v, %d) = %v, want %v", tt.docType, tt.pageCount, got, tt.want)
			}
		})
	}
}

func TestChunkDocument_NoChunk(t *testing.T) {
	tests := []struct {
		name      string
		docType   DocumentType
		pageCount int
	}{
		{"short invoice", DocumentTypeInvoice, 5},
		{"short purchase order", DocumentTypePurchaseOrder, 3},
		{"short unknown", DocumentTypeUnknown, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := ChunkDocument("some text content here", "doc1", "tenant1", tt.docType, tt.pageCount)
			if chunks != nil {
				t.Errorf("ChunkDocument() for %v should return nil, got %d chunks", tt.docType, len(chunks))
			}
		})
	}
}

func TestChunkDocument_SingleChunk(t *testing.T) {
	text := "This is a short document that fits in one chunk."
	chunks := ChunkDocument(text, "doc1", "tenant1", DocumentTypeContract, 5)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].ID != "doc1_chunk_0" {
		t.Errorf("chunk ID = %v, want doc1_chunk_0", chunks[0].ID)
	}
	if chunks[0].TenantID != "tenant1" {
		t.Errorf("chunk TenantID = %v, want tenant1", chunks[0].TenantID)
	}
	if chunks[0].Content != text {
		t.Errorf("chunk Content = %v, want %v", chunks[0].Content, text)
	}
	if chunks[0].ChunkIndex != 0 {
		t.Errorf("chunk ChunkIndex = %v, want 0", chunks[0].ChunkIndex)
	}
	if chunks[0].DocumentType != string(DocumentTypeContract) {
		t.Errorf("chunk DocumentType = %v, want contract", chunks[0].DocumentType)
	}
	if chunks[0].PageNumber != 1 {
		t.Errorf("chunk PageNumber = %v, want 1", chunks[0].PageNumber)
	}
}

func TestChunkDocument_MultipleChunks(t *testing.T) {
	// Create text longer than ChunkSizeChars (2000 chars)
	text := strings.Repeat("This is a test sentence for chunking. ", 100)
	chunks := ChunkDocument(text, "doc2", "tenant2", DocumentTypeGSTNotice, 15)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Check chunk indices are sequential
	for i, chunk := range chunks {
		if chunk.ChunkIndex != i {
			t.Errorf("chunk %d: ChunkIndex = %d, want %d", i, chunk.ChunkIndex, i)
		}
		if chunk.TenantID != "tenant2" {
			t.Errorf("chunk %d: TenantID = %v, want tenant2", i, chunk.TenantID)
		}
		if chunk.ID != "" {
			expectedID := "doc2_chunk_0"
			if i > 0 {
				expectedID = "doc2_chunk_1"
			}
			if chunk.ID != expectedID {
				// Just check prefix — may have more than 2 chunks
				if !strings.HasPrefix(chunk.ID, "doc2_chunk_") {
					t.Errorf("chunk %d: ID = %v, want prefix doc2_chunk_", i, chunk.ID)
				}
			}
		}
	}
}

func TestChunkDocument_ContentBoundaries(t *testing.T) {
	// Create text exactly 3x ChunkSizeChars
	text := strings.Repeat("A", ChunkSizeChars*3)
	chunks := ChunkDocument(text, "doc3", "tenant3", DocumentTypeContract, 20)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	// Verify each chunk is the right size
	for i, chunk := range chunks {
		expectedLen := ChunkSizeChars
		if i == len(chunks)-1 {
			expectedLen = ChunkSizeChars
		}
		if len(chunk.Content) != expectedLen {
			t.Errorf("chunk %d: content length = %d, want %d", i, len(chunk.Content), expectedLen)
		}
	}
}

func TestChunkDocument_PageNumberCalculation(t *testing.T) {
	// Long enough to span multiple chunks
	text := strings.Repeat("This is test content. ", 500)
	chunks := ChunkDocument(text, "doc4", "tenant4", DocumentTypeGSTNotice, 15)
	if len(chunks) > 0 {
		// Page number should be positive for all chunks
		for i, chunk := range chunks {
			if chunk.PageNumber <= 0 {
				t.Errorf("chunk %d: PageNumber = %d, want > 0", i, chunk.PageNumber)
			}
		}
	}
}

func TestChunkDocument_LongDocumentViaPageCount(t *testing.T) {
	// Even invoices get chunked if page count exceeds threshold
	text := "Invoice text that would otherwise not be chunked."
	chunks := ChunkDocument(text, "doc5", "tenant5", DocumentTypeInvoice, 20)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for invoice over threshold, got %d", len(chunks))
	}
}

func TestChunkDocument_EmptyText(t *testing.T) {
	chunks := ChunkDocument("", "doc6", "tenant6", DocumentTypeContract, 5)
	if chunks == nil {
		t.Fatal("expected non-nil slice for chunkable document type")
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for empty text, got %d", len(chunks))
	}
}
