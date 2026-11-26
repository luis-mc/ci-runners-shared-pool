package logs

import (
	"os/exec"
	"strings"
	"testing"
)

// networkPackages are the imports that open a connection (R6.7). The rule is
// the same one the depguard rule `logs-stay-local` in .golangci.yml holds.
var networkPackages = []string{
	"net", "net/http", "net/rpc", "net/smtp", "net/textproto", "net/mail", "crypto/tls",
	"connectrpc.com/connect", "google.golang.org/grpc",
	"github.com/luis-mc/ci-runners-shared-pool/internal/github",
	"github.com/luis-mc/ci-runners-shared-pool/internal/agentlink",
}

func goList(t *testing.T, format string, args ...string) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", append([]string{"list", "-f", format}, args...)...).Output() //nolint:gosec // fixed go tool call in a test
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func isNetwork(pkg string) bool {
	for _, n := range networkPackages {
		if pkg == n {
			return true
		}
	}
	return false
}

// TestNoNetworkImports: logs stay local (R6.7). internal/logs reaches no
// network package at all, in any depth. internal/history and internal/audit
// import none themselves; they reach the database through internal/store, which
// is outside this rule.
func TestNoNetworkImports(t *testing.T) {
	for _, pkg := range goList(t, `{{join .Deps "\n"}}`, "github.com/luis-mc/ci-runners-shared-pool/internal/logs") {
		if isNetwork(pkg) || strings.HasPrefix(pkg, "net/") {
			t.Errorf("internal/logs depends on %s", pkg)
		}
	}
	for _, p := range []string{"internal/logs", "internal/history", "internal/audit"} {
		for _, imp := range goList(t, `{{join .Imports "\n"}}`, "github.com/luis-mc/ci-runners-shared-pool/"+p) {
			if isNetwork(imp) {
				t.Errorf("%s imports %s", p, imp)
			}
		}
	}
}
