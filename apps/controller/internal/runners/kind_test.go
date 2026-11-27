package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

// kindMachine is a machine of the given OS with its own persistent backend and
// the container backend, whose OS and allow state are given. containerOS is
// what the machine's engine runs ("" for none).
func kindMachine(os, containerOS string, dockerAllowed bool) Machine {
	m := machine(os, "x64", true)
	m.Online = true
	m.ContainerOS = containerOS
	dos := os
	if containerOS != "" {
		dos = containerOS
	}
	m.Backends = append(m.Backends, BackendOption{Name: backend.Docker, Usable: containerOS != "" || os == "linux", Allowed: dockerAllowed,
		Kinds: []string{"ephemeral"}, OS: []string{dos}})
	return m
}

// OPS28 D93, R5.92, R5.93: which kind a machine offers for which system. A
// refusal comes at plan time with its reason.
func TestPlanKindRules(t *testing.T) {
	cases := []struct {
		name    string
		m       Machine
		os      string
		kind    string
		scope   Scope
		refused string // part of the reason; "" = accepted
		backend string
	}{
		{"linux persistent", kindMachine("linux", "", false), "linux", "persistent", Scope{Kind: ScopePool}, "", "persistent"},
		{"linux ephemeral, container backend allowed", kindMachine("linux", "", true), "linux", "ephemeral", Scope{Kind: ScopePool}, "", "docker"},
		{"linux ephemeral, container backend off", kindMachine("linux", "", false), "linux", "ephemeral", Scope{Kind: ScopePool}, "container backend allowed", ""},
		{"windows persistent", kindMachine("windows", "", false), "windows", "persistent", Scope{Kind: ScopePool}, "", "persistent"},
		{"windows ephemeral windows", kindMachine("windows", "linux", true), "windows", "ephemeral", Scope{Kind: ScopePool}, "fresh Linux containers", ""},
		{"macos persistent", kindMachine("macos", "", false), "macos", "persistent", Scope{Kind: ScopePool}, "", "persistent"},
		{"macos ephemeral", kindMachine("macos", "", true), "macos", "ephemeral", Scope{Kind: ScopePool}, "fresh Linux containers", ""},
		{"linux on windows, ephemeral", kindMachine("windows", "linux", true), "linux", "ephemeral", Scope{Kind: ScopePool}, "", "docker"},
		{"linux on windows, persistent", kindMachine("windows", "linux", true), "linux", "persistent", Scope{Kind: ScopePool}, "", "docker"},
		{"linux on windows, persistent, backend off", kindMachine("windows", "linux", false), "linux", "persistent", Scope{Kind: ScopePool}, "container backend is not allowed", ""},
		{"linux on windows, ephemeral, backend off", kindMachine("windows", "linux", false), "linux", "ephemeral", Scope{Kind: ScopePool}, "container backend is not allowed", ""},
		{"linux on windows without a Linux engine", kindMachine("windows", "windows", true), "linux", "persistent", Scope{Kind: ScopePool}, "no allowed backend", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(c.m)
			p, err := r.svc.PlanAdd(context.Background(), AddRequest{OS: c.os, Kind: c.kind, MachineID: "m1", Count: 2, Scope: c.scope}, "u")
			if c.refused != "" {
				var re *RefusalError
				if !errors.As(err, &re) || !strings.Contains(re.Reason, c.refused) {
					t.Fatalf("err = %v, want a refusal with %q", err, c.refused)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Adds) != 2 || p.Adds[0].Backend != c.backend || p.Adds[0].Kind != c.kind {
				t.Fatalf("adds %+v", p.Adds)
			}
			carries := false
			for _, l := range p.Adds[0].Labels {
				carries = carries || l == EphemeralLabel
			}
			if carries != (c.kind == "ephemeral") {
				t.Errorf("labels %v: ephemeral label = %v for kind %s", p.Adds[0].Labels, carries, c.kind)
			}
		})
	}
}

// R5.92: no persistent runner carries the ephemeral label; it would let a job
// that asks for ephemeral runners reach a runner that keeps its files.
func TestPersistentRunnerRefusesTheEphemeralLabel(t *testing.T) {
	r := newRig(kindMachine("linux", "", true))
	_, err := r.svc.PlanAdd(context.Background(), AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1,
		Scope: Scope{Kind: ScopePool}, Labels: []string{EphemeralLabel}}, "u")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "ephemeral runners only") {
		t.Fatalf("err = %v", err)
	}
}

