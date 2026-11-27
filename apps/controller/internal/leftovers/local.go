package leftovers

import (
	"context"
	"log/slog"
	"time"
)

// localSource is the Source of this host: the controller's own machine, and
// the agent link handlers, use it. The machine id is ignored.
type localSource struct {
	os  OS
	log *slog.Logger
}

// NewLocalSource scans and removes on this host with the real OS layer.
func NewLocalSource(log *slog.Logger) Source { return NewSourceFor(NewHostOS(), log) }

// NewSourceFor is a Source over the given OS layer (tests give a fake).
func NewSourceFor(o OS, log *slog.Logger) Source {
	if log == nil {
		log = slog.Default()
	}
	return &localSource{os: o, log: log}
}

func (s *localSource) Scan(ctx context.Context, _ string, owned []Owned) (Scan, error) {
	return Find(ctx, s.os, owned, time.Now())
}

func (s *localSource) Remove(ctx context.Context, _ string, owned []Owned, refs []Ref) ([]Result, error) {
	return Remove(ctx, s.os, owned, refs, func(m string) { s.log.Info("runner leftovers: " + m) })
}
