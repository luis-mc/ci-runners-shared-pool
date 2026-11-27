package main

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/registry"
)

// agentCacheSource lets an agent find its runners' cache folders from the OS
// service list (caches R5.36, runners R5.5). The agent measures and wipes; it
// takes no path from the controller.
func agentCacheSource() agent.CacheSource {
	reg := registry.Platform(realDeps(func(string, string) {}, secretRedactor.Redact, secretRedactor.Add))
	var ctr agent.ContainerRunners
	if b, ok := reg.Get(backend.Docker); ok {
		if pc, ok := b.(persistentContainer); ok {
			ctr = containerRunners{pc}
		}
	}
	return agent.FoundRunners{Container: ctr, Find: func(ctx context.Context) ([]agent.CacheRunner, error) {
		b, ok := reg.Get(backend.Persistent)
		if !ok {
			return nil, nil
		}
		f, ok := b.(backend.Finder)
		if !ok {
			return nil, nil
		}
		found, err := f.Find(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]agent.CacheRunner, 0, len(found))
		for _, r := range found {
			if r.Managed && r.Folder != "" {
				out = append(out, agent.CacheRunner{Name: r.Name, Service: r.ServiceName, Folder: r.Folder})
			}
		}
		return out, nil
	}}
}

// persistentContainer is the container backend's part for persistent Linux
// runners (runners R5.93).
type persistentContainer interface {
	PersistentStates(ctx context.Context) ([]container.PersistentState, error)
	ApplyHygiene(ctx context.Context, name string, keep, clear bool) error
}

type containerRunners struct{ b persistentContainer }

func (c containerRunners) Clean(ctx context.Context) ([]agent.ContainerClean, error) {
	st, err := c.b.PersistentStates(ctx)
	out := make([]agent.ContainerClean, 0, len(st))
	for _, s := range st {
		out = append(out, agent.ContainerClean{Name: s.Name, DependencyBytes: s.DependencyBytes, UncleanReason: s.UncleanReason})
	}
	return out, err
}

func (c containerRunners) Apply(ctx context.Context, name string, keep, clear bool) error {
	return c.b.ApplyHygiene(ctx, name, keep, clear)
}
