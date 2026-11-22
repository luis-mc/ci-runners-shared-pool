package routing_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
)

// github-integration R5.101: one label per job, a generic label, stable.
func TestJobLabelShapeAndStability(t *testing.T) {
	l := routing.JobLabel("o/r", "e2e")
	if !strings.HasPrefix(l, "crsp-e2e-") || len(l) != len("crsp-e2e-")+4 {
		t.Fatalf("label %q", l)
	}
	if l != routing.JobLabel("o/r", "e2e") || l == routing.JobLabel("o/other", "e2e") {
		t.Fatal("label must be stable and depend on the repository")
	}
	for _, in := range []string{"e2e", "Build & Test (linux)", "x", "___", strings.Repeat("long-name-", 10)} {
		got := routing.JobLabel("o/r", in)
		if !repos.IsGenericLabel(got) || !routing.IsLabel(got) || len(got) > 40 {
			t.Errorf("label %q of %q is not a short generic routing label", got, in)
		}
	}
	if c := routing.ClassLabel("o/r", "large"); !strings.HasPrefix(c, "crsp-large-") || !repos.IsGenericLabel(c) {
		t.Fatalf("class label %q", c)
	}
	if routing.IsLabel("crsp-home") || routing.IsLabel("self-hosted") {
		t.Fatal("a pool name is not a routing label")
	}
}

// R5.102: one variable per job, CRSP_E2E; keys that collide stay apart.
func TestVarNames(t *testing.T) {
	got := routing.VarNames([]string{"e2e", "unit-tests", "e2e-web", "e2e_web", "e2e"})
	if got["e2e"] != "CRSP_E2E" || got["unit-tests"] != "CRSP_UNIT_TESTS" {
		t.Fatalf("names %v", got)
	}
	if got["e2e-web"] == got["e2e_web"] || !strings.HasPrefix(got["e2e_web"], "CRSP_E2E_WEB") {
		t.Fatalf("collision not split: %v", got)
	}
	again := routing.VarNames([]string{"e2e_web", "e2e", "e2e-web", "unit-tests"})
	for k, v := range got {
		if again[k] != v {
			t.Fatalf("names depend on order: %s %s %s", k, v, again[k])
		}
	}
}

// R5.103: per-job labels until the cap, then one label per size class, then drop.
func TestMakePlanCap(t *testing.T) {
	jobs := []routing.Job{{Key: "a", Class: "small", OS: "linux"}, {Key: "b", Class: "large", OS: "linux"}, {Key: "c", Class: "large", OS: "linux"}, {Key: "d", Class: "xl", OS: "linux"}}
	p := routing.MakePlan("o/r", jobs, nil, 10, 100)
	if p.ByClass || len(p.Labels) != 4 || len(p.Dropped) != 0 {
		t.Fatalf("per-job plan %+v", p)
	}
	p = routing.MakePlan("o/r", jobs, nil, 10, 13) // room 3 < 4 jobs, 3 classes fit
	if !p.ByClass || len(p.Labels) != 3 || p.Jobs[1].Label != p.Jobs[2].Label || !p.Jobs[1].Shared {
		t.Fatalf("class plan %+v", p)
	}
	p = routing.MakePlan("o/r", jobs, nil, 10, 12) // room 2: xl goes
	if len(p.Labels) != 2 || len(p.Dropped) != 1 || p.Dropped[0] != "d" {
		t.Fatalf("dropped plan %+v", p)
	}
	if p = routing.MakePlan("o/r", jobs, nil, 100, 100); len(p.Jobs) != 0 || len(p.Dropped) != 4 {
		t.Fatalf("no room %+v", p)
	}
}

// R5.104: only the runners that fit the class carry the label.
func TestCarriersBySizeClassAndRoom(t *testing.T) {
	floors := map[string]routing.Floor{"small": {CPUs: 2, MemoryGiB: 4}, "large": {CPUs: 8, MemoryGiB: 16}}
	rs := []routing.RunnerInfo{
		{ID: "b", OS: "linux", CPUs: 8, MemoryGiB: 16, Persistent: true},
		{ID: "a", OS: "linux", CPUs: 16, MemoryGiB: 32},
		{ID: "c", OS: "linux", CPUs: 4, MemoryGiB: 8},
		{ID: "w", OS: "windows", CPUs: 16, MemoryGiB: 32},
	}
	got := routing.Carriers(routing.Job{Class: "large", OS: "linux"}, rs, floors)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("large carriers %+v", got)
	}
	if got = routing.Carriers(routing.Job{Class: "small", OS: "linux"}, rs, floors); len(got) != 3 {
		t.Fatalf("small carriers %+v", got)
	}
	if got = routing.Carriers(routing.Job{Class: "xl", OS: "linux"}, rs, floors); got != nil {
		t.Fatalf("unknown class %+v", got)
	}
}

// R5.108: Throttle spaces writes by the gap on an injected clock.
func TestThrottleSpacesWrites(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	var slept []time.Duration
	th := &routing.Throttle{Gap: time.Second, Now: func() time.Time { return now },
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); now = now.Add(d); return nil }}
	for i := 0; i < 3; i++ {
		if err := th.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != time.Second {
		t.Fatalf("slept %v", slept)
	}
	th.Hold(now.Add(time.Minute))
	_ = th.Wait(context.Background())
	if slept[len(slept)-1] < time.Minute-time.Second {
		t.Fatalf("hold ignored: %v", slept)
	}
}
