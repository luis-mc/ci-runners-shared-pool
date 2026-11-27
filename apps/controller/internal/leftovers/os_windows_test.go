//go:build windows

package leftovers

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

// OPS92: `powershell -Command -` needs a blank line after the script.
func TestPowerShellStdinEndsWithBlankLine(t *testing.T) {
	fake := &sysfake.Commands{}
	h := &hostOS{cmd: fake}
	if _, err := h.ps(context.Background(), scriptAccounts, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls[0].Stdin; !strings.HasSuffix(got, "\n\n") || strings.HasSuffix(got, "\n\n\n") {
		t.Errorf("stdin tail %q", got[max(0, len(got)-10):])
	}
}
