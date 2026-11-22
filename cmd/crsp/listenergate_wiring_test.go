package main

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// runners R5.134: only a persistent runner on the machine itself is gated; a
// container, a pool runner, a VM runner and a hand-made one are not.
func TestOnlyAPersistentRunnerOnTheMachineItselfIsGated(t *testing.T) {
	host := runners.Record{Kind: "persistent", Backend: "persistent", Managed: true, Scope: runners.Scope{Kind: "repository"},
		ServiceName: "actions.runner.o-r.box-1", GitHubRunnerID: 7, State: runners.Idle}
	if !onMachineItself(host) {
		t.Fatal("a host runner is not gated")
	}
	for name, mod := range map[string]func(*runners.Record){
		"ephemeral":      func(r *runners.Record) { r.Kind = "ephemeral" },
		"container":      func(r *runners.Record) { r.Backend = "docker"; r.ServiceName = "pool:box-1" },
		"pool runner":    func(r *runners.Record) { r.Scope.Kind = runners.ScopePool },
		"not ours":       func(r *runners.Record) { r.Managed = false },
		"not registered": func(r *runners.Record) { r.GitHubRunnerID = 0 },
		"unclean":        func(r *runners.Record) { r.UncleanReason = "folder busy" },
		"failed":         func(r *runners.Record) { r.State = runners.Failed },
	} {
		r := host
		mod(&r)
		if onMachineItself(r) {
			t.Errorf("%s runner is gated", name)
		}
	}
}
