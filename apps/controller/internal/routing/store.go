package routing

import (
	"context"
	"sync"
	"time"
)

// Label states.
const (
	LabelActive   = "active"
	LabelRetiring = "retiring"
)

// JobState is the variable crsp wrote for one job.
type JobState struct {
	Key   string
	Var   string
	Label string
	Class string
	OS    string
	// Value is the JSON list the variable holds.
	Value string
}

// LabelState is a label crsp serves. A retiring label stays served until no
// run created before Since is queued or running; then it is removed.
type LabelState struct {
	Label string
	State string
	Since time.Time
	Jobs  []string
}

// RunnerLabel records a custom label crsp added to a persistent runner.
type RunnerLabel struct {
	Repository string // the repository whose routing owns the label
	RunnerID   string
	GitHubID   int64
	Label      string
	ScopeOwner string
	ScopeRepo  string // empty for an organization runner
	ScopeOrg   string
}

// PRState is the one pull request opened for a repository.
type PRState struct {
	Number   int
	URL      string
	OpenedAt time.Time
}

// State is everything crsp keeps for the routing of one repository.
type State struct {
	Jobs    []JobState
	Labels  []LabelState
	Runners []RunnerLabel
	PR      *PRState
}

// Store keeps the routing state per repository ("owner/name").
type Store interface {
	Load(ctx context.Context, repository string) (State, error)
	Save(ctx context.Context, repository string, st State) error
}

// MemStore is a Store in memory, for tests.
type MemStore struct {
	mu sync.Mutex
	m  map[string]State
}

// Load implements Store.
func (s *MemStore) Load(_ context.Context, repository string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyState(s.m[repository]), nil
}

// Save implements Store.
func (s *MemStore) Save(_ context.Context, repository string, st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]State{}
	}
	s.m[repository] = copyState(st)
	return nil
}

func copyState(st State) State {
	out := State{Jobs: append([]JobState(nil), st.Jobs...), Runners: append([]RunnerLabel(nil), st.Runners...)}
	for _, l := range st.Labels {
		l.Jobs = append([]string(nil), l.Jobs...)
		out.Labels = append(out.Labels, l)
	}
	if st.PR != nil {
		p := *st.PR
		out.PR = &p
	}
	return out
}
