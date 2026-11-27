package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.40: Remove names every item that goes and asks for a yes; with the yes it
// drains first. R5.41: when idle the key is revoked at once and the link closed.
func TestRemoveNamesWhatGoesDrainsThenRevokes(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	fa, _ := e.session(id, r.GetMachineCredential(), &agentv1.Hello{
		Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{oneJob}},
		Runners: &agentv1.RunnerStates{Runners: []*agentv1.RunnerStatus{{Name: "runner-1"}}},
	})
	_, err := e.Svc.Remove(context.Background(), "owner", id, false)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want needs confirmation", err)
	}
	all := strings.Join(nc.Names, "\n")
	for _, want := range []string{"box-1", "runner-1", "images and caches", "settings", "key", "o/r"} {
		if !strings.Contains(all, want) {
			t.Errorf("the list of what goes lacks %q:\n%s", want, all)
		}
	}
	// Nothing happened yet.
	if _, err := e.Svc.Get(context.Background(), id); err != nil {
		t.Fatalf("machine gone before the yes: %v", err)
	}
	ch, err := e.Svc.Remove(context.Background(), "owner", id, true)
	if err != nil || ch.Kind != machines.KindRemove || ch.State != machines.ChangeWaiting {
		t.Fatalf("ch=%+v err=%v", ch, err)
	}
	fa.Next(isDrain)
	// While jobs run the key still works: removal waits.
	if _, err := e.Svc.Authenticate(context.Background(), r.GetMachineCredential(), ""); err != nil {
		t.Fatalf("key revoked before the drain finished: %v", err)
	}
	fa.Send(drainStatus(ch.ID, true))
	m := fa.Next(func(m *agentv1.SessionResponse) bool { return m.GetMachineRemoved() != nil })
	if m.GetMachineRemoved().GetReason() == "" {
		t.Fatal("no reason sent")
	}
	if _, err := e.Svc.Authenticate(context.Background(), r.GetMachineCredential(), ""); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("key still valid: %v", err)
	}
	if _, err := e.Svc.Get(context.Background(), id); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("machine still listed: %v", err)
	}
	if vs, _ := e.Svc.List(context.Background()); len(vs) != 0 {
		t.Fatalf("list = %d", len(vs))
	}
	// The stream closes after the notice.
	select {
	case err := <-fa.errc:
		_ = err
	case <-timeAfter():
		t.Fatal("the link stayed open after removal")
	}
	_ = connect.CodeUnknown
}
