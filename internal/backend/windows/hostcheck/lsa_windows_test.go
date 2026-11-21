//go:build windows

package hostcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	backend "github.com/luis-mc/ci-runners-shared-pool/internal/backend/windows"
)

// On a real Windows host the C# of the LSA and logon scripts compiles and
// reads the rights of a built-in account; nothing is changed. Integration
// tier: PowerShell compiling C# takes longer than the 10 s unit limit.
func TestLSASourceCompilesAndReads(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("needs an elevated shell: opening the LSA policy needs admin")
	}
	// A file, not stdin: PowerShell reading a multi-line script from stdin
	// can end without running it and exit 0 with no output on a runner.
	file := filepath.Join(t.TempDir(), "lsa.ps1")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf"+backend.HostCheckScript()), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("err = %v, output = %s", err, out)
	}
}
