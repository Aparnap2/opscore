package azure

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/aparna/opscore/internal/providers"
)

type BlobConfig struct {
	ConnectionString string
	AccountName      string
	AccountKey       string
	Endpoint         string // Custom endpoint for Azurite (e.g., http://127.0.0.1:10000/devstoreaccount1)
}

type BlobAdapter struct {
	client     *azblob.Client
	accountURL string
}

func NewBlobAdapter(config BlobConfig) (*BlobAdapter, error) {
	var accountURL string
	if config.Endpoint != "" {
		accountURL = config.Endpoint
	} else {
		accountURL = fmt.Sprintf("https://%s.blob.core.windows.net/", config.AccountName)
	}

	cred, err := azblob.NewSharedKeyCredential(config.AccountName, config.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("creating blob credential: %w", err)
	}

	client, err := azblob.NewClientWithSharedKeyCredential(accountURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating blob client: %w", err)
	}

	return &BlobAdapter{client: client, accountURL: accountURL}, nil
}

func (b *BlobAdapter) Upload(ctx context.Context, containerName, key string, r io.Reader, contentType string) (string, error) {
	// Try to create container - Azurite doesn't auto-create
	// Ignore errors - container might already exist
	_, _ = b.client.CreateContainer(ctx, containerName, nil)

	_, err := b.client.UploadStream(ctx, containerName, key, r, nil)
	if err != nil {
		return "", fmt.Errorf("uploading blob: %w", err)
	}

	return fmt.Sprintf("%s/%s/%s", b.accountURL, containerName, key), nil
}

func (b *BlobAdapter) Download(ctx context.Context, containerName, key string) (io.ReadCloser, error) {
	resp, err := b.client.DownloadStream(ctx, containerName, key, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading blob: %w", err)
	}

	return resp.Body, nil
}

func (b *BlobAdapter) Delete(ctx context.Context, containerName, key string) error {
	_, err := b.client.DeleteBlob(ctx, containerName, key, nil)
	return err
}

func (b *BlobAdapter) List(ctx context.Context, containerName, prefix string) ([]providers.BlobItem, error) {
	pager := b.client.NewListBlobsFlatPager(containerName, nil)
	if prefix != "" {
		pager = b.client.NewListBlobsFlatPager(containerName, &azblob.ListBlobsFlatOptions{
			Prefix: &prefix,
		})
	}

	var items []providers.BlobItem
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing blobs: %w", err)
		}
		for _, blob := range page.Segment.BlobItems {
			modified := time.Time{}
			if blob.Properties.LastModified != nil {
				modified = *blob.Properties.LastModified
			}
			items = append(items, providers.BlobItem{
				Name:     *blob.Name,
				Size:     *blob.Properties.ContentLength,
				Modified: modified,
			})
		}
	}

	return items, nil
}

var _ providers.StorageProvider = (*BlobAdapter)(nil)

// BlobAdapterMock implements StorageProvider for testing
type BlobAdapterMock struct {
	UploadFunc    func(ctx context.Context, container, key string, r io.Reader, contentType string) (string, error)
	DownloadFunc  func(ctx context.Context, container, key string) (io.ReadCloser, error)
	DeleteFunc   func(ctx context.Context, container, key string) error
	ListFunc     func(ctx context.Context, container, prefix string) ([]providers.BlobItem, error)
}

func (m *BlobAdapterMock) Upload(ctx context.Context, container, key string, r io.Reader, contentType string) (string, error) {
	if m.UploadFunc != nil {
		return m.UploadFunc(ctx, container, key, r, contentType)
	}
	return "", nil
}

func (m *BlobAdapterMock) Download(ctx context.Context, container, key string) (io.ReadCloser, error) {
	if m.DownloadFunc != nil {
		return m.DownloadFunc(ctx, container, key)
	}
	return nil, nil
}

func (m *BlobAdapterMock) Delete(ctx context.Context, container, key string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, container, key)
	}
	return nil
}

func (m *BlobAdapterMock) List(ctx context.Context, container, prefix string) ([]providers.BlobItem, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, container, prefix)
	}
	return nil, nil
}

var _ providers.StorageProvider = (*BlobAdapterMock)(nil)