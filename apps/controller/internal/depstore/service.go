package depstore

import (
	"context"
	"errors"
	"io"
	"strings"
)

// AgentInstaller is one agent package of this release (agentinstallers.Installer).
type AgentInstaller struct {
	Name, SHA256, OS, Arch string
	Cached                 bool
}

// AgentPackages is the store of the agent packages (internal/agentinstallers). It
// keeps its own files and its own checks; this service only lists and routes.
type AgentPackages interface {
	List(ctx context.Context) ([]AgentInstaller, error)
	// Fetch makes sure the package is stored and checked.
	Fetch(ctx context.Context, name string) error
	// Import keeps a file the owner carried over, if it matches SHA256SUMS.
	Import(ctx context.Context, name string, r io.Reader) error
}

// Service is what the console and the API see: every dependency in one list.
type Service struct {
	Store  *Store
	Agents AgentPackages
	// Audit writes one audit row; nil writes none.
	Audit func(ctx context.Context, actor, action, id string, details map[string]any)
}

func (s *Service) audit(ctx context.Context, actor, action, id string, err error, extra map[string]any) {
	if s.Audit == nil {
		return
	}
	d := map[string]any{"ok": err == nil}
	for k, v := range extra {
		d[k] = v
	}
	if err != nil {
		d["error"] = err.Error()
	}
	s.Audit(ctx, actor, action, id, d)
}

const agentPrefix = "agent-"

func agentID(a AgentInstaller) string {
	return agentPrefix + strings.ToLower(a.OS) + "-" + strings.ToLower(a.Arch)
}

func (s *Service) agents(ctx context.Context) map[string]AgentInstaller {
	out := map[string]AgentInstaller{}
	if s.Agents == nil {
		return out
	}
	list, err := s.Agents.List(ctx)
	if err != nil {
		return out
	}
	for _, a := range list {
		out[agentID(a)] = a
	}
	return out
}

func agentEntry(id string, a AgentInstaller) Entry {
	e := Entry{Spec: Spec{ID: id, Name: "crsp agent for " + a.OS + " " + a.Arch, Kind: KindAgent, OS: a.OS, Arch: a.Arch, Source: "github.com", SHA256: a.SHA256}, State: StateMissing}
	if a.SHA256 == "" {
		e.State = StateNotPinned
	}
	if a.Cached {
		e.State, e.Origin = StateReady, "fetched"
	}
	return e
}

// List returns every dependency and its state.
func (s *Service) List(ctx context.Context) []Entry {
	out := s.Store.List(ctx)
	ag := s.agents(ctx)
	ids := make([]string, 0, len(ag))
	for id := range ag {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		out = append(out, agentEntry(id, ag[id]))
	}
	return out
}

// Get returns one entry.
func (s *Service) Get(ctx context.Context, id string) (Entry, error) {
	if strings.HasPrefix(id, agentPrefix) {
		a, ok := s.agents(ctx)[id]
		if !ok {
			return Entry{}, ErrUnknown
		}
		return agentEntry(id, a), nil
	}
	return s.Store.Get(ctx, id)
}

// Fetch downloads one dependency (the controller must have internet).
func (s *Service) Fetch(ctx context.Context, actor, id string) error {
	err := s.fetch(ctx, id)
	if !errors.Is(err, ErrUnknown) {
		s.audit(ctx, actor, "dependency.store.fetch", id, err, nil)
	}
	return err
}

func (s *Service) fetch(ctx context.Context, id string) error {
	if strings.HasPrefix(id, agentPrefix) {
		a, ok := s.agents(ctx)[id]
		if !ok {
			return ErrUnknown
		}
		return s.Agents.Fetch(ctx, a.Name)
	}
	return s.Store.FetchOne(ctx, id)
}

// Import keeps a file carried over by the owner; a file that does not match is refused.
func (s *Service) Import(ctx context.Context, actor, id string, r io.Reader, limit int64) error {
	err := s.importFile(ctx, id, r, limit)
	if !errors.Is(err, ErrUnknown) {
		s.audit(ctx, actor, "dependency.store.import", id, err, nil)
	}
	return err
}

func (s *Service) importFile(ctx context.Context, id string, r io.Reader, limit int64) error {
	if strings.HasPrefix(id, agentPrefix) {
		a, ok := s.agents(ctx)[id]
		if !ok {
			return ErrUnknown
		}
		return s.Agents.Import(ctx, a.Name, r)
	}
	return s.Store.Import(ctx, id, r, limit)
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
