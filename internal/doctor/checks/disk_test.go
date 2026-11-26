package checks

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

func floorRun(t *testing.T, m doctor.Machine) doctor.Finding {
	t.Helper()
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{m}}}, "machine.floor.disk")
	if len(fs) != 1 {
		t.Fatalf("%d findings", len(fs))
	}
	return fs[0]
}

// CRSP253 (R5.37): a warning at the floor plus 20%, with the largest folders.
func TestWarnsBeforeTheFloor(t *testing.T) {
	m := healthyMachine()
	m.DiskFloorGiB, m.DiskFreeBytes = 100, i64(110*gib)
	m.LargestFolders = []string{"/var/crsp/cache 60 GiB"}
	f := floorRun(t, m)
	if f.Severity != doctor.Warn || !strings.Contains(f.Detail, "/var/crsp/cache") || !strings.Contains(f.Detail, "110.0 GiB") {
		t.Fatalf("%+v", f)
	}
	m.DiskFreeBytes = i64(121 * gib)
	if f := floorRun(t, m); f.Severity != doctor.OK {
		t.Fatalf("above floor+20%%: %+v", f)
	}
}

func TestFailsWhenTheFloorHoldsWork(t *testing.T) {
	m := healthyMachine()
	m.PauseReason, m.DiskFreeBytes, m.LargestFolders = "disk_low", i64(4*gib), []string{"C:\\crsp\\vm 30 GiB"}
	f := floorRun(t, m)
	if f.Severity != doctor.Fail || !strings.Contains(f.Detail, "C:\\crsp\\vm") {
		t.Fatalf("%+v", f)
	}
}

func TestUnreadableIsUnknown(t *testing.T) {
	m := healthyMachine()
	m.DiskFloorGiB, m.DiskFreeBytes = 100, nil
	if f := floorRun(t, m); f.Severity != doctor.Skip {
		t.Fatalf("%+v", f)
	}
}
