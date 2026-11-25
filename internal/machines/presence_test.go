package machines_test

import (
	"context"
	"sync"
	"testing"
	"time"
)

type presenceCall struct {
	online bool
	reason string
}

type presenceLog struct {
	mu    sync.Mutex
	calls []presenceCall
}

func (p *presenceLog) Observe(_ context.Context, _ string, online bool, reason string, _, _ time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, presenceCall{online, reason})
}

func (p *presenceLog) last() presenceCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return presenceCall{}
	}
	return p.calls[len(p.calls)-1]
}

// R5.83: the service tells the observer when a machine comes online and goes offline, and why it
// took no job.
func TestPresenceObserverSeesOnlineOfflineAndReason(t *testing.T) {
	ctx := context.Background()
	e, id, key := setup(t)
	log := &presenceLog{}
	e.Svc.SetPresenceObserver(log)
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	fa.Send(heartbeat())
	_ = e.Svc.Tick(ctx)
	if c := log.last(); !c.online || c.reason != "" {
		t.Fatalf("online and taking: %+v", log.calls)
	}
	e.Clk.Add(31 * time.Second)
	_ = e.Svc.Tick(ctx)
	if c := log.last(); c.online || c.reason != "heartbeat_lost" {
		t.Fatalf("offline for a lost heartbeat: %+v", log.calls)
	}
}
