package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

func timeAfter() <-chan time.Time { return time.After(5 * time.Second) }

// R5.42: if the machine is offline, removal still revokes the key at once. The
// change says the agent will learn this on its next connect and that its local
// files stay until it is uninstalled. When it connects, it is refused with the
// "removed" reason (R5.39).
func TestRemoveOfflineMachineRevokesAtOnce(t *testing.T) {
	e, id, key := setup(t)
	ch, err := e.Svc.Remove(context.Background(), "owner", id, true)
	if err != nil {
		t.Fatal(err)
	}
	if ch.State != machines.ChangeDone || !strings.Contains(ch.Detail.Reason, "next connect") || !strings.Contains(ch.Detail.Reason, "local files stay") {
		t.Fatalf("change = %+v", ch)
	}
	if _, err := e.Svc.Authenticate(context.Background(), key, ""); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("key still valid: %v", err)
	}
	_, err = e.session(id, key, nil)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("next connect: %v", err)
	}
	if _, err := e.Svc.Remove(context.Background(), "owner", id, true); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
}

// R5.42: an offline machine's runners are deregistered from GitHub, and the change says which ones
// could not be.
func TestRemoveOfflineMachineDeregistersItsRunners(t *testing.T) {
	e, id, _ := setup(t)
	var gotUp = true
	e.Svc.SetRunnerRemover(func(_ context.Context, machineID, _ string, agentUp bool) []machines.RunnerRemoval {
		gotUp = agentUp
		if machineID != id {
			t.Errorf("machine %s", machineID)
		}
		return []machines.RunnerRemoval{{Name: "crsp-a", Done: true}, {Name: "crsp-b", Reason: "GitHub refused"}}
	})
	ch, err := e.Svc.Remove(context.Background(), "owner", id, true)
	if err != nil {
		t.Fatal(err)
	}
	if gotUp {
		t.Fatal("an offline machine has no agent to clean up")
	}
	if !strings.Contains(ch.Detail.Reason, "1 runners deregistered from GitHub") || !strings.Contains(ch.Detail.Reason, "crsp-b (GitHub refused)") {
		t.Fatalf("reason = %q", ch.Detail.Reason)
	}
}