// R5.90: keep_dependencies is on unless the request says off; it is kept in the
// record; an ephemeral runner has none.
func TestKeepDependenciesDefaultsOnAndRoundTrips(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		name string
		keep *bool
		want bool
	}{{"default", nil, true}, {"on", &yes, true}, {"off", &no, false}} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			r := newRig(kindMachine("linux", "", true))
			p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1,
				Scope: Scope{Kind: ScopePool}, KeepDependencies: c.keep}, "u")
			if err != nil {
				t.Fatal(err)
			}
			if p.Adds[0].KeepDependencies != c.want {
				t.Fatalf("spec keep = %v", p.Adds[0].KeepDependencies)
			}
			if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
				t.Fatalf("apply %+v", got)
			}
			all, _ := r.store.ListRunners(ctx, Filter{})
			if len(all) != 1 || all[0].KeepDependencies != c.want {
				t.Fatalf("record %+v", all)
			}
		})
	}
	// Ephemeral: the setting is ignored.
	r := newRig(kindMachine("linux", "", true))
	p, err := r.svc.PlanAdd(context.Background(), AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1,
		Scope: Scope{Kind: ScopePool}, KeepDependencies: &yes}, "u")
	if err != nil || p.Adds[0].KeepDependencies {
		t.Fatalf("ephemeral spec %+v %v", p.Adds, err)
	}
}

// R5.92: an ephemeral pool request is accepted; its count is the ceiling, kept
// as one slot record each. The slot records start nothing and hold no job slot.
func TestEphemeralPoolRequestSetsTheCeiling(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("linux", "", true))
	if ok, err := r.svc.EphemeralCapacity(ctx); err != nil || ok {
		t.Fatalf("capacity before: %v %v", ok, err)
	}
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 3, Scope: Scope{Kind: ScopePool}}, "u")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
		t.Fatalf("apply %+v", got)
	}
	if len(r.ag.added) != 0 {
		t.Fatalf("the ceiling started runners: %v", r.ag.added)
	}
	if n, _ := r.svc.EphemeralCeiling(ctx, "m1"); n != 3 {
		t.Fatalf("ceiling = %d", n)
	}
	if n, _ := r.svc.EphemeralOnMachine(ctx, "m1"); n != 0 {
		t.Fatalf("slot records counted as running: %d", n)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	for _, rec := range all {
		if !IsPoolRunner(rec) || rec.State != Idle || rec.Isolation != "container" || rec.GitHubRunnerID != 0 {
			t.Errorf("slot record %+v", rec)
		}
	}
	if ok, err := r.svc.EphemeralCapacity(ctx); err != nil || !ok {
		t.Fatalf("capacity after: %v %v", ok, err)
	}
	// An offline machine gives no capacity.
	m := kindMachine("linux", "", true)
	m.Online = false
	r.svc.Machines = fakeMachines{"m1": m}
	if ok, _ := r.svc.EphemeralCapacity(ctx); ok {
		t.Error("an offline machine counted as capacity")
	}
}

// R5.93: a persistent Linux runner of a Windows machine is a pool runner in a
// long-lived container that counts against the machine's slots.
func TestPersistentLinuxOnWindowsIsAContainerPoolRunner(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("windows", "linux", true))
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "u")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
		t.Fatalf("apply %+v", got)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	if len(all) != 1 {
		t.Fatalf("records %+v", all)
	}
	rec := all[0]
	if rec.Kind != "persistent" || rec.Backend != "docker" || rec.Isolation != "container" || !IsPoolRunner(rec) || !rec.KeepDependencies {
		t.Fatalf("record %+v", rec)
	}
	if n, _ := r.svc.EphemeralOnMachine(ctx, "m1"); n != 1 {
		t.Errorf("slots held = %d, want 1", n)
	}
}

func addPersistent(t *testing.T, r *rig) Record {
	t.Helper()
	ctx := context.Background()
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "u")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
		t.Fatalf("apply %+v", got)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	return all[0]
}

// R5.90: the setting changes for a persistent runner only, with an audit line;
// an ephemeral runner is refused with the reason.
func TestUpdateSettings(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("linux", "", true))
	rec := addPersistent(t, r)
	off := false
	v, err := r.svc.UpdateSettings(ctx, rec.ID, &off, "luis")
	if err != nil || v.KeepDependencies {
		t.Fatalf("update: %+v %v", v, err)
	}
	if r.svc.KeepDependencies(ctx, "m1", rec.Name) {
		t.Error("the agent still reads keep = true")
	}
	if !r.au.has("runner.keep_dependencies") {
		t.Errorf("no audit line: %v", r.au.entries)
	}
	// The ephemeral slot record has no settings.
	pe, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "u")
	_, _ = r.svc.Apply(ctx, pe.ID, nil, "u")
	all, _ := r.store.ListRunners(ctx, Filter{})
	for _, x := range all {
		if x.Kind != "ephemeral" {
			continue
		}
		var re *RefusalError
		if _, err := r.svc.UpdateSettings(ctx, x.ID, &off, "luis"); !errors.As(err, &re) || !strings.Contains(re.Reason, "ephemeral") {
			t.Errorf("ephemeral settings: %v", err)
		}
		if _, err := r.svc.RequestClearCache(ctx, x.ID, "luis"); !errors.As(err, &re) {
			t.Errorf("ephemeral clear: %v", err)
		}
	}
}

