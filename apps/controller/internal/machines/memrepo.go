package machines

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// AuditEntry is one audit record kept by MemRepo.
type AuditEntry struct {
	At                             time.Time
	Actor, Action, Subject, Detail string
}

// MemRepo is an in-memory Repository for tests and for a controller: with a path
// (OpenFileRepo) it is saved to a file and survives a restart; without, nothing does.
type MemRepo struct {
	mu       sync.Mutex
	machines map[string]Machine
	settings map[string]Settings
	keys     map[string]string // hash -> machine id (live keys)
	revoked  map[string]bool   // hash -> revoked
	codes    map[string]JoinCode
	load     map[string][]LoadPoint
	changes  map[string]Change
	audit    []AuditEntry
	// Down makes every call fail, to test the failure mode "database unavailable".
	Down bool
	// Now stamps audit entries.
	Now func() time.Time

	// path, dirty and saved keep the file of OpenFileRepo (filerepo.go).
	path  string
	dirty bool
	saved time.Time
}

var _ Repository = (*MemRepo)(nil)

// NewMemRepo returns an empty repository.
func NewMemRepo() *MemRepo {
	return &MemRepo{
		machines: map[string]Machine{}, settings: map[string]Settings{}, keys: map[string]string{},
		revoked: map[string]bool{}, codes: map[string]JoinCode{}, load: map[string][]LoadPoint{},
		changes: map[string]Change{}, Now: time.Now,
	}
}

var errDown = fmt.Errorf("database unavailable")

func (r *MemRepo) lock() (func(), error) {
	r.mu.Lock()
	if r.Down {
		r.mu.Unlock()
		return nil, errDown
	}
	return r.mu.Unlock, nil
}

// InsertMachine implements Repository.
func (r *MemRepo) InsertMachine(_ context.Context, m Machine, s Settings, keyHash string) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	for _, o := range r.machines {
		if o.Name == m.Name && o.RemovedAt.IsZero() {
			return fmt.Errorf("%w: name %q is taken", ErrConflict, m.Name)
		}
	}
	r.machines[m.ID], r.settings[m.ID] = m, s
	r.keys[keyHash] = m.ID
	return nil
}

// GetMachine implements Repository.
func (r *MemRepo) GetMachine(_ context.Context, id string) (Machine, error) {
	u, err := r.lock()
	if err != nil {
		return Machine{}, err
	}
	defer u()
	m, ok := r.machines[id]
	if !ok {
		return Machine{}, ErrNotFound
	}
	return m, nil
}

