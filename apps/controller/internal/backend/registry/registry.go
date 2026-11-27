// Package registry is the set of backends built into this binary for this OS
// (runners R5.24). Each OS file is behind a build tag, so the Windows backend
// is compiled only into the Windows binary, and so on.
package registry

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// Registry implements backend.Registry over a fixed list.
type Registry struct{ list []backend.Backend }

var _ backend.Registry = (*Registry)(nil)

// All returns every backend compiled in, best first.
func (r *Registry) All() []backend.Backend { return r.list }

// Get returns the backend with the name.
func (r *Registry) Get(name backend.Name) (backend.Backend, bool) {
	for _, b := range r.list {
		if b.Name() == name {
			return b, true
		}
	}
	return nil, false
}

// With builds a registry from backends, for tests and other seams.
func With(b ...backend.Backend) *Registry { return &Registry{list: b} }

// RealDeps wires the real command runner, file system and downloader. The log
// function receives setup log lines; redact removes known secrets first.
func RealDeps(logLine func(step, msg string), redact func(string) string, secret ...func(string)) sys.Deps {
	var learn func(string)
	if len(secret) > 0 {
		learn = secret[0]
	}
	return sys.Deps{
		Cmd:     sys.ExecCommands{},
		FS:      sys.OSFS{},
		Fetch:   sys.HTTPFetcher{},
		Verify:  runners.VerifyFile,
		Redact:  redact,
		Secret:  learn,
		LogLine: logLine,
	}
}

// Platform is the registry for this OS with the production layout.
// ids, when given, is the source of the persistent runner uid/gid (runners
// R5.72); the Linux backends use it.
func Platform(d sys.Deps, ids ...backend.IDSource) *Registry {
	var src backend.IDSource
	if len(ids) > 0 {
		src = ids[0]
	}
	return &Registry{list: platform(d, src)}
}