// R5.90: Clear now raises the flag the agent reads; the agent's clean report ends it.
func TestClearCacheFlagEndsWithTheCleanReport(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("linux", "", true))
	rec := addPersistent(t, r)
	if r.svc.ClearCachePending("m1", rec.Name) {
		t.Fatal("pending before it was asked")
	}
	v, err := r.svc.RequestClearCache(ctx, rec.ID, "luis")
	if err != nil || v.ID != rec.ID {
		t.Fatalf("request: %+v %v", v, err)
	}
	if !r.svc.ClearCachePending("m1", rec.Name) {
		t.Fatal("not pending")
	}
	if err := r.svc.ReportClean(ctx, "m1", rec.Name, 0, ""); err != nil {
		t.Fatal(err)
	}
	if r.svc.ClearCachePending("m1", rec.Name) {
		t.Error("still pending after the clean report")
	}
	if !r.au.has("runner.clear_cache") {
		t.Errorf("no audit line: %v", r.au.entries)
	}
}

// R5.91: a failed clean-up marks the runner unclean (shown in its reason, read
// by the job gate); a later good one clears it. Each turn is audited.
func TestReportCleanMarksAndClearsUnclean(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("linux", "", true))
	rec := addPersistent(t, r)
	if err := r.svc.ReportClean(ctx, "m1", rec.Name, 1234, "the folder _work is locked"); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Unclean(ctx, "m1", rec.Name); got != "the folder _work is locked" {
		t.Fatalf("unclean = %q", got)
	}
	v, _ := r.svc.GetView(ctx, rec.ID)
	if v.DependencyCacheBytes != 1234 || !strings.Contains(v.Reason, "the folder _work is locked") || !strings.Contains(v.Reason, "no new job") {
		t.Fatalf("view %+v reason %q", v.Record, v.Reason)
	}
	if !r.au.has("runner.unclean") {
		t.Errorf("no unclean audit line: %v", r.au.entries)
	}
	// A second failed report does not repeat the audit line.
	n := len(r.au.entries)
	if err := r.svc.ReportClean(ctx, "m1", rec.Name, 1234, "still locked"); err != nil || len(r.au.entries) != n {
		t.Fatalf("second report: %v, %d entries", err, len(r.au.entries)-n)
	}
	if err := r.svc.ReportClean(ctx, "m1", rec.Name, 99, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Unclean(ctx, "m1", rec.Name); got != "" {
		t.Fatalf("still unclean: %q", got)
	}
	if !r.au.has("runner.clean") {
		t.Errorf("no clean audit line: %v", r.au.entries)
	}
	if err := r.svc.ReportClean(ctx, "m1", "nobody", 0, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown runner: %v", err)
	}
}

// R5.91: a registration of a pool runner reports for the pool runner: its
// cache size adds up and its unclean reason shows on the pool runner.
func TestReportCleanFoldsRegistrationsIntoThePoolRunner(t *testing.T) {
	ctx := context.Background()
	r := newRig(kindMachine("linux", "", true))
	parent := addPersistent(t, r)
	for _, n := range []string{"reg-a", "reg-b"} {
		_ = r.store.InsertRunner(ctx, Record{ID: "r_" + n, Name: n, MachineID: "m1", OS: "linux", Kind: "persistent", PoolRunnerID: parent.ID, State: Idle, KeepDependencies: true})
	}
	_ = r.svc.ReportClean(ctx, "m1", "reg-a", 100, "")
	_ = r.svc.ReportClean(ctx, "m1", "reg-b", 50, "a process is left running")
	got, _ := r.store.GetRunner(ctx, parent.ID)
	if got.DependencyCacheBytes != 150 || got.UncleanReason != "a process is left running" {
		t.Fatalf("pool runner %+v", got)
	}
	// Clear now on the pool runner reaches its registrations.
	if _, err := r.svc.RequestClearCache(ctx, parent.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if !r.svc.ClearCachePending("m1", "reg-a") || !r.svc.ClearCachePending("m1", "reg-b") {
		t.Error("the registrations were not flagged")
	}
}
