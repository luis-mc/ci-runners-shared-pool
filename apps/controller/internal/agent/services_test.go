package agent_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

// healingRunners is the agent's runner backend with the heal step of the
// Windows backend: each pass answers what the script says.
type healingRunners struct {
	fakeRunners
	hmu    sync.Mutex
	passes int
	answer func(pass int) ([]backend.ServiceHealth, error)
}

func (h *healingRunners) HealServices(context.Context) ([]backend.ServiceHealth, error) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.passes++
	return h.answer(h.passes)
}

func (h *healingRunners) count() int { h.hmu.Lock(); defer h.hmu.Unlock(); return h.passes }

const lostWhy = "the service did not start: Logon failure: the user has not been granted the requested logon type at this computer (Windows error 1069)"

// R5.89: the agent heals the runner services when it starts, before any
// command arrives, and reports what it saw to the controller, which shows a
// problem for a service that did not start.
func TestAgentHealsAtStartAndReportsTheProblem(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Redactor = redact.New()
	hr := &healingRunners{answer: func(int) ([]backend.ServiceHealth, error) {
		return []backend.ServiceHealth{{Runner: "crsp-gameroompc-1", Service: "actions.runner.o-r.crsp-gameroompc-1", Account: `.\crsp-gameroompc-1`,
			LogonKnown: true, LogonHeld: false, Problem: lostWhy}}, errors.New(lostWhy)
	}}
	cfg.Runners = hr
	runAgent(t, cfg)
	waitFor(t, "the heal at start", func() bool { return hr.count() >= 1 })
	waitFor(t, "the problem on the machine view", func() bool {
		vs, _ := c.Svc.List(context.Background())
		return len(vs) == 1 && len(vs[0].RunnerProblems()) == 1
	})
	vs, _ := c.Svc.List(context.Background())
	p := vs[0].RunnerProblems()[0]
	if p.Runner != "crsp-gameroompc-1" || p.Account != `.\crsp-gameroompc-1` || p.Problem != lostWhy {
		t.Errorf("problem = %+v", p)
	}
}

// R5.89: once a pass finds the service running the problem is gone from the
// controller. "Check again" (Redetect) makes the agent do a pass at once.
func TestProblemClearsWhenTheServiceRuns(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Redactor = redact.New()
	var mu sync.Mutex
	fixed := false
	hr := &healingRunners{answer: func(int) ([]backend.ServiceHealth, error) {
		mu.Lock()
		defer mu.Unlock()
		s := backend.ServiceHealth{Runner: "crsp-a", Service: "svc-a", Account: `.\crsp-a`, LogonKnown: true, LogonHeld: fixed, Running: fixed}
		if !fixed {
			s.Problem = lostWhy
		}
		return []backend.ServiceHealth{s}, nil
	}}
	cfg.Runners = hr
	runAgent(t, cfg)
	var id string
	waitFor(t, "the problem", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && len(vs[0].RunnerProblems()) == 1 {
			id = vs[0].Machine.ID
			return true
		}
		return false
	})
	mu.Lock()
	fixed = true
	mu.Unlock()
	if err := c.Svc.Redetect(context.Background(), "owner", id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the problem to clear", func() bool {
		vs, _ := c.Svc.List(context.Background())
		return len(vs) == 1 && len(vs[0].RunnerServices) == 1 && vs[0].RunnerServices[0].Running && len(vs[0].RunnerProblems()) == 0
	})
}

// R5.178: the controller's own agent carries no Runners, only a Healer; the
// heal still runs at start, so a runner service that stopped at boot starts again.
func TestAgentHealsWithAHealerAndNoRunners(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Redactor = redact.New()
	hr := &healingRunners{answer: func(int) ([]backend.ServiceHealth, error) {
		return []backend.ServiceHealth{{Runner: "crsp-gameroompc-1", Service: "actions.runner.o-r.crsp-gameroompc-1", Account: `.\crsp-gameroompc-1`,
			Running: true, LogonKnown: true, LogonHeld: true, Problem: "started again after it stopped"}}, nil
	}}
	cfg.Healer = hr
	runAgent(t, cfg)
	waitFor(t, "the heal at start", func() bool { return hr.count() >= 1 })
}
