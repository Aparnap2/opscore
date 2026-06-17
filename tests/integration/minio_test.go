package integration

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/aparna/opscore/internal/adapters/minio"
)

func TestMinIOAdapter(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9002"
	}
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if secretKey == "" {
		secretKey = "minioadmin"
	}

	ctx := context.Background()
	adapter, err := minio.NewAdapter(endpoint, accessKey, secretKey, false)
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}
	t.Log("✓ NewAdapter OK")

	// Test Upload
	content := []byte("hello minio test")
	url, err := adapter.Upload(ctx, "test-bucket", "test.txt", bytes.NewReader(content), "text/plain")
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	t.Logf("✓ Upload OK: %s", url)

	// Test Download
	reader, err := adapter.Download(ctx, "test-bucket", "test.txt")
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(reader)
	if err != nil {
		t.Fatalf("Read download failed: %v", err)
	}
	if buf.String() != "hello minio test" {
		t.Fatalf("Content mismatch: got %q", buf.String())
	}
	t.Log("✓ Download OK")

	// Test List
	items, err := adapter.List(ctx, "test-bucket", "test")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("List returned 0 items")
	}
	t.Logf("✓ List OK (%d items)", len(items))

	// Test Delete
	if err := adapter.Delete(ctx, "test-bucket", "test.txt"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	t.Log("✓ Delete OK")

	t.Log("\n✅ All MinIO integration tests passed")
}
