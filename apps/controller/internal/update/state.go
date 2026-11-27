package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Files the update keeps in its work folder (operations: the data folder's
// updates/). Nothing here is a secret.
const (
	LockFile     = "install.lock"
	ResultFile   = "result.json"
	FailuresFile = "failures.json"
	LogFile      = "update.log"
	PlanFile     = "plan.json"
)

// LockMaxAge is how long an install may run before its lock is taken to be
// dead. msiexec takes minutes; a dead chain must not block the next install
// for hours.
const LockMaxAge = 45 * time.Minute

// ErrInProgress says another install holds the lock.
var ErrInProgress = errors.New("an update is already being installed")

type lockRecord struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Lock is the machine-wide install lock: two installs at once destroy each
// other (the comics-organizer lesson of 2026-10-03). It is taken before
// anything is downloaded and held until the install has ended.
type Lock struct{ path string }

// TakeLock takes the lock in dir or returns ErrInProgress. A lock whose pid is
// gone, or older than LockMaxAge, is stale and is taken over. alive may be nil
// (the OS check is used).
func TakeLock(dir string, now time.Time, pid int, alive func(int) bool) (*Lock, error) {
	if alive == nil {
		alive = pidAlive
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, LockFile)
	rec, _ := json.Marshal(lockRecord{PID: pid, StartedAt: now})
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is the update work folder
		if err == nil {
			_, werr := f.Write(append(rec, '\n'))
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return &Lock{path: path}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if attempt > 0 || lockLive(path, now, alive) {
			return nil, ErrInProgress
		}
		_ = os.Remove(path) // stale
	}
}

func lockLive(path string, now time.Time, alive func(int) bool) bool {
	b, err := os.ReadFile(path) //nolint:gosec // path is the update work folder
	if err != nil {
		return false
	}
	var r lockRecord
	if json.Unmarshal(b, &r) != nil || now.Sub(r.StartedAt) > LockMaxAge {
		return false
	}
	return alive(r.PID)
}

// HandOver gives the lock to the helper process that now waits for the install.
func (l *Lock) HandOver(pid int, now time.Time) error {
	rec, _ := json.Marshal(lockRecord{PID: pid, StartedAt: now})
	return os.WriteFile(l.path, append(rec, '\n'), 0o600)
}

// Release removes the lock.
func (l *Lock) Release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}

// Result is what the helper reports after an install ended. The controller
// reads it on start, so the console can say how the last update went.
type Result struct {
	OK    bool   `json:"ok"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	To    string `json:"to"`
	Error string `json:"error,omitempty"`
	// Attempt is the id of the update or restart that ended with this result (CRSP205).
	Attempt string    `json:"attempt,omitempty"`
	At      time.Time `json:"at"`
}

// WriteResult stores the result atomically.
func WriteResult(dir string, r Result) error {
	return writeJSON(filepath.Join(dir, ResultFile), r)
}

// ReadResult reads the last result; ok is false when there is none.
func ReadResult(dir string) (Result, bool) {
	var r Result
	b, err := os.ReadFile(filepath.Join(dir, ResultFile)) //nolint:gosec // work folder
	if err != nil || json.Unmarshal(b, &r) != nil {
		return Result{}, false
	}
	return r, true
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Automatic installs that keep failing back off (the comics-organizer rule):
// after the first failed try of a version the next waits an hour, then four,
// then sixteen, and after three the automatic path gives up on that version.
// The owner's own "Update to <version>" still works. A build that did not
// become healthy is recorded with the full count, so it is never tried again
// on its own (operations R5.17).
const (
	AutoTries     = 3
	AutoFirstWait = time.Hour
)

// Failures is the record of failed automatic tries of one version.
type Failures struct {
	Version  string    `json:"version"`
	Count    int       `json:"count"`
	FailedAt time.Time `json:"failed_at"`
}

// RecordFailure counts one more failed try of version.
func RecordFailure(dir, version string, now time.Time, bad bool) error {
	f, _ := ReadFailures(dir)
	if f.Version == version {
		f.Count++
	} else {
		f = Failures{Version: version, Count: 1}
	}
	if bad {
		f.Count = AutoTries
	}
	f.FailedAt = now
	return writeJSON(filepath.Join(dir, FailuresFile), f)
}

// ReadFailures reads the record; the zero value means no failures.
func ReadFailures(dir string) (Failures, bool) {
	var f Failures
	b, err := os.ReadFile(filepath.Join(dir, FailuresFile)) //nolint:gosec // work folder
	if err != nil || json.Unmarshal(b, &f) != nil {
		return Failures{}, false
	}
	return f, true
}

// AutoAllowed says whether the automatic path may try version now. When it may
// not, why says so in words (waiting until a time, or given up).
func AutoAllowed(f Failures, version string, now time.Time) (ok bool, why string) {
	if f.Version != version || f.Count == 0 {
		return true, ""
	}
	if f.Count >= AutoTries {
		return false, "Automatic update gave up on " + version + " after " + strconv.Itoa(f.Count) + " failed tries. Update by hand from the console."
	}
	wait := AutoFirstWait
	for i := 1; i < f.Count; i++ {
		wait *= 4
	}
	if at := f.FailedAt.Add(wait); at.After(now) {
		return false, fmt.Sprintf("Automatic update of %s failed %d time(s) and waits until %s.", version, f.Count, at.Local().Format("15:04"))
	}
	return true, ""
}
