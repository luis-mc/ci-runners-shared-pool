package capacity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

type fakeDemand []LabelSetLoad

func (f fakeDemand) Demand(context.Context, insights.Window) ([]LabelSetLoad, error) { return f, nil }

type fakeHeads []Headroom

func (f fakeHeads) Headroom(context.Context, insights.Window) ([]Headroom, error) { return f, nil }

type fakeRepos []RepoLoad

func (f fakeRepos) RepoLoads(context.Context, insights.Window) ([]RepoLoad, error) { return f, nil }

var win = insights.Window{From: t0, To: t0.Add(7 * 24 * time.Hour)}

func busy() LabelSetLoad {
	return LabelSetLoad{Labels: []string{"linux"}, Slots: 2, Utilisation: 0.85, JobsPerHour: 6, MeanService: 5 * time.Minute,
		CVArrivals: 1, CVService: 1, P50Wait: time.Minute, P95Wait: 6 * time.Minute}
}

func analyse(t *testing.T, d []LabelSetLoad, h []Headroom, r []RepoLoad) []insights.Finding {
	t.Helper()
	got, err := (&Advisor{Demand: fakeDemand(d), Estimator: AllenCunneen{}, Headroom: fakeHeads(h), Repos: fakeRepos(r)}).Analyse(context.Background(), win)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

const g = int64(1 << 30)

// R5.17: the finding names the machine that takes the slot within the budget and has the action.
func TestPoolSizeWithinHeadroom(t *testing.T) {
	heads := []Headroom{
		{MachineID: "m-small", MachineName: "small", Labels: []string{"linux"}, Slots: 1, FreeMemory: g, SlotMemory: 2 * g, FreeCPUMillis: 4000, SlotCPUMillis: 1000, MaxExtraSlots: 3},
		{MachineID: "m-big", MachineName: "big", Labels: []string{"linux", "x64"}, Slots: 1, FreeMemory: 8 * g, SlotMemory: 2 * g, FreeCPUMillis: 4000, SlotCPUMillis: 1000, MaxExtraSlots: 4},
		{MachineID: "m-gpu", MachineName: "gpu", Labels: []string{"gpu"}, Slots: 1, FreeMemory: 64 * g, SlotMemory: g, MaxExtraSlots: 9},
	}
	got := analyse(t, []LabelSetLoad{busy()}, heads, nil)
	if len(got) != 1 {
		t.Fatalf("findings %+v", got)
	}
	f := got[0]
	if f.Kind != insights.KindPoolSize || f.Action != insights.ActionMachineSlots || f.Subject != "linux" || f.Scope != insights.ScopePool {
		t.Fatalf("%+v", f)
	}
	if !strings.Contains(f.Fix, "big") || f.MinutesSavedWeek <= 0 {
		t.Fatalf("fix %q minutes %d", f.Fix, f.MinutesSavedWeek)
	}
	var ev map[string]any
	if err := json.Unmarshal(f.Evidence, &ev); err != nil || ev["machine_id"] != "m-big" || ev["extra_slots"] != float64(1) {
		t.Fatalf("evidence %s %v", f.Evidence, err)
	}
}

// R5.17: wait at or under 2 min, or a cut under 30 percent, gives no finding.
func TestPoolSizeThresholds(t *testing.T) {
	heads := []Headroom{{MachineID: "m", MachineName: "m", Labels: []string{"linux"}, MaxExtraSlots: 2}}
	quick := busy()
	quick.P95Wait = 2 * time.Minute
	if got := analyse(t, []LabelSetLoad{quick}, heads, nil); len(got) != 0 {
		t.Fatalf("2 min wait: %+v", got)
	}
	// Plenty of slots already: one more changes little, the cut is under 30 percent.
	flat := busy()
	flat.Slots, flat.Utilisation = 100, 0.6
	if got := analyse(t, []LabelSetLoad{flat}, heads, nil); len(got) != 0 {
		t.Fatalf("flat: %+v", got)
	}
}

// R5.17: no machine with room: the fix says so with the size to add, and there is no action.
func TestNoRoomNoAction(t *testing.T) {
	heads := []Headroom{
		{MachineID: "m1", MachineName: "m1", Labels: []string{"linux"}, FreeMemory: g, SlotMemory: 3*g + 1, FreeCPUMillis: 9000, SlotCPUMillis: 1000, MaxExtraSlots: 5},
	}
	got := analyse(t, []LabelSetLoad{busy()}, heads, nil)
	if len(got) != 1 || got[0].Action != insights.ActionNone {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(got[0].Fix, "add a machine with at least 4 GiB free") && !strings.Contains(got[0].Fix, "Add a machine with at least 4 GiB free") {
		t.Fatalf("fix %q", got[0].Fix)
	}
	// CPU over budget also leaves no room.
	h := Headroom{FreeMemory: 10 * g, SlotMemory: g, FreeCPUMillis: 500, SlotCPUMillis: 1000, MaxExtraSlots: 4}
	if SlotRoom(h) != 0 {
		t.Fatalf("room %d", SlotRoom(h))
	}
}

// A saturated label set (queue grows without bound) needs MinSlots, not one slot.
func TestSaturatedPoolNeedsMinSlots(t *testing.T) {
	l := busy()
	l.Utilisation = 1.3 // offered 2.6: 4 slots give 0.65 and 3 give 0.87, so 4
	heads := []Headroom{
		{MachineID: "a", MachineName: "a", Labels: []string{"linux"}, MaxExtraSlots: 1},
		{MachineID: "b", MachineName: "b", Labels: []string{"linux"}, MaxExtraSlots: 1},
	}
	got := analyse(t, []LabelSetLoad{l}, heads, nil)
	if len(got) != 1 || got[0].Action != insights.ActionNone || !strings.Contains(got[0].Fix, "2 more slots") {
		t.Fatalf("%+v", got)
	}
	heads[1].MaxExtraSlots = 2
	l.Utilisation = 1.1 // offered 2.2: 3 slots give 0.73, so 1 extra
	got = analyse(t, []LabelSetLoad{l}, heads, nil)
	if len(got) != 1 || got[0].Action != insights.ActionMachineSlots {
		t.Fatalf("%+v", got)
	}
}

// R5.18: raise the limit to the p95 of concurrent jobs, cap by memory headroom, advise a limit for a hog.
func TestRepoLimitRaiseAndCap(t *testing.T) {
	heads := []Headroom{{MachineID: "m", Labels: []string{"linux"}, Slots: 6, FreeMemory: 10 * g, SlotMemory: 2 * g, MaxExtraSlots: 9}} // room 5
	repo := RepoLoad{RepositoryID: 1, Repository: "o/a", ParallelLimit: 2, LimitWait: 45 * time.Minute, P95Concurrent: 4}
	got := analyse(t, nil, heads, []RepoLoad{repo})
	if len(got) != 1 || got[0].Kind != insights.KindRepoSlots || got[0].Action != insights.ActionRepoParallelLimit || !strings.Contains(got[0].Fix, "from 2 to 4") {
		t.Fatalf("%+v", got)
	}
	if got[0].MinutesSavedWeek != 45 || got[0].RepositoryID != 1 || got[0].Scope != insights.ScopeRepo {
		t.Fatalf("%+v", got[0])
	}
	// Memory room 1: limit 2 + 1 = 3 at most.
	heads[0].FreeMemory = 2 * g
	got = analyse(t, nil, heads, []RepoLoad{repo})
	if len(got) != 1 || !strings.Contains(got[0].Fix, "from 2 to 3") {
		t.Fatalf("%+v", got)
	}
	// No room at all: the finding says so and has no action.
	heads[0].FreeMemory = 0
	got = analyse(t, nil, heads, []RepoLoad{repo})
	if len(got) != 1 || got[0].Action != insights.ActionNone {
		t.Fatalf("%+v", got)
	}
	// Under 30 min of limit wait: nothing.
	repo.LimitWait = 29 * time.Minute
	if got := analyse(t, nil, heads, []RepoLoad{repo}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// One repository with more than half the slots while others waited: a limit of half.
	hog := RepoLoad{RepositoryID: 2, Repository: "o/hog", ShareOfSlots: 0.7, OthersWaited: time.Hour, P95Concurrent: 5}
	got = analyse(t, nil, heads, []RepoLoad{hog})
	if len(got) != 1 || got[0].Action != insights.ActionRepoParallelLimit || !strings.Contains(got[0].Fix, "limit of 3") {
		t.Fatalf("%+v", got)
	}
}

func TestAdvisorNameAndKinds(t *testing.T) {
	a := &Advisor{}
	if a.Name() != "capacity" || len(a.Kinds()) != 2 {
		t.Fatal("name or kinds")
	}
}
