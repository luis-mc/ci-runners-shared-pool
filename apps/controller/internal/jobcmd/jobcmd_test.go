package jobcmd

import (
	"strings"
	"testing"
)

func TestVMAndDBEnvAreHandledAndOthersAreNot(t *testing.T) {
	for _, n := range []string{"vm", "db-env"} {
		if !Handles(n) {
			t.Errorf("%s not handled", n)
		}
	}
	if Handles("doctor") {
		t.Error("doctor must stay with the CLI")
	}
	var b strings.Builder
	if code := RunEnv("nope", nil, Env{Stderr: &b}); code != 2 || !strings.Contains(b.String(), "unknown job command") {
		t.Errorf("unknown: %d %q", code, b.String())
	}
}
