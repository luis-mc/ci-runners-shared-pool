package runnercache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Lane folders (caches R5.43). A container runner is new for each job, so its
// caches live outside it, in a lane of its repository on the machine.
const (
	laneTools     = "tools"
	laneRust      = "rust-target"
	laneToolCache = "tool-cache"
	laneWine      = "wine"
	laneBrowsers  = "browsers"
	laneLockName  = ".lane.lock"
)

// EnvToolCache is the variable `actions/setup-*` reads for its downloads.
const EnvToolCache = "RUNNER_TOOL_CACHE"

// LaneDirs are the folders of one lane on the host.
type LaneDirs struct {
	Root      string // <cache>/<repository id>/lane-<n>
	Tools     string // CI_TOOLS_DIR
	Rust      string // CARGO_TARGET_DIR
	ToolCache string // RUNNER_TOOL_CACHE
	Wine      string
	Browsers  string
	Number    int
}

// Folders lists the folders to make and mount.
func (d LaneDirs) Folders() []string {
	return []string{d.Tools, d.Rust, d.ToolCache, d.Wine, d.Browsers}
}

// LaneRoot is the folder that holds the lanes of one repository.
func LaneRoot(cacheRoot, repoID string) string { return filepath.Join(cacheRoot, repoID) }

// LaneFor gives the folders of lane n (1 based) of a repository.
func LaneFor(cacheRoot, repoID string, n int) LaneDirs {
	root := filepath.Join(LaneRoot(cacheRoot, repoID), "lane-"+strconv.Itoa(n))
	return LaneDirs{
		Root: root, Number: n,
		Tools: filepath.Join(root, laneTools), Rust: filepath.Join(root, laneRust),
		ToolCache: filepath.Join(root, laneToolCache), Wine: filepath.Join(root, laneWine),
		Browsers: filepath.Join(root, laneBrowsers),
	}
}

// Errors of the lanes.
var (
	ErrNoFreeLane        = errors.New("every cache lane of this repository is in use")
	ErrLaneBusy          = errors.New("a job holds a cache lane of this repository")
	ErrNeedsConfirmation = errors.New("this deletes the cache lanes of the repository and needs a yes")
)

// Lane is a lane a job holds. Release gives it back.
type Lane struct {
	LaneDirs
	lock string
}

// Release frees the lane. It is safe to call twice.
func (l *Lane) Release() error {
	if l == nil || l.lock == "" {
		return nil
	}
	// On Windows a file another process has open (a waiter reading the
	// holder, a scanner) cannot be removed: the remove fails for a moment.
	// Retry, and keep the lock path on failure so a later Release can try
	// again instead of leaving the lane taken for ever.
	var err error
	for i := 0; i < releaseTries; i++ {
		err = os.Remove(l.lock)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			l.lock = ""
			return nil
		}
		time.Sleep(releaseBackoff)
	}
	return err
}

const (
	releaseTries   = 50
	releaseBackoff = 10 * time.Millisecond
)

// LaneSet says how lanes are taken. Alive says whether the holder written in a
// lock still runs; a lock whose holder is gone is taken over, so a crashed job
// does not keep its lane for ever. A nil Alive treats every holder as alive.
type LaneSet struct {
	CacheRoot string
	RepoID    string
	Count     int // lanes of the repository, default 1
	Alive     func(holder string) bool
}

func (s LaneSet) count() int {
	if s.Count < 1 {
		return 1
	}
	return s.Count
}

