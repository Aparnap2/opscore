//go:build e2e

package e2e

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// E2E test setup — verify required env vars
	if os.Getenv("APP_ENV") != "test" {
		println("WARNING: e2e tests should run in Docker (make test-e2e)")
	}
	os.Exit(m.Run())
}
