package slots_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// R5.1. Ported from test_machine.py Config test_defaults_roundtrip_and_bad_values and
// test_dl310_windows_runner.py test_limits_are_the_pool_defaults.
func TestDefaults(t *testing.T) {
	d := slots.Defaults()
	if d.BudgetMemoryPct != 85 || d.BudgetCPUPct != 60 || d.JobSlots != 2 || d.LightSlots != 1 || d.HeavySlots != 1 ||
		d.SlotMaxAgeMin != 120 || d.GateMaxWaitMin != 2 || d.GatePollSec != 15 || d.OnTimeout != "fail" || len(d.HeavyJobs) != 0 {
		t.Fatalf("%+v", d)
	}
	want := []string{"smoke", "prune", "prune-*", "cleanup", "remove-train", "decide", "delete-*", "runners"}
	if !reflect.DeepEqual(d.LightJobs, want) {
		t.Fatalf("light jobs %v", d.LightJobs)
	}
	if got := slots.Normalise(nil); !reflect.DeepEqual(got, d) {
		t.Fatal("nil is not the defaults")
	}
}

// R5.2 and R6.8. Ported from test_machine.py Config test_defaults_roundtrip_and_bad_values and
// test_dl267_light_slot.py test_config_validation.
func TestBadValuesKeepDefaults(t *testing.T) {
	d := slots.Defaults()
	bad := map[string]any{
		"budget_memory_pct": 0, "budget_cpu_pct": 101, "job_slots": 0, "light_slots": -1, "heavy_slots": 1.5,
		"slot_max_age_minutes": 0, "gate_max_wait_minutes": -1, "gate_poll_seconds": true, "gate_on_timeout": "explode",
		"light_jobs": "smoke",
	}
	if got := slots.Normalise(bad); !reflect.DeepEqual(got, d) {
		t.Fatalf("bad values changed the defaults: %+v", got)
	}
	good := slots.Normalise(map[string]any{"budget_memory_pct": 70, "budget_cpu_pct": 100, "job_slots": 4, "light_slots": 0,
		"heavy_slots": 0, "slot_max_age_minutes": 5, "gate_max_wait_minutes": 0, "gate_poll_seconds": 0, "gate_on_timeout": "run",
		"heavy_jobs": []any{"e2e", 3, ""}})
	if good.BudgetMemoryPct != 70 || good.BudgetCPUPct != 100 || good.JobSlots != 4 || good.LightSlots != 0 || good.HeavySlots != 0 ||
		good.SlotMaxAgeMin != 5 || good.GateMaxWaitMin != 0 || good.GatePollSec != 0 || good.OnTimeout != "run" ||
		!reflect.DeepEqual(good.HeavyJobs, []string{"e2e"}) {
		t.Fatalf("%+v", good)
	}
}

// R5.3. Ported from test_machine.py Config test_write_refuses_unknown_keys.
func TestWriteRefusesUnknownKeys(t *testing.T) {
	if err := slots.CheckKeys(map[string]any{"job_slots": 2}); err != nil {
		t.Fatal(err)
	}
	err := slots.CheckKeys(map[string]any{"job_slots": 2, "bogus": 1, "alpha": 2})
	if err == nil || !strings.Contains(err.Error(), "alpha, bogus") {
		t.Fatalf("err %v", err)
	}
}

// R5.4. Ported from test_machine.py Limits test_project_can_lower_never_raise and test_load.py Limits
// test_precedence_project_over_machine_over_default.
func TestRepoCanLowerNeverRaise(t *testing.T) {
	m := slots.Budget{MemoryPct: 85, CPUPct: 60}
	cases := []struct {
		mem, cpu int
		want     slots.Budget
	}{
		{70, 50, slots.Budget{MemoryPct: 70, CPUPct: 50}},
		{99, 99, m},
		{0, -5, m},
		{101, 1000, m},
		{85, 60, m},
		{1, 1, slots.Budget{MemoryPct: 1, CPUPct: 1}},
	}
	for _, c := range cases {
		if got := slots.Limits(m, c.mem, c.cpu); got != c.want {
			t.Errorf("Limits(%d,%d) = %+v, want %+v", c.mem, c.cpu, got, c.want)
		}
	}
}