// ListMachines implements Repository.
func (r *MemRepo) ListMachines(_ context.Context) ([]Machine, error) {
	u, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer u()
	var out []Machine
	for _, m := range r.machines {
		if m.RemovedAt.IsZero() {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsController != out[j].IsController {
			return out[i].IsController
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// UpdateMachine implements Repository.
func (r *MemRepo) UpdateMachine(_ context.Context, m Machine) error {
	u, err := r.lockW(false)
	if err != nil {
		return err
	}
	defer u()
	if _, ok := r.machines[m.ID]; !ok {
		return ErrNotFound
	}
	for _, o := range r.machines {
		if o.ID != m.ID && o.Name == m.Name && o.RemovedAt.IsZero() {
			return fmt.Errorf("%w: name %q is taken", ErrConflict, m.Name)
		}
	}
	r.machines[m.ID] = m
	return nil
}

// MachineByKey implements Repository.
func (r *MemRepo) MachineByKey(_ context.Context, keyHash string) (Machine, error) {
	u, err := r.lock()
	if err != nil {
		return Machine{}, err
	}
	defer u()
	id, ok := r.keys[keyHash]
	if !ok || r.revoked[keyHash] {
		return Machine{}, ErrUnauthenticated
	}
	m := r.machines[id]
	if !m.RemovedAt.IsZero() {
		return Machine{}, ErrUnauthenticated
	}
	return m, nil
}

// RevokeKeys implements Repository.
func (r *MemRepo) RevokeKeys(_ context.Context, machineID string) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	for h, id := range r.keys {
		if id == machineID {
			r.revoked[h] = true
		}
	}
	return nil
}

// MarkRemoved implements Repository.
func (r *MemRepo) MarkRemoved(_ context.Context, machineID string, at time.Time) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	m, ok := r.machines[machineID]
	if !ok {
		return ErrNotFound
	}
	m.State, m.RemovedAt, m.Online = StateRemoving, at, false
	r.machines[machineID] = m
	return nil
}

// GetSettings implements Repository.
func (r *MemRepo) GetSettings(_ context.Context, id string) (Settings, error) {
	u, err := r.lock()
	if err != nil {
		return Settings{}, err
	}
	defer u()
	s, ok := r.settings[id]
	if !ok {
		return Settings{}, ErrNotFound
	}
	return s, nil
}

// PutSettings implements Repository.
func (r *MemRepo) PutSettings(_ context.Context, id string, s Settings) error {
	u, err := r.lockW(false)
	if err != nil {
		return err
	}
	defer u()
	if _, ok := r.machines[id]; !ok {
		return ErrNotFound
	}
	r.settings[id] = s
	return nil
}

// InsertJoinCode implements Repository.
func (r *MemRepo) InsertJoinCode(_ context.Context, c JoinCode) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	r.codes[c.ID] = c
	return nil
}

// GetJoinCode implements Repository.
func (r *MemRepo) GetJoinCode(_ context.Context, id string) (JoinCode, error) {
	u, err := r.lock()
	if err != nil {
		return JoinCode{}, err
	}
	defer u()
	c, ok := r.codes[id]
	if !ok {
		return JoinCode{}, ErrNotFound
	}
	return c, nil
}

// FindJoinCodeByHash implements Repository.
func (r *MemRepo) FindJoinCodeByHash(_ context.Context, hash string) (JoinCode, error) {
	u, err := r.lock()
	if err != nil {
		return JoinCode{}, err
	}
	defer u()
	for _, c := range r.codes {
		if c.Hash == hash {
			return c, nil
		}
	}
	return JoinCode{}, ErrNotFound
}

// RevokeOpenJoinCodes implements Repository.
func (r *MemRepo) RevokeOpenJoinCodes(_ context.Context, at time.Time) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	for id, c := range r.codes {
		if c.Kind == CodeKindCode && c.UsedAt.IsZero() && c.RevokedAt.IsZero() {
			c.RevokedAt = at
			r.codes[id] = c
		}
	}
	return nil
}

// RevokeJoinCode implements Repository.
func (r *MemRepo) RevokeJoinCode(_ context.Context, id string, at time.Time) (bool, error) {
	u, err := r.lockW(true)
	if err != nil {
		return false, err
	}
	defer u()
	c, ok := r.codes[id]
	if !ok || !c.UsedAt.IsZero() || !c.RevokedAt.IsZero() {
		return false, nil
	}
	c.RevokedAt = at
	r.codes[id] = c
	return true, nil
}

// UseJoinCode implements Repository.
func (r *MemRepo) UseJoinCode(_ context.Context, hash, machineID string, now time.Time) (bool, error) {
	u, err := r.lockW(true)
	if err != nil {
		return false, err
	}
	defer u()
	for id, c := range r.codes {
		if c.Hash == hash {
			if c.State(now) != CodeWaiting {
				return false, nil
			}
			c.UsedAt, c.MachineID = now, machineID
			r.codes[id] = c
			return true, nil
		}
	}
	return false, nil
}

// InsertLoad implements Repository.
func (r *MemRepo) InsertLoad(_ context.Context, id string, p LoadPoint) error {
	u, err := r.lock()
	if err != nil {
		return err
	}
	defer u()
	for _, o := range r.load[id] {
		if o.At.Equal(p.At) {
			return nil
		}
	}
	r.load[id] = append(r.load[id], p)
	return nil
}

