package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aparna/opscore/internal/domain"
)

// UpsertDocument inserts or updates a document record.
func (a *Adapter) UpsertDocument(ctx context.Context, doc *domain.Document) error {
	extractedJSON, _ := json.Marshal(doc.Extracted)
	now := time.Now()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}

	query := `INSERT INTO documents (
		id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10
	) ON CONFLICT (id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		job_id = EXCLUDED.job_id,
		file_name = EXCLUDED.file_name,
		storage_path = EXCLUDED.storage_path,
		type = EXCLUDED.type,
		status = EXCLUDED.status,
		content_hash = EXCLUDED.content_hash,
		extracted = EXCLUDED.extracted`

	_, err := a.getExec(ctx).Exec(ctx, query,
		doc.ID, doc.TenantID, doc.JobID, doc.FileName, doc.StoragePath,
		doc.Type, doc.Status, doc.ContentHash, extractedJSON, doc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by id and tenant_id.
func (a *Adapter) GetDocument(ctx context.Context, id, tenantID string) (*domain.Document, error) {
	query := `SELECT id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	FROM documents WHERE id = $1 AND tenant_id = $2`

	row := a.getExec(ctx).QueryRow(ctx, query, id, tenantID)

	doc := &domain.Document{}
	var extractedJSON []byte

	err := row.Scan(
		&doc.ID, &doc.TenantID, &doc.JobID, &doc.FileName, &doc.StoragePath,
		&doc.Type, &doc.Status, &doc.ContentHash, &extractedJSON, &doc.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting document %s: %w", id, err)
	}

	if len(extractedJSON) > 0 {
		_ = json.Unmarshal(extractedJSON, &doc.Extracted)
	}

	return doc, nil
}

// FindBySHA256 finds a document by tenant_id and content_hash for duplicate detection.
// Returns (nil, nil) when no matching document is found.
func (a *Adapter) FindBySHA256(ctx context.Context, tenantID, contentHash string) (*domain.Document, error) {
	query := `SELECT id, tenant_id, job_id, file_name, storage_path, type, status,
		content_hash, extracted, created_at
	FROM documents WHERE tenant_id = $1 AND content_hash = $2
	LIMIT 1`

	row := a.getExec(ctx).QueryRow(ctx, query, tenantID, contentHash)

	doc := &domain.Document{}
	var extractedJSON []byte

	err := row.Scan(
		&doc.ID, &doc.TenantID, &doc.JobID, &doc.FileName, &doc.StoragePath,
		&doc.Type, &doc.Status, &doc.ContentHash, &extractedJSON, &doc.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("finding document by sha256: %w", err)
	}

	if len(extractedJSON) > 0 {
		_ = json.Unmarshal(extractedJSON, &doc.Extracted)
	}

	return doc, nil
}
