package minio

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/aparna/opscore/internal/providers"
)

// Adapter implements providers.StorageProvider using MinIO (S3-compatible).
type Adapter struct {
	client   *minio.Client
	endpoint string
	useSSL   bool
}

// NewAdapter creates a new MinIO storage adapter.
func NewAdapter(endpoint, accessKey, secretKey string, useSSL bool) (*Adapter, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating minio client: %w", err)
	}

	return &Adapter{
		client:   client,
		endpoint: endpoint,
		useSSL:   useSSL,
	}, nil
}

// Upload uploads a file to the specified container/bucket and returns the object URL.
func (a *Adapter) Upload(ctx context.Context, container, key string, r io.Reader, contentType string) (string, error) {
	// Ensure bucket exists.
	exists, err := a.client.BucketExists(ctx, container)
	if err != nil {
		return "", fmt.Errorf("checking bucket %s: %w", container, err)
	}
	if !exists {
		if err := a.client.MakeBucket(ctx, container, minio.MakeBucketOptions{}); err != nil {
			return "", fmt.Errorf("creating bucket %s: %w", container, err)
		}
	}

	_, err = a.client.PutObject(ctx, container, key, r, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("uploading object %s/%s: %w", container, key, err)
	}

	scheme := "http"
	if a.useSSL {
		scheme = "https"
	}
	objectURL := fmt.Sprintf("%s://%s/%s/%s", scheme, a.endpoint, container, key)

	return objectURL, nil
}

// Download retrieves an object from the specified container/bucket.
func (a *Adapter) Download(ctx context.Context, container, key string) (io.ReadCloser, error) {
	obj, err := a.client.GetObject(ctx, container, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("downloading %s/%s: %w", container, key, err)
	}

	return obj, nil
}

// Delete removes an object from the specified container/bucket.
func (a *Adapter) Delete(ctx context.Context, container, key string) error {
	err := a.client.RemoveObject(ctx, container, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("deleting %s/%s: %w", container, key, err)
	}
	return nil
}

// List returns all objects under a prefix in the specified container/bucket.
func (a *Adapter) List(ctx context.Context, container, prefix string) ([]providers.BlobItem, error) {
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	var items []providers.BlobItem
	for objInfo := range a.client.ListObjects(ctx, container, opts) {
		if objInfo.Err != nil {
			return nil, fmt.Errorf("listing objects in %s: %w", container, objInfo.Err)
		}
		items = append(items, providers.BlobItem{
			Name:     objInfo.Key,
			Size:     objInfo.Size,
			Modified: objInfo.LastModified,
		})
	}

	return items, nil
}

// Compile-time interface check.
var _ providers.StorageProvider = (*Adapter)(nil)