// ListLoad implements Repository.
func (r *MemRepo) ListLoad(_ context.Context, id string, from, to time.Time) ([]LoadPoint, error) {
	u, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer u()
	var out []LoadPoint
	for _, p := range r.load[id] {
		if !p.At.Before(from) && !p.At.After(to) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// DeleteLoadBefore implements Repository.
func (r *MemRepo) DeleteLoadBefore(_ context.Context, t time.Time) error {
	u, err := r.lock()
	if err != nil {
		return err
	}
	defer u()
	for id, ps := range r.load {
		kept := ps[:0]
		for _, p := range ps {
			if !p.At.Before(t) {
				kept = append(kept, p)
			}
		}
		r.load[id] = kept
	}
	return nil
}

// InsertChange implements Repository.
func (r *MemRepo) InsertChange(_ context.Context, c Change) error {
	u, err := r.lockW(false)
	if err != nil {
		return err
	}
	defer u()
	r.changes[c.ID] = c
	return nil
}

// GetChange implements Repository.
func (r *MemRepo) GetChange(_ context.Context, machineID, id string) (Change, error) {
	u, err := r.lock()
	if err != nil {
		return Change{}, err
	}
	defer u()
	c, ok := r.changes[id]
	if !ok || c.MachineID != machineID {
		return Change{}, ErrNotFound
	}
	return c, nil
}

// UpdateChange implements Repository.
func (r *MemRepo) UpdateChange(_ context.Context, c Change) error {
	u, err := r.lockW(false)
	if err != nil {
		return err
	}
	defer u()
	if _, ok := r.changes[c.ID]; !ok {
		return ErrNotFound
	}
	r.changes[c.ID] = c
	return nil
}

func isOpen(c Change) bool {
	return c.State == ChangeWaiting || c.State == ChangeRunning || c.State == ChangeNeedsYou
}

// OpenChange implements Repository.
func (r *MemRepo) OpenChange(_ context.Context, machineID string) (Change, bool, error) {
	u, err := r.lock()
	if err != nil {
		return Change{}, false, err
	}
	defer u()
	var best Change
	found := false
	for _, c := range r.changes {
		if c.MachineID == machineID && isOpen(c) && (!found || c.StartedAt.After(best.StartedAt)) {
			best, found = c, true
		}
	}
	return best, found, nil
}

// OpenChanges implements Repository.
func (r *MemRepo) OpenChanges(_ context.Context) ([]Change, error) {
	u, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer u()
	var out []Change
	for _, c := range r.changes {
		if isOpen(c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Audit implements Repository.
func (r *MemRepo) Audit(_ context.Context, actor, action, subject, detail string) error {
	u, err := r.lock()
	if err != nil {
		return err
	}
	defer u()
	r.audit = append(r.audit, AuditEntry{At: r.Now(), Actor: actor, Action: action, Subject: subject, Detail: detail})
	return nil
}

// AuditLog returns the audit entries so far (tests).
func (r *MemRepo) AuditLog() []AuditEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]AuditEntry(nil), r.audit...)
}

// KeyHashes returns every stored key hash (tests: no key is stored in clear, R6.2).
func (r *MemRepo) KeyHashes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for h := range r.keys {
		out = append(out, h)
	}
	return out
}

// CodeHashes returns every stored join code hash (tests).
func (r *MemRepo) CodeHashes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.codes {
		out = append(out, c.Hash)
	}
	return out
}

// keysOf lists the key hashes of a machine.
func (r *MemRepo) keysOf(machineID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for h, id := range r.keys {
		if id == machineID {
			out = append(out, h)
		}
	}
	return out
}

// AddKey gives an existing machine one more live key (a rejoin, OPS59).
func (r *MemRepo) AddKey(_ context.Context, machineID, keyHash string) error {
	u, err := r.lockW(true)
	if err != nil {
		return err
	}
	defer u()
	if _, ok := r.machines[machineID]; !ok {
		return ErrNotFound
	}
	r.keys[keyHash] = machineID
	return nil
}
