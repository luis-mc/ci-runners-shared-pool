package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

func waitRun(t *testing.T, cfg agent.Config) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errc := make(chan error, 1)
	go func() { errc <- agent.Run(ctx, cfg) }()
	return errc
}

// R5.39: when the controller refuses the key (machine removed), the agent stops
// retrying, says "this machine was removed from the pool", drops its key and
// waits for a new join.
func TestRemovedMachineStopsRetryingAndForgetsItsKey(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	sl := &sleepLog{}
	cfg.Sleep = sl.sleep
	errc := waitRun(t, cfg)
	var id string
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online && c.Svc.Connected(vs[0].Machine.ID) {
			id = vs[0].Machine.ID
			return true
		}
		return false
	})
	ch, err := c.Svc.Remove(context.Background(), "owner", id, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = ch // the agent is idle: it reports so itself, the controller revokes
	select {
	case err := <-errc:
		if !errors.Is(err, agent.ErrRemoved) || err.Error() != "this machine was removed from the pool" {
			t.Fatalf("Run err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent kept running after it was removed")
	}
	if sl.count() != 0 {
		t.Fatalf("the agent retried %d times after removal", sl.count())
	}
	if _, err := cfg.Secrets.Get(context.Background(), agent.MachineKeySecret); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("the revoked key is still kept: %v", err)
	}
	// A new join works with a new code and a new key (R5.43).
	code2, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg.JoinCode = code2.Code
	runAgent(t, cfg)
	waitFor(t, "joined again", func() bool {
		vs, _ := c.Svc.List(context.Background())
		return len(vs) == 1 && vs[0].Machine.ID != id && vs[0].Machine.Online
	})
}

// R5.39 on reconnect: a machine removed while it was offline learns it on its
// next connect and stops.
func TestRemovedWhileOfflineLearnsOnNextConnect(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Sleep = (&sleepLog{}).sleep
	errc := waitRun(t, cfg)
	var id string
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online {
			id = vs[0].Machine.ID
		}
		return id != ""
	})
	c.Stop()
	waitFor(t, "offline", func() bool { return !c.Svc.Connected(id) })
	if _, err := c.Svc.Remove(context.Background(), "owner", id, true); err != nil {
		t.Fatal(err)
	}
	c.Start(t)
	select {
	case err := <-errc:
		if !errors.Is(err, agent.ErrRemoved) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not learn it was removed")
	}
}