// R5.5. Ported from test_load.py Gate test_settings_precedence and test_machine.py Gate
// test_settings_read_machine_json_over_the_old_file.
func TestSettingsPrecedence(t *testing.T) {
	machine := slots.Defaults()
	machine.GateMaxWaitMin, machine.GatePollSec, machine.OnTimeout = 2, 5, "run"
	none := func(string) string { return "" }
	if g := slots.ResolveGate(-1, -1, "", none, machine); g != (slots.GateSettings{MaxWaitMin: 2, PollSec: 5, OnTimeout: "run"}) {
		t.Fatalf("machine: %+v", g)
	}
	env := func(k string) string {
		return map[string]string{"CRSP_GATE_MAX_WAIT_MIN": "1", "CRSP_GATE_POLL_SEC": "7", "CRSP_GATE_ON_TIMEOUT": "fail"}[k]
	}
	if g := slots.ResolveGate(-1, -1, "", env, machine); g != (slots.GateSettings{MaxWaitMin: 1, PollSec: 7, OnTimeout: "fail"}) {
		t.Fatalf("env: %+v", g)
	}
	if g := slots.ResolveGate(2, 1, "run", env, machine); g != (slots.GateSettings{MaxWaitMin: 2, PollSec: 1, OnTimeout: "run"}) {
		t.Fatalf("flag: %+v", g)
	}
	// a poll below 0 counts as 0; an unknown on-timeout counts as fail
	if g := slots.ResolveGate(-1, -1, "weird", func(k string) string {
		if k == "CRSP_GATE_POLL_SEC" {
			return "-4"
		}
		return ""
	}, machine); g.PollSec != 0 || g.OnTimeout != "fail" {
		t.Fatalf("clamp: %+v", g)
	}
}

// R5.7. Ported from test_machine.py Queue test_priority_from_the_event_file.
func TestEventClass(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) func(string) string {
		p := filepath.Join(dir, "event.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return func(k string) string {
			return map[string]string{"GITHUB_EVENT_PATH": p, "GITHUB_REF": "refs/heads/main"}[k]
		}
	}
	cases := []struct {
		name, body string
		want       int
	}{
		{"ready pr", `{"pull_request":{"draft":false}}`, 0},
		{"draft pr", `{"pull_request":{"draft":true}}`, 1},
		{"default branch", `{"repository":{"default_branch":"main"}}`, 0},
		{"other branch", `{"repository":{"default_branch":"dev"}}`, 1},
		{"garbage", `not json`, 1},
	}
	for _, c := range cases {
		if got := slots.EventClass(write(c.body)); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if got := slots.EventClass(func(string) string { return "" }); got != 1 {
		t.Errorf("no event: %d", got)
	}
}

func tk(id, repo string, prio, class int, ns int64) slots.Ticket {
	return slots.Ticket{ID: id, Job: slots.Job{Repository: repo}, Priority: prio, EventClass: class, QueuedAtNs: ns}
}

func ids(o []slots.Ordered) string {
	var s []string
	for _, x := range o {
		s = append(s, x.Ticket.ID)
	}
	return strings.Join(s, ",")
}

// R5.6, R5.8. Ported from test_machine.py Queue test_order_ready_pr_and_default_branch_first_then_first_come.
func TestOrderOfService(t *testing.T) {
	ts := []slots.Ticket{tk("c", "r", 0, 1, 1), tk("a", "r", 0, 0, 3), tk("b", "r", 0, 0, 2), tk("p", "r", 5, 1, 9)}
	if got := ids(slots.Order(ts, nil, nil)); got != "p,b,a,c" {
		t.Fatalf("order %s", got)
	}
	// ties on the same nanosecond break by the random suffix fixed at enqueue
	x, y := tk("x", "r", 0, 0, 7), tk("y", "r", 0, 0, 7)
	x.Random, y.Random = 9, 2
	if got := ids(slots.Order([]slots.Ticket{x, y}, nil, nil)); got != "y,x" {
		t.Fatalf("tie %s", got)
	}
}

