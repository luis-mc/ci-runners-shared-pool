package machines

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A controller keeps its machines, their key hashes, settings, join codes and
// open changes in one file in the data folder, so a restart of the controller
// leaves the pool as it was: the controller's own agent and every other agent
// keep their keys and never hear "this machine was removed from the pool" for a
// normal restart. Only hashes are stored (R6.2), never a key or a code. Load
// points and the audit trail are not kept here.

// MachinesFile is the snapshot file's name in the data folder.
const MachinesFile = "machines.json"

// saveEvery bounds how often a routine change (a heartbeat's last-seen time) is
// written; a change that grants or takes away access is written at once.
const saveEvery = 2 * time.Second

// Snapshot is the whole pool as one value: the content of machines.json, and
// what a database-backed repository loads at start (OPS59).
type Snapshot struct {
	Machines map[string]Machine  `json:"machines"`
	Settings map[string]Settings `json:"settings"`
	Keys     map[string]string   `json:"keys"`
	Revoked  map[string]bool     `json:"revoked"`
	Codes    map[string]JoinCode `json:"codes"`
	Changes  map[string]Change   `json:"changes"`
}

// OpenFileRepo returns a repository that is saved to path after a change and
// read from it now. A missing file is an empty pool; an unreadable file is an
// error, so a damaged pool is never silently replaced by an empty one.
func OpenFileRepo(path string) (*MemRepo, error) {
	r := NewMemRepo()
	r.path = path
	s, ok, err := ReadSnapshotFile(path)
	if err != nil {
		return nil, err
	}
	if ok {
		r.Restore(s)
	}
	return r, nil
}

// ReadSnapshotFile reads a machines.json; ok is false when the file is missing.
func ReadSnapshotFile(path string) (s Snapshot, ok bool, err error) {
	b, err := os.ReadFile(path) //nolint:gosec // the controller's own data folder
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	return s, true, nil
}

// Restore fills an empty repository from a snapshot. Nobody is connected yet:
// every machine is offline until its session opens.
func (r *MemRepo) Restore(s Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Machines != nil {
		r.machines = s.Machines
	}
	if s.Settings != nil {
		r.settings = s.Settings
	}
	if s.Keys != nil {
		r.keys = s.Keys
	}
	if s.Revoked != nil {
		r.revoked = s.Revoked
	}
	if s.Codes != nil {
		r.codes = s.Codes
	}
	if s.Changes != nil {
		r.changes = s.Changes
	}
	for id, m := range r.machines {
		m.Online = false
		r.machines[id] = m
	}
}

// Snapshot copies the pool as it is now.
func (r *MemRepo) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Snapshot{Machines: cloneMap(r.machines), Settings: cloneMap(r.settings), Keys: cloneMap(r.keys),
		Revoked: cloneMap(r.revoked), Codes: cloneMap(r.codes), Changes: cloneMap(r.changes)}
}

func cloneMap[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// lockW is lock for a call that changes the repository: the unlock saves the
// file, at once when now is true and otherwise at most every saveEvery.
func (r *MemRepo) lockW(now bool) (func(), error) {
	u, err := r.lock()
	if err != nil {
		return nil, err
	}
	if r.path == "" {
		return u, nil
	}
	return func() {
		r.dirty = true
		if now || time.Since(r.saved) >= saveEvery {
			r.save()
		}
		u()
	}, nil
}

// Flush writes a change that is still waiting; call it when the controller stops.
func (r *MemRepo) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.path != "" && r.dirty {
		r.save()
	}
}

// save writes the snapshot atomically; the caller holds r.mu. A failed write
// stays dirty and is tried again at the next change.
func (r *MemRepo) save() {
	b, err := json.Marshal(Snapshot{Machines: r.machines, Settings: r.settings, Keys: r.keys, Revoked: r.revoked, Codes: r.codes, Changes: r.changes})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return
	}
	r.dirty, r.saved = false, time.Now()
}
