package machines_test

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R6.4: every join, settings change, drain, resume, removal and key revocation
// is written to the audit log: who, what, when.
func TestEveryChangeIsAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	fa, _ := e.session(id, r.GetMachineCredential(), nil)
	_, _, _ = e.Svc.UpdateSettings(ctx, "alice", id, machines.SettingsUpdate{JobSlots: ptr(3)})
	ch, _ := e.Svc.Drain(ctx, "bob", id, "")
	fa.Next(isDrain)
	fa.Send(drainStatus(ch.ID, true))
	waitFor(t, "drained", func() bool { v, _ := e.Svc.Get(ctx, id); return v.Machine.State == machines.StateDrained })
	_, _ = e.Svc.Resume(ctx, "carol", id, false)
	rm, _ := e.Svc.Remove(ctx, "dave", id, true)
	fa.Next(isDrain)
	fa.Send(drainStatus(rm.ID, true))
	waitFor(t, "removed", func() bool { _, err := e.Svc.Get(ctx, id); return err != nil })

	seen := map[string]string{}
	for _, a := range e.Repo.AuditLog() {
		seen[a.Action] = a.Actor
		if a.At.IsZero() || a.Actor == "" || a.Subject == "" && !strings.HasPrefix(a.Action, "join_code") {
			t.Errorf("incomplete entry %+v", a)
		}
	}
	want := map[string]string{
		"join_code.create": "owner", "machine.join": "agent", "machine.settings": "alice", "machine.drain": "bob",
		"machine.resume": "carol", "machine.remove": "dave", "machine.key.revoke": "dave",
	}
	for action, actor := range want {
		if seen[action] != actor {
			t.Errorf("audit %q by %q, want %q (log: %v)", action, seen[action], actor, seen)
		}
	}
}
