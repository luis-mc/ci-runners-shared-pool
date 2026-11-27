package doctor

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
)

func newEngine(checks ...Check) (*Engine, *MemStore) {
	reg := NewRegistry()
	for _, c := range checks {
		reg.Register(c)
	}
	st := NewMemStore()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	return &Engine{
		Registry: reg, Store: st, Now: func() time.Time { return now },
		Env: func(context.Context) (Env, error) { return Env{}, nil },
	}, st
}

func one(id string, sev Severity, fix string) Check {
	return NewCheck(id, GroupOf(id), 1, false, func(context.Context, Env) []Finding {
		return []Finding{{Subject: Subject{Kind: "machine", ID: "m1", Name: "alpha"}, Severity: sev, Title: id, Fix: Fix{Text: fix}}}
	})
}

func TestRunResultIsWorstSeverityWithCounts(t *testing.T) {
	e, _ := newEngine(one("machine.online", OK, ""), one("machine.disk", Warn, "free space"), one("runner.service", Fail, "start it"))
	r, err := e.Run(context.Background(), Scope{}, TriggerCLI, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if r.State != Done || r.Worst != Fail || r.Counts[OK] != 1 || r.Counts[Warn] != 1 || r.Counts[Fail] != 1 {
		t.Fatalf("run = %+v", r)
	}
	if r.Findings[0].Severity != Fail || r.Findings[2].Severity != OK {
		t.Fatalf("not worst first: %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Milestone != 1 || f.CheckedAt.IsZero() {
			t.Fatalf("engine did not stamp: %+v", f)
		}
	}
}

func TestEveryNotOkFindingHasAFix(t *testing.T) {
	e, _ := newEngine(one("machine.disk", Warn, ""))
	r, _ := e.Run(context.Background(), Scope{}, TriggerCLI, "")
	if r.Findings[0].Fix.Text == "" {
		t.Fatal("no fix text") // R5.4
	}
}

func TestCrashIsolated(t *testing.T) {
	boom := NewCheck("repo.boom", GroupRepository, 1, false, func(context.Context, Env) []Finding { panic("nil map") })
	e, _ := newEngine(boom, one("machine.online", OK, ""))
	r, err := e.Run(context.Background(), Scope{}, TriggerCLI, "")
	if err != nil {
		t.Fatal(err)
	}
	var crashed, other bool
	for _, f := range r.Findings {
		if f.Check == "repo.boom" && f.Severity == Fail && strings.Contains(f.Title, "crashed") && strings.Contains(f.Detail, "nil map") {
			crashed = true
		}
		if f.Check == "machine.online" && f.Severity == OK {
			other = true
		}
	}
	if !crashed || !other {
		t.Fatalf("crash=%v other=%v findings=%+v", crashed, other, r.Findings)
	}
}

func TestSlowCheckIsSkipNotOk(t *testing.T) {
	slow := NewCheck("github.slow", GroupGitHub, 1, false, func(ctx context.Context, _ Env) []Finding {
		<-ctx.Done()
		return []Finding{{Severity: OK}}
	})
	e, _ := newEngine(slow)
	e.CheckTimeout = 20 * time.Millisecond
	r, _ := e.Run(context.Background(), Scope{}, TriggerCLI, "")
	if len(r.Findings) != 1 || r.Findings[0].Severity != Skip {
		t.Fatalf("got %+v", r.Findings)
	}
	if (&Engine{}).timeout() != DefaultCheckTimeout || (&Engine{CheckTimeout: time.Hour}).timeout() != MaxCheckTimeout {
		t.Fatal("timeout default or ceiling")
	}
}

func TestScopeByCheckPrefix(t *testing.T) {
	e, _ := newEngine(one("machine.online", OK, ""), one("machine.disk", OK, ""), one("runner.service", OK, ""))
	r, _ := e.Run(context.Background(), Scope{Checks: []string{"machine"}}, TriggerWeb, "")
	if len(r.Findings) != 2 {
		t.Fatalf("got %d findings", len(r.Findings))
	}
	r, _ = e.Run(context.Background(), Scope{Checks: []string{"machine.disk"}}, TriggerWeb, "")
	if len(r.Findings) != 1 || r.Findings[0].Check != "machine.disk" {
		t.Fatalf("got %+v", r.Findings)
	}
	if (Scope{Checks: []string{"mach"}}).MatchCheck("machine.disk") {
		t.Fatal("prefix must end at a dot")
	}
}

func TestSecondRunOfSameScopeJoinsTheFirst(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	gate := NewCheck("machine.gate", GroupMachine, 1, false, func(context.Context, Env) []Finding {
		calls.Add(1)
		<-release
		return nil
	})
	e, _ := newEngine(gate)
	id1, joined1, _ := e.Start(context.Background(), Scope{}, TriggerWeb, "a")
	id2, joined2, _ := e.Start(context.Background(), Scope{}, TriggerCLI, "b")
	id3, joined3, _ := e.Start(context.Background(), Scope{Checks: []string{"machine"}}, TriggerCLI, "b")
	if id1 != id2 || joined1 || !joined2 || id3 == id1 || joined3 {
		t.Fatalf("ids %s %s %s joined %v %v %v", id1, id2, id3, joined1, joined2, joined3)
	}
	close(release)
	if _, err := e.Wait(context.Background(), id1); err != nil {
		t.Fatal(err)
	}
	// After it ends a new run starts.
	id4, joined4, _ := e.Start(context.Background(), Scope{}, TriggerCLI, "b")
	if id4 == id1 || joined4 {
		t.Fatal("old run joined after it ended")
	}
	_, _ = e.Wait(context.Background(), id4)
}

func TestSecretsAreRedactedInFindings(t *testing.T) {
	c := NewCheck("runner.leak", GroupRunner, 1, false, func(context.Context, Env) []Finding {
		return []Finding{{Severity: Warn, Title: "token ghs_abcdefghijklmnopqrstuvwx used", Detail: "Authorization: Bearer abc.def",
			Fix: Fix{Text: "x", Command: "run.sh --token ABCDEF123"}}}
	})
	e, _ := newEngine(c)
	r, _ := e.Run(context.Background(), Scope{}, TriggerCLI, "")
	f := r.Findings[0]
	all := f.Title + f.Detail + f.Fix.Text + f.Fix.Command
	for _, bad := range []string{"ghs_abcdefghijklmnopqrstuvwx", "Bearer abc.def", "ABCDEF123"} {
		if strings.Contains(all, bad) {
			t.Errorf("leaked %q in %q", bad, all)
		}
	}
}

type pub struct{ n atomic.Int32 }

func (p *pub) Publish(t events.Type, _ []byte) {
	if t == events.DoctorProgress {
		p.n.Add(1)
	}
}

func TestProgressEventsAndStoreLatest(t *testing.T) {
	e, st := newEngine(one("machine.online", OK, ""), one("machine.disk", Warn, "free space"))
	p := &pub{}
	e.Events = p
	r, _ := e.Run(context.Background(), Scope{}, TriggerSchedule, "system")
	if p.n.Load() < 3 { // start, one per check, end
		t.Fatalf("progress events: %d", p.n.Load())
	}
	fs, last, _ := st.Latest(context.Background(), "", "")
	if len(fs) != 2 || last.IsZero() {
		t.Fatalf("latest = %+v %v", fs, last)
	}
	fs, _, _ = st.Latest(context.Background(), Warn, "")
	if len(fs) != 1 || fs[0].Check != "machine.disk" {
		t.Fatalf("by severity = %+v", fs)
	}
	fs, _, _ = st.Latest(context.Background(), "", GroupRunner)
	if len(fs) != 0 {
		t.Fatalf("by group = %+v", fs)
	}
	// The API form round-trips.
	if a := r.API(); a.Worst == nil || *a.Worst != "warn" || len(a.Findings) != 2 || a.Counts["ok"] != 1 {
		t.Fatalf("api = %+v", a)
	}
}

func TestRegistryReplacesSameID(t *testing.T) {
	reg := NewRegistry()
	reg.Register(one("a.b", OK, ""))
	reg.Register(one("a.b", Warn, "x"))
	reg.Register(one("a.c", OK, ""))
	if got := reg.SortedIDs(); len(got) != 2 || got[0] != "a.b" {
		t.Fatalf("ids = %v", got)
	}
}
