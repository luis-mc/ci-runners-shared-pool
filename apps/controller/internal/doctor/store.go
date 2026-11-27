package doctor

import (
	"context"
	"sort"
	"sync"
	"time"
)

// RunState is where a run is.
type RunState string

// Run states.
const (
	Running RunState = "running"
	Done    RunState = "done"
	Failed  RunState = "failed"
)

// Trigger is what started a run.
type Trigger string

// Triggers.
const (
	TriggerWeb      Trigger = "web"
	TriggerCLI      Trigger = "cli"
	TriggerSchedule Trigger = "schedule"
	TriggerSystem   Trigger = "system"
)

// Run is one doctor run with its findings (spec 7.4).
type Run struct {
	ID         string
	Trigger    Trigger
	State      RunState
	Scope      Scope
	StartedBy  string
	StartedAt  time.Time
	FinishedAt time.Time
	Worst      Severity
	Counts     map[Severity]int
	Findings   []Finding
}

// Store keeps runs and the latest finding per check and subject (spec 7.5).
// The SQLite adapter over doctor_runs, doctor_findings and doctor_state lives
// with the store feature; MemStore is the in-process one used by tests and by
// a controller before that adapter is wired.
type Store interface {
	// SaveRun inserts or replaces a run by ID. A run with state done also
	// updates the latest finding per check and subject.
	SaveRun(ctx context.Context, r Run) error
	// GetRun returns a run with its findings.
	GetRun(ctx context.Context, id string) (Run, bool, error)
	// Latest returns the latest finding per check and subject, worst first,
	// filtered by severity and group when set, and when the newest run ended.
	Latest(ctx context.Context, severity Severity, group Group) ([]Finding, time.Time, error)
}

// MemStore is an in-memory Store.
type MemStore struct {
	mu     sync.Mutex
	runs   map[string]Run
	latest map[[3]string]Finding
	last   time.Time
}

// NewMemStore makes an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{runs: map[string]Run{}, latest: map[[3]string]Finding{}}
}

var _ Store = (*MemStore)(nil)

// SaveRun implements Store.
func (m *MemStore) SaveRun(_ context.Context, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Findings = append([]Finding(nil), r.Findings...)
	m.runs[r.ID] = r
	if r.State == Done {
		for _, f := range r.Findings {
			m.latest[[3]string{f.Check, f.Subject.Kind, f.Subject.ID}] = f
		}
		if r.FinishedAt.After(m.last) {
			m.last = r.FinishedAt
		}
	}
	return nil
}

// GetRun implements Store.
func (m *MemStore) GetRun(_ context.Context, id string) (Run, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	r.Findings = append([]Finding(nil), r.Findings...)
	return r, ok, nil
}

// Latest implements Store.
func (m *MemStore) Latest(_ context.Context, severity Severity, group Group) ([]Finding, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Finding
	for _, f := range m.latest {
		if severity != "" && f.Severity != severity {
			continue
		}
		if group != "" && GroupOf(f.Check) != group {
			continue
		}
		out = append(out, f)
	}
	SortFindings(out)
	return out, m.last, nil
}

// GroupOf is the group of a check ID: the part before the first dot.
func GroupOf(checkID string) Group {
	for i := 0; i < len(checkID); i++ {
		if checkID[i] == '.' {
			return Group(checkID[:i])
		}
	}
	return Group(checkID)
}

// SortFindings orders worst first, then by check, subject name and ID.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Severity.Rank() != b.Severity.Rank() {
			return a.Severity.Rank() > b.Severity.Rank()
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.Subject.Name != b.Subject.Name {
			return a.Subject.Name < b.Subject.Name
		}
		return a.Subject.ID < b.Subject.ID
	})
}
