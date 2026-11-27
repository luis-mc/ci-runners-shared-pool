package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

// R5.54: the agent reads memory= and processors= of .wslconfig into the facts.
func TestFactsReadWSLConfig(t *testing.T) {
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = []cmdRule{
		{[]string{"powershell", "-NoProfile", "-Command", agent.WSLConfigScript}, load.Result{Out: "[wsl2]\nmemory=12GB\nprocessors=6\nnestedVirtualization=true\n"}, true},
		{[]string{"powershell"}, load.Result{Out: "Microsoft Windows 11 Pro\n10.0.26100\n26100\nIntel\n8\n16\n34359738368\n"}, true},
	}
	f := agent.DetectFacts(context.Background(), &fakeSys{b}, `C:\crsp`, time.Now())
	if !f.WslConfigPresent || f.WslMemoryBytes != 12<<30 || f.WslProcessors != 6 || !f.WslNestedVirtualization {
		t.Fatalf("facts = %+v", f)
	}
}

func TestFactsWithoutWSLConfigAreEmpty(t *testing.T) {
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = []cmdRule{{[]string{"powershell"}, load.Result{Out: ""}, true}}
	f := agent.DetectFacts(context.Background(), &fakeSys{b}, `C:\crsp`, time.Now())
	if f.WslConfigPresent || f.WslMemoryBytes != 0 {
		t.Fatalf("facts = %+v", f)
	}
	if f := agent.DetectFacts(context.Background(), &fakeSys{linuxBox()}, "/", time.Now()); f.WslConfigPresent {
		t.Fatal("Linux has no .wslconfig")
	}
}
