package checks

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type fakeBase struct {
	ps  []BaseImageProbe
	err error
}

func (f fakeBase) BaseImages(context.Context) ([]BaseImageProbe, error) { return f.ps, f.err }

func runBase(t *testing.T, src BaseImageSource) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterBaseImage(reg, src)
	for _, c := range reg.All() {
		return c.Run(context.Background(), doctor.Env{})
	}
	t.Fatal("check not registered")
	return nil
}

// R5.74: a missing base image fails with the fix; a present one is ok; an
// unreachable machine is a skip.
func TestBaseImageCheck(t *testing.T) {
	got := runBase(t, fakeBase{ps: []BaseImageProbe{
		{MachineID: "m1", MachineName: "box", Backend: "docker", Ref: "crsp-pool:abc", Present: true},
		{MachineID: "m1", MachineName: "box", Backend: "kvm", Ref: "/cache/bases/x.qcow2", Fix: "Prepare the base disk again."},
	}})
	if len(got) != 2 || got[0].Severity != doctor.OK || got[1].Severity != doctor.Fail || got[1].Fix.Text == "" {
		t.Fatalf("findings %+v", got)
	}
	got = runBase(t, fakeBase{err: doctor.ErrUnreachable})
	if len(got) != 1 || got[0].Severity != doctor.Skip {
		t.Fatalf("findings %+v", got)
	}
}
