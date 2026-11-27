package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/power"
)

// R5.74: a missing base image stops the machine accepting, with its own pause
// reason, and holds a new job; a running job is not cut; the image back clears it.
func TestMissingBaseImageStopsAccepting(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	if d := g.Start("running", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	g.SetBaseImageGaps(agent.MissingBaseImages([]agent.BaseImage{
		{Backend: "docker", Ref: "crsp-pool:abc", Present: true},
		{Backend: "kvm", Ref: "/cache/bases/x.qcow2", Fix: "Prepare the base disk again."},
	}))
	if ok, why := accept(g); ok || why != machines.PauseReasonBaseImage {
		t.Fatalf("accepting %v reason %q", ok, why)
	}
	if d := g.Start("new", job, "persistent"); d.Go || d.Reasons[0] != machines.PauseReasonBaseImage {
		t.Fatalf("new job: %+v", d)
	}
	if g.SlotsUsed() != 1 || len(g.BaseImageGaps()) != 1 {
		t.Fatalf("slots %d gaps %v", g.SlotsUsed(), g.BaseImageGaps())
	}
	g.SetBaseImageGaps(nil)
	if ok, _ := accept(g); !ok {
		t.Fatal("not accepting after the image came back")
	}
}

func TestVMBaseDiskProbe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bases", "d.qcow2")
	if agent.VMBaseDisk("kvm", p).Present {
		t.Fatal("missing disk reported present")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !agent.VMBaseDisk("kvm", p).Present {
		t.Fatal("existing disk reported missing")
	}
}
