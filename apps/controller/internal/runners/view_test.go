package runners

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

func TestTruthMapsGitHubAndServiceToState(t *testing.T) { // UX-19
	on := &github.Runner{Name: "a", Status: "online"}
	run := &backend.Found{Running: true}
	cases := []struct {
		name string
		rec  Record
		f    Facts
		want State
	}{
		{"idle needs online, not busy, running", Record{State: Idle}, Facts{true, on, true, run}, Idle},
		{"busy follows GitHub", Record{State: Idle}, Facts{true, &github.Runner{Status: "online", Busy: true}, true, run}, Busy},
		{"stored idle but not registered", Record{State: Idle}, Facts{true, nil, true, run}, NotRegistered},
		{"stored idle but service stopped", Record{State: Idle}, Facts{true, on, true, &backend.Found{}}, Stopped},
		{"service stopped and not registered shows the cause", Record{State: Idle}, Facts{true, nil, true, &backend.Found{}}, Stopped},
		{"service missing", Record{State: Idle}, Facts{true, on, true, nil}, Stopped},
		{"github offline", Record{State: Idle}, Facts{true, &github.Runner{Status: "offline"}, true, run}, Offline},
		{"github unreadable", Record{State: Idle}, Facts{false, nil, true, run}, Unknown},
		{"service list unknown does not block idle", Record{State: Idle}, Facts{true, on, false, nil}, Idle},
		{"failed stays failed", Record{State: Failed, Reason: "r"}, Facts{true, on, true, run}, Failed},
		{"creating stays", Record{State: Creating}, Facts{}, Creating},
	}
	for _, c := range cases {
		got, why := Truth(c.rec, c.f)
		if got != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got, why, c.want)
		}
		if got != Idle && got != Busy && why == "" {
			t.Errorf("%s: %s has no reason", c.name, got)
		}
	}
}

func TestListViewReconcilesAgainstGitHubAndAgent(t *testing.T) { // UX-19
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := r.add(t)
	r.svc.Services = func(context.Context, string) ([]backend.Found, error) {
		return []backend.Found{{ServiceName: rec.ServiceName, Name: rec.Name, Running: false}}, nil
	}
	v, err := r.svc.ListView(ctx, Filter{})
	if err != nil || len(v) != 1 || v[0].State != Stopped || v[0].MachineName != "box" || v[0].Reason == "" {
		t.Fatalf("%+v %v", v, err)
	}
	if v[0].Record.State != Idle {
		t.Fatal("the stored state is not changed")
	}
	r.svc.Services = func(context.Context, string) ([]backend.Found, error) {
		return []backend.Found{{ServiceName: rec.ServiceName, Name: rec.Name, Running: true}}, nil
	}
	if v, _ = r.svc.ListView(ctx, Filter{}); v[0].State != NotRegistered {
		t.Fatalf("GitHub lists nothing: %s", v[0].State)
	}
	r.gh.runners = []github.Runner{{ID: 5, Name: rec.Name, Status: "online"}}
	if g, err := r.svc.GetView(ctx, rec.ID); err != nil || g.State != Idle {
		t.Fatalf("%+v %v", g, err)
	}
}

// TestTruthShowsTheSessionWaitAsAWait: after a hard restart GitHub holds the
// old session for a few minutes; the runner shows the reason, never a failure
// (runners R5.95).
func TestTruthShowsTheSessionWaitAsAWait(t *testing.T) {
	until := time.Date(2026, 11, 20, 10, 7, 0, 0, time.UTC)
	r := Record{State: Idle, Kind: "persistent"}
	gh := &github.Runner{Status: "offline"}
	st, why := Truth(r, Facts{GitHubKnown: true, GitHub: gh, ServiceKnown: true,
		Service: &backend.Found{Running: true, SessionRefused: true, SessionWaitUntil: until}})
	if st == Failed || st == Stopped || why != "Waiting for GitHub to release the old session, until 10:07:00Z" {
		t.Fatalf("state %q reason %q", st, why)
	}
	st, why = Truth(r, Facts{GitHubKnown: true, GitHub: gh, ServiceKnown: true,
		Service: &backend.Found{Running: true, SessionRefused: true}})
	if st != Offline || !strings.Contains(why, "after the wait window") {
		t.Fatalf("past the window: %q %q", st, why)
	}
}

// OPS70: a container runner whose record holds a handle ref is found in the
// service list by that ref, and by its name when the list gives the runner
// under another ref; a runner the list does not name is not on the machine.
func TestContainerRunnerIsFoundByRecordOrName(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := r.add(t)
	const ref = "crsp-persistent-pool:"
	if err := r.svc.Store.SetRunnerService(ctx, rec.ID, ref+rec.Name, ""); err != nil {
		t.Fatal(err)
	}
	r.gh.runners = []github.Runner{{ID: 5, Name: rec.Name, Status: "online"}}
	list := func(f ...backend.Found) {
		r.svc.Services = func(context.Context, string) ([]backend.Found, error) { return f, nil }
	}
	cases := []struct {
		name  string
		found []backend.Found
		want  State
	}{
		{"by the ref", []backend.Found{{ServiceName: ref + rec.Name, Name: rec.Name, Running: true}}, Idle},
		{"by the name", []backend.Found{{ServiceName: ref + "other-ref", Name: rec.Name, Running: true}}, Idle},
		{"not listed", []backend.Found{{ServiceName: ref + "someone", Name: "someone", Running: true}}, Stopped},
		{"listed but not up", []backend.Found{{ServiceName: ref + rec.Name, Name: rec.Name}}, Stopped},
	}
	for _, c := range cases {
		list(c.found...)
		if g, err := r.svc.GetView(ctx, rec.ID); err != nil || g.State != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, g.State, err, c.want)
		}
	}
}
