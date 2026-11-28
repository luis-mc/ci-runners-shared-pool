package runners

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// FactsTTL is how old the GitHub runner lists and the agent service scans may
// be before a request starts a refresh in the background. The list itself never
// waits for the refresh: it shows the facts of the last one (CRSP389).
const FactsTTL = 15 * time.Second

// factsLoopEvery is how often the background loop refreshes the facts.
const factsLoopEvery = 20 * time.Second

// factsParallel bounds the GitHub and agent calls a refresh makes at once.
const factsParallel = 4

// factsTimeout bounds one refresh.
const factsTimeout = 2 * time.Minute

type ghEntry struct {
	list ghList
}

type svcEntry struct {
	byKey map[string]backend.Found
}

// factCache holds what GitHub's runner lists and the machines' agents said at
// the last refresh. The zero value is empty and ready.
type factCache struct {
	mu         sync.Mutex
	gh         map[string]ghEntry
	svc        map[string]svcEntry
	at         time.Time // when the last refresh finished; zero before the first
	refreshing bool
}

func ghKey(sc Scope) string {
	return fmt.Sprintf("%s|%d|%s|%s", sc.Kind, sc.RepositoryID, sc.Organization, sc.Group)
}

func (c *factCache) ghFor(sc Scope) (ghList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.gh[ghKey(sc)]
	return e.list, ok
}

func (c *factCache) svcFor(machineID string) (map[string]backend.Found, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.svc[machineID]
	return e.byKey, ok && e.byKey != nil
}

// serviceMap indexes what an agent found by service name and by runner name.
func serviceMap(found []backend.Found) map[string]backend.Found {
	m := map[string]backend.Found{}
	for _, x := range found {
		m[x.ServiceName] = x
	}
	// A container runner is also found by name: the folder may carry a -r<repo id>
	// suffix the record's ref does not (OPS70).
	for _, x := range found {
		if _, dup := m[nameKey(x.Name)]; !dup && x.Name != "" {
			m[nameKey(x.Name)] = x
		}
	}
	return m
}

// wantsFacts is true for a record whose state is read from GitHub and the agent.
func wantsFacts(r Record) bool { return r.State == Idle || r.State == Busy || r.State == Offline }

// factsStale says whether the cache is older than FactsTTL or was never filled,
// and no refresh runs now.
func (s *Service) factsStale() bool {
	s.facts.mu.Lock()
	defer s.facts.mu.Unlock()
	if s.facts.refreshing {
		return false
	}
	return s.facts.at.IsZero() || s.now().Sub(s.facts.at) >= FactsTTL
}

// kickFacts starts a refresh in the background when the facts are stale. It
// returns at once.
func (s *Service) kickFacts() {
	if !s.factsStale() {
		return
	}
	s.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), factsTimeout)
		defer cancel()
		s.RefreshFacts(ctx)
	})
}

// FactsLoop refreshes the facts every few seconds until ctx ends, so the list
// answers from fresh facts without a request starting the work.
func (s *Service) FactsLoop(ctx context.Context) {
	t := time.NewTicker(factsLoopEvery)
	defer t.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, factsTimeout)
		s.RefreshFacts(rctx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RefreshFacts reads GitHub's runner list of every scope that has a runner and
// the service list of every machine that has a persistent runner, with at most
// factsParallel calls at once, and keeps the result for ListView. Only one
// refresh runs at a time.
func (s *Service) RefreshFacts(ctx context.Context) {
	s.facts.mu.Lock()
	if s.facts.refreshing {
		s.facts.mu.Unlock()
		return
	}
	s.facts.refreshing = true
	s.facts.mu.Unlock()
	defer func() {
		s.facts.mu.Lock()
		s.facts.refreshing = false
		s.facts.mu.Unlock()
	}()

	recs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return
	}
	scopes := map[string]Scope{}
	machines := map[string]bool{}
	for _, r := range recs {
		if !wantsFacts(r) {
			continue
		}
		scopes[ghKey(r.Scope)] = r.Scope
		if r.Kind == "persistent" && s.Services != nil {
			machines[r.MachineID] = true
		}
	}
	gh := make(map[string]ghEntry, len(scopes))
	svc := make(map[string]svcEntry, len(machines))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, factsParallel)
	run := func(f func()) {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			f()
		}()
	}
	bg := github.Background(ctx)
	for k, sc := range scopes {
		run(func() {
			l := s.githubList(bg, sc)
			mu.Lock()
			gh[k] = ghEntry{list: l}
			mu.Unlock()
		})
	}
	for id := range machines {
		run(func() {
			found, err := s.Services(ctx, id)
			e := svcEntry{}
			if err == nil {
				e.byKey = serviceMap(found)
			}
			mu.Lock()
			svc[id] = e
			mu.Unlock()
		})
	}
	wg.Wait()
	s.facts.mu.Lock()
	s.facts.gh, s.facts.svc, s.facts.at = gh, svc, s.now()
	s.facts.mu.Unlock()
}
