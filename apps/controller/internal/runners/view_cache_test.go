package runners

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// countingGitHub counts the runner lists asked of GitHub, and how many run at once.
type countingGitHub struct {
	*fakeGitHub
	lists, now, peak atomic.Int32
	// meet, when set, holds each call until two are inside at once: calls that run one after
	// another never get past it, so the test fails by timeout instead of by a clock.
	meet chan struct{}
	once sync.Once
}

func (g *countingGitHub) ListRunners(ctx context.Context, id int64, s github.RunnerScope) ([]github.Runner, error) {
	g.lists.Add(1)
	n := g.now.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if g.meet != nil {
		if n >= 2 {
			g.once.Do(func() { close(g.meet) })
		}
		<-g.meet
	}
	g.now.Add(-1)
	return g.fakeGitHub.ListRunners(ctx, id, s)
}

// CRSP389: the runner list never waits on GitHub or an agent.
func TestListViewNeverCallsGitHubOrTheAgentOnTheRequestPath(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := r.add(t)
	gh := &countingGitHub{fakeGitHub: r.gh}
	var scans atomic.Int32
	r.svc.GitHub = gh
	r.svc.Services = func(context.Context, string) ([]backend.Found, error) {
		scans.Add(1)
		return []backend.Found{{ServiceName: rec.ServiceName, Name: rec.Name, Running: true}}, nil
	}
	var kicked []func()
	r.svc.Background = func(f func()) { kicked = append(kicked, f) } // the refresh is held back
	r.gh.runners = []github.Runner{{ID: 5, Name: rec.Name, Status: "online"}}

	// Cold cache: the list answers from the stored record, at once, with no outside call.
	v, err := r.svc.ListView(ctx, Filter{})
	if err != nil || len(v) != 1 || v[0].State != Idle || v[0].Reason != "" {
		t.Fatalf("a cold list shows the stored state: %+v %v", v, err)
	}
	if gh.lists.Load() != 0 || scans.Load() != 0 {
		t.Fatalf("the request called GitHub %d times and the agent %d times", gh.lists.Load(), scans.Load())
	}
	if len(kicked) != 1 {
		t.Fatalf("the request starts one background refresh: %d", len(kicked))
	}

	// The refresh runs in the background; then the list shows the real state, still with no call of its own.
	kicked[0]()
	if gh.lists.Load() != 1 || scans.Load() != 1 {
		t.Fatalf("the refresh reads GitHub and the agent once: %d %d", gh.lists.Load(), scans.Load())
	}
	for i := 0; i < 3; i++ {
		if v, _ = r.svc.ListView(ctx, Filter{}); v[0].State != Idle {
			t.Fatalf("%+v", v)
		}
	}
	if gh.lists.Load() != 1 || scans.Load() != 1 {
		t.Fatalf("fresh facts are not read again: %d %d", gh.lists.Load(), scans.Load())
	}
	if len(kicked) != 1 {
		t.Fatalf("no refresh while the facts are fresh: %d", len(kicked))
	}

	// The facts age past FactsTTL: the list still answers from them and starts a refresh.
	now := time.Now().Add(FactsTTL + time.Second)
	r.svc.Now = func() time.Time { return now }
	r.gh.runners = nil
	if v, _ = r.svc.ListView(ctx, Filter{}); v[0].State != Idle {
		t.Fatalf("a stale list shows the last facts: %+v", v)
	}
	if gh.lists.Load() != 1 || len(kicked) != 2 {
		t.Fatalf("stale: no call on the request, one refresh started: %d %d", gh.lists.Load(), len(kicked))
	}
	kicked[1]()
	if v, _ = r.svc.ListView(ctx, Filter{}); v[0].State != NotRegistered {
		t.Fatalf("after the refresh: %+v", v)
	}
}

// CRSP389: a refresh reads the scopes at once, within a bound.
func TestRefreshFactsReadsScopesConcurrentlyWithinTheBound(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		rec := Record{ID: "r_x" + string(rune('a'+i)), Name: "box-" + string(rune('a'+i)), MachineID: "m1", OS: "linux", Kind: "ephemeral",
			State: Idle, Scope: Scope{Kind: "repository", RepositoryID: int64(100 + i)}}
		if err := r.store.InsertRunner(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	gh := &countingGitHub{fakeGitHub: r.gh, meet: make(chan struct{})}
	r.svc.GitHub = gh
	r.svc.RefreshFacts(ctx)
	if gh.lists.Load() < 2 {
		t.Fatalf("lists: %d", gh.lists.Load())
	}
	if p := gh.peak.Load(); p < 2 || p > factsParallel {
		t.Fatalf("peak concurrency %d, want 2 to %d", p, factsParallel)
	}
}
