package agent_test

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/power"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

const gibB = int64(1) << 30

func floorOf(free *int64) *runnercache.Floor {
	return &runnercache.Floor{Bytes: 10 * gibB, Path: "x", Read: func(string) (runnercache.Disk, error) {
		return runnercache.Disk{Free: *free, Total: 500 * gibB}, nil
	}}
}

// runners R5.83, OPS86: settings from the controller change the floor live, with
// no restart; a held machine is released by a lower floor, and a higher floor holds.
func TestConsoleFloorAppliesLive(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	free := 100 * gibB
	g.SetDiskFloor(floorOf(&free))
	set, _ := g.Settings()
	if ok, _ := accept(g); !ok {
		t.Fatal("held with 100 GiB free and a floor of 10")
	}
	set.DiskFloorGiB = 150
	g.SetSettings(set, machines.StateActive)
	if gib, off := g.DiskFloor(); gib != 150 || off {
		t.Fatalf("floor in force: %d off %v", gib, off)
	}
	if ok, why := accept(g); ok || why != machines.PauseReasonDiskLow {
		t.Fatalf("a floor of 150 with 100 free: accepting %v reason %q", ok, why)
	}
	set.DiskFloorGiB = 50
	g.SetSettings(set, machines.StateActive)
	if ok, _ := accept(g); !ok {
		t.Fatal("a lower floor did not release the machine")
	}
}

// OPS86: a floor that is off in config stays off; the console value is ignored.
func TestConsoleFloorCannotTurnOnAFloorThatIsOffInConfig(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	set, _ := g.Settings()
	set.DiskFloorGiB = 150
	g.SetSettings(set, machines.StateActive)
	if gib, off := g.DiskFloor(); gib != 0 || !off {
		t.Fatalf("floor in force: %d off %v", gib, off)
	}
}

// OPS86: the value survives the wire in both directions, and 0 means not set.
func TestFloorOverTheWire(t *testing.T) {
	st := machines.DefaultSettings()
	st.DiskFloorGiB = 150
	if got := agent.SettingsFromProto(machines.ProtoSettings(st, machines.StateActive)); got.DiskFloorGiB != 150 {
		t.Fatalf("round trip: %d", got.DiskFloorGiB)
	}
	if got := agent.SettingsFromProto(&agentv1.MachineSettings{}); got.DiskFloorGiB != 0 {
		t.Fatalf("an older controller: %d", got.DiskFloorGiB)
	}
}