// R5.9: fair share, the repository with the fewest jobs running first (new).
func TestFairShare(t *testing.T) {
	ts := []slots.Ticket{tk("a1", "o/a", 0, 1, 1), tk("b1", "o/b", 0, 1, 2), tk("c1", "o/c", 0, 1, 3)}
	got := ids(slots.Order(ts, map[string]int{"o/a": 3, "o/b": 1}, nil))
	if got != "c1,b1,a1" {
		t.Fatalf("order %s", got)
	}
	// priority and event class still come before fair share
	ts = []slots.Ticket{tk("busy-ready", "o/a", 0, 0, 5), tk("idle-other", "o/b", 0, 1, 1)}
	if got := ids(slots.Order(ts, map[string]int{"o/a": 9}, nil)); got != "busy-ready,idle-other" {
		t.Fatalf("class before fair share: %s", got)
	}
}

// R5.10: a repository at its parallel limit is passed over and keeps its place (new).
func TestParallelLimitPassesOver(t *testing.T) {
	ts := []slots.Ticket{tk("a", "o/a", 0, 1, 1), tk("b", "o/b", 0, 1, 2)}
	o := slots.Order(ts, map[string]int{"o/a": 2}, map[string]int{"o/a": 2})
	// a runs 2 jobs and b none, so fair share puts b ahead; a is marked passed over
	if o[0].Ticket.ID != "b" || o[0].PassedOver || o[1].Ticket.ID != "a" || !o[1].PassedOver || o[1].Place != 2 {
		t.Fatalf("%+v", o)
	}
	// below the limit, or no limit: not passed over
	o = slots.Order(ts, map[string]int{"o/a": 1}, map[string]int{"o/a": 2})
	if o[0].PassedOver {
		t.Fatal("passed over below its limit")
	}
	o = slots.Order(ts, map[string]int{"o/a": 50}, nil)
	if o[0].PassedOver {
		t.Fatal("passed over without a limit")
	}
}

// R5.11: first in line is first among tickets that are not passed over (new).
func TestFirstInLineSkipsPassedTickets(t *testing.T) {
	ts := []slots.Ticket{tk("a", "o/a", 0, 1, 1), tk("b", "o/b", 0, 1, 2)}
	o := slots.Order(ts, map[string]int{"o/a": 1}, map[string]int{"o/a": 1})
	f, ok := slots.FirstInLine(o)
	if !ok || f.ID != "b" {
		t.Fatalf("first %+v", f)
	}
	if slots.Ahead(o, "b") != 0 {
		t.Fatalf("ahead %d", slots.Ahead(o, "b"))
	}
	if _, ok := slots.FirstInLine(slots.Order(ts[:1], map[string]int{"o/a": 1}, map[string]int{"o/a": 1})); ok {
		t.Fatal("first in line with every ticket passed over")
	}
}

// R5.24 and R5.25. Ported from test_dl327_heavy_slots.py test_exact_glob_and_repo_prefix,
// test_zero_heavy_slots_means_no_limit, test_heavy_beats_light; test_dl267_light_slot.py
// test_heavy_job_never_takes_a_light_slot.
func TestHeavyMatch(t *testing.T) {
	s := slots.Defaults()
	s.HeavyJobs = []string{"e2e", "integ-*", "o/special:slow", "other/x:*"}
	j := func(repo, job, shard string) slots.JobNames {
		return slots.JobNames{Repository: repo, Job: job, Shard: shard}
	}
	yes := []slots.JobNames{j("o/r", "e2e", ""), j("o/r", "integ-db", ""), j("o/r", "build", "e2e"), j("o/special", "slow", "")}
	no := []slots.JobNames{j("o/r", "slow", ""), j("o/r", "build", ""), j("o/r", "", "")}
	for _, x := range yes {
		if !slots.IsHeavy(x, s) {
			t.Errorf("%+v should be heavy", x)
		}
	}
	for _, x := range no {
		if slots.IsHeavy(x, s) {
			t.Errorf("%+v should not be heavy", x)
		}
	}
	s.HeavySlots = 0 // 0 means no heavy limit
	if slots.IsHeavy(j("o/r", "e2e", ""), s) {
		t.Error("heavy with zero heavy slots")
	}
	// heavy wins over light
	s = slots.Defaults()
	s.HeavyJobs = []string{"smoke"}
	if slots.IsLight(j("o/r", "smoke", ""), s) || !slots.IsHeavy(j("o/r", "smoke", ""), s) {
		t.Error("heavy must beat light")
	}
	s = slots.Defaults()
	if !slots.IsLight(j("o/r", "prune-old", ""), s) || !slots.IsLight(j("o/r", "x", "cleanup"), s) || slots.IsLight(j("o/r", "build", ""), s) {
		t.Error("light match")
	}
	s.LightSlots = 0
	if slots.IsLight(j("o/r", "smoke", ""), s) {
		t.Error("light with zero light slots")
	}
	if slots.SlotLabel(slots.KindLight, 1) != "light-1" || slots.SlotLabel(slots.KindCI, 2) != "2" {
		t.Error("slot label")
	}
}