// Acquire takes the first free lane, with a lock, and makes its folders. Two
// jobs of one repository never get one lane, so cargo never waits on a build
// lock another job holds. holder names the job. ErrNoFreeLane when all are
// taken. Lanes of other repositories are never touched.
func (s LaneSet) Acquire(holder string) (*Lane, error) {
	if s.CacheRoot == "" || s.RepoID == "" || strings.ContainsAny(s.RepoID, `/\`) || s.RepoID == ".." || s.RepoID == "." {
		return nil, errors.New("a lane needs a cache folder and a plain repository id")
	}
	for n := 1; n <= s.count(); n++ {
		d := LaneFor(s.CacheRoot, s.RepoID, n)
		if err := os.MkdirAll(d.Root, 0o755); err != nil {
			return nil, err
		}
		lock := filepath.Join(d.Root, laneLockName)
		if !s.take(lock, holder) {
			continue
		}
		for _, f := range d.Folders() {
			if err := os.MkdirAll(f, 0o755); err != nil {
				_ = os.Remove(lock)
				return nil, err
			}
		}
		return &Lane{LaneDirs: d, lock: lock}, nil
	}
	return nil, ErrNoFreeLane
}

// AcquireWait is Acquire that waits for a lane until ctx ends.
func (s LaneSet) AcquireWait(ctx context.Context, holder string, poll time.Duration) (*Lane, error) {
	if poll <= 0 {
		poll = time.Second
	}
	for {
		l, err := s.Acquire(holder)
		if !errors.Is(err, ErrNoFreeLane) {
			return l, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// take makes the lock file with O_EXCL; the one that creates it wins.
func (s LaneSet) take(lock, holder string) bool {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.WriteString(holder)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(lock)
				return false
			}
			return true
		}
		if !errors.Is(err, os.ErrExist) {
			return false
		}
		if s.Alive == nil {
			return false // every holder is alive: do not open the lock file
		}
		b, rerr := os.ReadFile(lock)
		if rerr != nil || s.Alive(strings.TrimSpace(string(b))) {
			return false
		}
		// The holder is gone: take the lane over once.
		if os.Remove(lock) != nil {
			return false
		}
	}
	return false
}

// LaneVars are the only values a job reads from its lane (caches R5.44):
// CI_TOOLS_DIR and CARGO_TARGET_DIR, as the paths the job sees (mount
// targets), and RUNNER_TOOL_CACHE for `actions/setup-*`.
func LaneVars(inside LaneDirs) map[string]string {
	return map[string]string{
		EnvToolsDir:  inside.Tools,
		EnvRustDir:   inside.Rust,
		EnvToolCache: inside.ToolCache,
	}
}

// LaneCaps are the quota-bound folders of a lane with their quota in bytes:
// the same quotas and the same oldest-first eviction as a persistent runner.
func LaneCaps(d LaneDirs, q Quota) []Cap {
	q = q.WithDefaults()
	return []Cap{
		{Name: "tool cache", Dir: d.Tools, Bytes: q.ToolBytes()},
		{Name: "Rust build cache", Dir: d.Rust, Bytes: q.RustBytes()},
	}
}

// SweepLane evicts the lane's caches under their quotas. A lane that a job
// holds is busy: nothing is removed.
func SweepLane(fsys FS, d LaneDirs, q Quota, busy bool) (SweepResult, error) {
	caps := LaneCaps(d, q)
	t, err := Enforce(fsys, caps[0].Dir, caps[0].Bytes, busy)
	if err != nil {
		return SweepResult{Tools: t}, err
	}
	r, err := Enforce(fsys, caps[1].Dir, caps[1].Bytes, busy)
	return SweepResult{Tools: t, Rust: r}, err
}

// LaneHeld says whether a lane has a live lock.
func (s LaneSet) laneHeld(n int) bool {
	lock := filepath.Join(LaneFor(s.CacheRoot, s.RepoID, n).Root, laneLockName)
	if s.Alive == nil {
		_, err := os.Stat(lock)
		return err == nil
	}
	b, err := os.ReadFile(lock)
	if err != nil {
		return false
	}
	return s.Alive(strings.TrimSpace(string(b)))
}

// LanesOnDisk counts the lane folders a repository has now.
func LanesOnDisk(cacheRoot, repoID string) int {
	es, err := os.ReadDir(LaneRoot(cacheRoot, repoID))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if e.IsDir() && strings.HasPrefix(e.Name(), "lane-") {
			n++
		}
	}
	return n
}

// RemoveRepositoryLanes deletes every lane of a repository, after a yes. A
// lane a job holds is never deleted (ErrLaneBusy). Lanes of other repositories
// stay. Removing a runner leaves lanes alone; only removing the repository
// from the pool calls this.
func (s LaneSet) RemoveRepositoryLanes(yes bool) (int64, error) {
	if !yes {
		return 0, ErrNeedsConfirmation
	}
	n := LanesOnDisk(s.CacheRoot, s.RepoID)
	for i := 1; i <= n; i++ {
		if s.laneHeld(i) {
			return 0, fmt.Errorf("lane %d: %w", i, ErrLaneBusy)
		}
	}
	root := LaneRoot(s.CacheRoot, s.RepoID)
	es, err := Scan(OSFS{}, root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	freed := Total(es)
	return freed, os.RemoveAll(root)
}

// LaneTotal is the bytes all lanes of all repositories hold under cacheRoot,
// for the disk guard of the machine (caches R5.38).
func LaneTotal(cacheRoot string) int64 {
	es, err := Scan(OSFS{}, cacheRoot)
	if err != nil {
		return 0
	}
	return Total(es)
}
