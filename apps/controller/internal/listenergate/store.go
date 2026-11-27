package listenergate

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// MemStore keeps the leases in memory (tests, and a controller with no store).
type MemStore struct {
	mu     sync.Mutex
	leases map[int64]Lease
	last   map[string]time.Time
	waking map[string]time.Time
}

func (m *MemStore) init() {
	if m.leases == nil {
		m.leases, m.last, m.waking = map[int64]Lease{}, map[string]time.Time{}, map[string]time.Time{}
	}
}

// Claim implements Store.
func (m *MemStore) Claim(_ context.Context, l Lease) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if old, ok := m.leases[l.JobID]; ok {
		return old, false, nil
	}
	m.leases[l.JobID] = l
	return l, true, nil
}

// Get implements Store.
func (m *MemStore) Get(_ context.Context, id int64) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	l, ok := m.leases[id]
	return l, ok, nil
}

// Save implements Store.
func (m *MemStore) Save(_ context.Context, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.leases[l.JobID] = l
	return nil
}

// ListState implements Store.
func (m *MemStore) ListState(_ context.Context, s LeaseState) ([]Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var out []Lease
	for _, l := range m.leases {
		if l.State == s {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out, nil
}

// OpenFor implements Store.
func (m *MemStore) OpenFor(ctx context.Context, runnerID string) ([]Lease, error) {
	all, _ := m.ListState(ctx, Started)
	var out []Lease
	for _, l := range all {
		if l.Runner.ID == runnerID {
			out = append(out, l)
		}
	}
	return out, nil
}

// LastWake implements Store.
func (m *MemStore) LastWake(_ context.Context, id string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	return m.last[id], nil
}

// WakeStarted implements Store.
func (m *MemStore) WakeStarted(_ context.Context, id string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.last[id], m.waking[id] = t, t
	return nil
}

// WakeEnded implements Store.
func (m *MemStore) WakeEnded(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	delete(m.waking, id)
	return nil
}

// Waking implements Store.
func (m *MemStore) Waking(context.Context) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	out := make(map[string]time.Time, len(m.waking))
	for k, v := range m.waking {
		out[k] = v
	}
	return out, nil
}

// SQLStore keeps the leases in the store (migration 00040).
type SQLStore struct{ DB *store.Store }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func rowOf(l Lease) (store.ListenerLeaseRow, error) {
	b, err := json.Marshal(l)
	return store.ListenerLeaseRow{JobID: l.JobID, RunnerID: l.Runner.ID, State: string(l.State), Body: string(b), UpdatedAt: ts(l.UpdatedAt)}, err
}

func leaseOf(r store.ListenerLeaseRow) (Lease, error) {
	var l Lease
	err := json.Unmarshal([]byte(r.Body), &l)
	l.JobID, l.State = r.JobID, LeaseState(r.State)
	return l, err
}

// Claim implements Store.
func (s SQLStore) Claim(ctx context.Context, l Lease) (Lease, bool, error) {
	row, err := rowOf(l)
	if err != nil {
		return l, false, err
	}
	got, fresh, err := s.DB.ClaimListenerLease(ctx, row)
	if err != nil {
		return l, false, err
	}
	out, err := leaseOf(got)
	return out, fresh, err
}

// Get implements Store.
func (s SQLStore) Get(ctx context.Context, id int64) (Lease, bool, error) {
	r, ok, err := s.DB.ListenerLease(ctx, id)
	if err != nil || !ok {
		return Lease{}, false, err
	}
	l, err := leaseOf(r)
	return l, true, err
}

// Save implements Store.
func (s SQLStore) Save(ctx context.Context, l Lease) error {
	row, err := rowOf(l)
	if err != nil {
		return err
	}
	return s.DB.SaveListenerLease(ctx, row)
}

func (s SQLStore) list(ctx context.Context, st LeaseState, runner string) ([]Lease, error) {
	rows, err := s.DB.ListenerLeases(ctx, string(st), runner)
	if err != nil {
		return nil, err
	}
	out := make([]Lease, 0, len(rows))
	for _, r := range rows {
		l, err := leaseOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// ListState implements Store.
func (s SQLStore) ListState(ctx context.Context, st LeaseState) ([]Lease, error) {
	return s.list(ctx, st, "")
}

// OpenFor implements Store.
func (s SQLStore) OpenFor(ctx context.Context, runnerID string) ([]Lease, error) {
	return s.list(ctx, Started, runnerID)
}

// LastWake implements Store.
func (s SQLStore) LastWake(ctx context.Context, id string) (time.Time, error) {
	w, err := s.DB.ListenerWakeOf(ctx, id)
	return parseTS(w.LastWake), err
}

// WakeStarted implements Store.
func (s SQLStore) WakeStarted(ctx context.Context, id string, t time.Time) error {
	return s.DB.SaveListenerWake(ctx, store.ListenerWake{RunnerID: id, LastWake: ts(t), WakingSince: ts(t)})
}

// WakeEnded implements Store.
func (s SQLStore) WakeEnded(ctx context.Context, id string) error {
	w, err := s.DB.ListenerWakeOf(ctx, id)
	if err != nil {
		return err
	}
	w.WakingSince = ""
	return s.DB.SaveListenerWake(ctx, w)
}

// Waking implements Store.
func (s SQLStore) Waking(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.DB.ListenerWaking(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, w := range rows {
		out[w.RunnerID] = parseTS(w.WakingSince)
	}
	return out, nil
}

// SettingsStore reads and saves the gate settings. Enforce is false until the
// owner switches it on, and a saved set without it reads as false.
type SettingsStore struct{ DB *store.Store }

// Get returns the saved settings, or the defaults (observe mode).
func (s SettingsStore) Get(ctx context.Context) Settings {
	v, _ := s.DB.ListenerGateJSON(ctx)
	var in struct {
		Enforce bool `json:"enforce"`
	}
	_ = json.Unmarshal([]byte(v), &in)
	return Settings{Enforce: in.Enforce}
}

// Save saves the settings.
func (s SettingsStore) Save(ctx context.Context, next Settings, by string) error {
	b, _ := json.Marshal(struct {
		Enforce bool `json:"enforce"`
	}{next.Enforce})
	return s.DB.SaveListenerGate(ctx, string(b), by)
}