// R5.31 (new, from machine_slots.py holder_pid): the override wins; on Windows a shell is skipped.
func TestHolderSkipsShell(t *testing.T) {
	table := map[int64]slots.ProcessInfo{10: {Parent: 5, Name: "PowerShell.exe"}, 20: {Parent: 6, Name: "Runner.Worker.exe"}, 30: {Parent: 0, Name: "cmd.exe"}}
	look := func(p int64) (slots.ProcessInfo, bool) { i, ok := table[p]; return i, ok }
	if got := slots.HolderPID("", 10, true, look); got != 5 {
		t.Errorf("shell not skipped: %d", got)
	}
	if got := slots.HolderPID("", 20, true, look); got != 20 {
		t.Errorf("worker: %d", got)
	}
	if got := slots.HolderPID("", 10, false, look); got != 10 {
		t.Errorf("not windows: %d", got)
	}
	if got := slots.HolderPID("777", 10, true, look); got != 777 {
		t.Errorf("override: %d", got)
	}
	if got := slots.HolderPID("zero", 99, true, look); got != 99 {
		t.Errorf("unknown parent: %d", got)
	}
	if got := slots.HolderPID("", 30, true, look); got != 30 {
		t.Errorf("shell without parent: %d", got)
	}
}

// R5.34: the beat limit is the larger of 4 polls and 120 s; a ticket expires at the earlier of age and beat.
func TestTicketExpiryRule(t *testing.T) {
	s := slots.Defaults()
	if slots.BeatLimit(s) != 120*time.Second {
		t.Fatal("beat limit")
	}
	s.GatePollSec = 60
	if slots.BeatLimit(s) != 240*time.Second {
		t.Fatal("beat limit with a long poll")
	}
	now := time.Now()
	if got := slots.TicketExpiry(now.Add(-time.Hour), now, s); !got.Equal(now.Add(240 * time.Second)) {
		t.Fatalf("expiry %v", got)
	}
	if got := slots.TicketExpiry(now.Add(-119*time.Minute-30*time.Second), now, s); !got.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("age expiry %v", got)
	}
}

// OPS55, OPS81 R5.75: the hold is at most 2 minutes; a longer setting is cut.
func TestHoldIsCutBelowTheJobTimeout(t *testing.T) {
	machine := slots.Defaults()
	machine.GateMaxWaitMin = 45
	none := func(string) string { return "" }
	if g := slots.ResolveGate(-1, -1, "", none, machine); g.MaxWaitMin != 2 || slots.MaxHoldMin != 2 {
		t.Fatalf("machine value not cut: %+v", g)
	}
	if g := slots.ResolveGate(99, -1, "", none, machine); g.MaxWaitMin != slots.MaxHoldMin {
		t.Fatalf("flag value not cut: %+v", g)
	}
	if d := slots.Defaults(); d.GateMaxWaitMin != slots.MaxHoldMin {
		t.Fatalf("default hold %d", d.GateMaxWaitMin)
	}
}
