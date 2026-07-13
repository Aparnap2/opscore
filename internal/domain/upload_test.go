package domain

import (
	"bytes"
	"testing"
)

func TestComputeSHA256Streaming(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHash string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"hello world", "hello world", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeSHA256Streaming(bytes.NewReader([]byte(tt.input)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantHash {
				t.Errorf("got %v, want %v", got, tt.wantHash)
			}
		})
	}
}

func TestGenerateIdempotencyKey(t *testing.T) {
	tests := []struct {
		name     string
		hash     string
		tenantID string
		want     string
	}{
		{"standard", "abc123", "tenant1", "sha256:abc123:tenant:tenant1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateIdempotencyKey(tt.hash, tt.tenantID)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
