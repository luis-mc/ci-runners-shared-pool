package lifecycletest

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

// TestConformance runs the shared runner against the test interface. LIFECYCLE_CONFORMANCE is the
// runner's path (scripts/delivery/lifecycle_conformance.py); CI runs it as its own step too.
func TestConformance(t *testing.T) {
	runner := os.Getenv("LIFECYCLE_CONFORMANCE")
	if runner == "" {
		t.Skip("LIFECYCLE_CONFORMANCE is not set")
	}
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	out, err := exec.CommandContext(context.Background(), "python3", runner, "--url", srv.URL).CombinedOutput() //nolint:gosec // test runner path
	t.Log("\n" + string(out))
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}
}
