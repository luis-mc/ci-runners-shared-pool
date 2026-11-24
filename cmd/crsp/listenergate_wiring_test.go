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

// OPS87 (runners R5.93, R5.134): the persistent Linux container runner of a Windows machine is always on, never
// gated, whatever its scope. The pool runner and each repository registration it makes are both left alone.
func TestPersistentLinuxContainerRunnerOnWindowsIsNeverGated(t *testing.T) {
	pool := runners.Record{Kind: "persistent", Backend: "docker", OS: "linux", Isolation: "container", Managed: true,
		Scope: runners.Scope{Kind: runners.ScopePool}, ServiceName: "crsp-persistent-pool:gameroompc-linux-1", GitHubRunnerID: 9, State: runners.Idle}
	reg := pool
	reg.Scope = runners.Scope{Kind: "repository", RepositoryID: 7}
	reg.PoolRunnerID = "r_pool"
	repoScoped := reg
	repoScoped.PoolRunnerID = ""
	repoScoped.ServiceName = "gameroompc-linux-1"
	for name, r := range map[string]runners.Record{"pool runner": pool, "registration": reg, "repository runner": repoScoped} {
		if onMachineItself(r) {
			t.Errorf("%s of the container is gated", name)
		}
	}
}
