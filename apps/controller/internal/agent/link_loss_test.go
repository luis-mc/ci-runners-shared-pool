package agent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type sleepLog struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleepLog) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	select { // do not really wait long
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil
	}
}
func (s *sleepLog) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.d) }

// R5.37: a lost link does not stop runners. Jobs already running keep running,
// and the machine's own gate keeps enforcing slots and budget from its last
// settings. R5.36: the agent reconnects with backoff; a successful session
// resets it. R5.38: on reconnect it sends the jobs it runs.
func TestLinkLossKeepsJobsAndGateThenReconnects(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	sl := &sleepLog{}
	cfg.Sleep = sl.sleep
	cfg.Backoff = &agent.Backoff{Rand: func() float64 { return 0 }}
	cfg.Gate = agent.NewGate(cfg.Sampler)
	runAgent(t, cfg)
	var id string
	waitFor(t, "online with settings", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online {
			id = vs[0].Machine.ID
		}
		_, ok := cfg.Gate.Settings()
		return id != "" && ok
	})
	waitFor(t, "first load sample", func() bool { _, ok := cfg.Sampler.Latest(); return ok })
	// One slot: tighten the settings, then start a job.
	one := 1
	if _, _, err := c.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{JobSlots: &one}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "one slot applied", func() bool { s, _ := cfg.Gate.Settings(); return s.JobSlots == 1 })
	jr := machines.JobRef{Repository: "o/r", RunID: 9, Job: "build"}
	if d := cfg.Gate.Start("j1", jr, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}

	c.Stop() // the controller goes away
	waitFor(t, "backoff tries", func() bool { return sl.count() >= 3 })
	sl.mu.Lock()
	first := sl.d[:3]
	sl.mu.Unlock()
	if first[0] != time.Second || first[1] != 2*time.Second || first[2] != 4*time.Second {
		t.Fatalf("backoff = %v, want 1s, 2s, 4s", first)
	}
	if len(cfg.Gate.Running()) != 1 {
		t.Fatal("the running job was dropped when the link broke")
	}
	if d := cfg.Gate.Start("j2", jr, "persistent"); d.Go || d.Reasons[0] != "free_slot" {
		t.Fatalf("the gate stopped enforcing slots from the last settings: %+v", d)
	}

	c.Start(t) // the controller is back
	waitFor(t, "reconnected with the running job reported", func() bool {
		v, err := c.Svc.Get(context.Background(), id)
		return err == nil && v.Machine.Online && len(v.RunningJobs) == 1 && v.RunningJobs[0].RunID == 9
	})
	// The agent resets its backoff after the hello answer; the first heartbeat
	// after that proves the reset ran, so wait for a newer LastSeen.
	c.Now.Add(time.Minute)
	waitFor(t, "a heartbeat in the new session", func() bool {
		v, err := c.Svc.Get(context.Background(), id)
		return err == nil && v.Machine.LastSeen.After(c.Now.Now().Add(-time.Second))
	})
	// A successful session reset the backoff: the next loss starts at 1 s again.
	n := sl.count()
	c.Stop()
	waitFor(t, "a new backoff try", func() bool { return sl.count() > n })
	sl.mu.Lock()
	next := sl.d[n]
	sl.mu.Unlock()
	if next != time.Second {
		t.Fatalf("the backoff after a good session = %v, want 1s", next)
	}
}
