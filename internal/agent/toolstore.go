package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/internal/toolstore"
)

// ToolStoreServes says whether this OS uses the tool store with a private job home (runners R5.110, OPS75):
// Windows and macOS. Linux keeps the image profiles.
func ToolStoreServes(goos string) bool { return goos == toolstore.Windows || goos == toolstore.Darwin }

// ToolPlanSource gives the scan input of a commit: the stack files and the text of .crsp.yml (nil when the
// repository has none). The controller's scan (internal/jobsize, stackscan) is behind it.
type ToolPlanSource interface {
	ToolPlanInput(ctx context.Context, repository, commit string) (files []registry.StackFile, crspYML []byte, err error)
}

// JobToolFlow is the job flow of the tool store (OPS75, steps 1 to 8). The agent owns one per machine.
type JobToolFlow struct {
	Store     *toolstore.Store
	Installer toolstore.Installer
	Cache     toolstore.Cache
	Plans     ToolPlanSource
	// JobsRoot is the folder the job homes are made in.
	JobsRoot string
	Killer   toolstore.TreeKiller
	Identity toolstore.Identity
	// MaxUnused is the housekeeping setting (R5.118); 0 means toolstore.DefaultUnusedDays.
	MaxUnused time.Duration
}

// ToolJob is what the flow needs to know of one job.
type ToolJob struct {
	Repository, Commit, JobID string
	// LockHash is the hash of the repository's lockfiles at the commit (part of the cache key).
	LockHash string
	// BaseEnv is the environment the runner would give the job: its PATH, system folders, runner variables.
	BaseEnv map[string]string
}

// PreparedJob is a job whose tools are in the store and whose home is built.
type PreparedJob struct {
	flow  *JobToolFlow
	Plan  toolstore.Plan
	Home  toolstore.JobHome
	Key   string
	Hit   bool
	Lacks []toolstore.Lack
	// CacheDir is where the restored cache is, inside the job's caches folder; the job's tools may read and
	// write it, and it is saved from here at the end.
	CacheDir string
}

// Env is the job's environment, KEY=VALUE.
func (p *PreparedJob) Env() []string {
	return append(append([]string(nil), p.Home.Env...), "CRSP_CACHE_DIR="+p.CacheDir)
}

// Prepare runs steps 1 to 5: scan to plan, check the store, install what is missing once, build the home and
// restore the cache. An install failure returns the error and no home: the job must not start without its tools.
func (f *JobToolFlow) Prepare(ctx context.Context, j ToolJob) (*PreparedJob, error) {
	if !ToolStoreServes(f.Store.GOOS) {
		return nil, fmt.Errorf("agent: no tool store on %s", f.Store.GOOS)
	}
	files, yml, err := f.Plans.ToolPlanInput(ctx, j.Repository, j.Commit)
	if err != nil {
		return nil, fmt.Errorf("agent: scan for the tool plan: %w", err)
	}
	plan, err := toolstore.BuildPlan(files, yml)
	if err != nil {
		return nil, err // a bad tools: entry fails the job with the file's own words
	}
	if len(f.Store.Missing(plan)) > 0 {
		if err := f.Store.EnsureInstalled(ctx, f.Installer, plan); err != nil {
			return nil, err
		}
	}
	jh, err := f.Store.BuildJobHome(f.JobsRoot, j.JobID, plan, j.BaseEnv)
	if err != nil {
		return nil, err
	}
	p := &PreparedJob{flow: f, Plan: plan, Home: jh, Key: toolstore.CacheKey(f.Store.GOOS, plan, j.LockHash), CacheDir: filepath.Join(jh.Caches, "deps")}
	if f.Cache.Clone != nil {
		if p.Hit, err = f.Cache.Restore(ctx, p.Key, p.CacheDir); err != nil {
			// A cache is a speed-up: a failed restore is a miss, never a failed job.
			p.Hit = false
			_ = removeAllQuiet(p.CacheDir)
		}
	}
	return p, nil
}

// ToolOutcome is how the job ended.
type ToolOutcome struct {
	// PID is the job's process, killed with its whole tree; 0 when it is already gone.
	PID int
	// User is the job's OS user, whose registry hive or keychain is wiped.
	User  string
	Trust toolstore.Trust
}

// Finish runs steps 6 to 8 after the job: it kills the tree, saves the cache when the run is trusted and
// green (a new key only), then wipes the home, temp and the job user's registry or keychain. Wiping always
// runs; an error of the cache save is joined with it.
func (p *PreparedJob) Finish(ctx context.Context, o ToolOutcome) error {
	f := p.flow
	var errs []error
	if o.PID > 0 && f.Killer != nil {
		errs = append(errs, f.Killer.KillTree(ctx, o.PID))
	}
	if !p.Hit && o.Trust.MaySave() && f.Cache.Clone != nil {
		_, err := f.Cache.Save(ctx, p.Key, p.CacheDir, o.Trust)
		errs = append(errs, err)
	}
	errs = append(errs, p.Home.Finish(ctx, nil, 0, f.Identity, o.User))
	return errors.Join(errs...)
}

// ToolPlanReport is what the agent saw of the plan against the store, for the doctor (R5.119): one item per
// tool of the plan. Err is set when the probe could not run.
type ToolPlanReport struct {
	Err   string
	Items []toolstore.ProbeItem
}

// ProbeToolPlan compares the plan of a commit with the store and installs nothing.
func (f *JobToolFlow) ProbeToolPlan(ctx context.Context, repository, commit string) ToolPlanReport {
	files, yml, err := f.Plans.ToolPlanInput(ctx, repository, commit)
	if err != nil {
		return ToolPlanReport{Err: "the tool plan could not be made: " + err.Error()}
	}
	plan, err := toolstore.BuildPlan(files, yml)
	if err != nil {
		return ToolPlanReport{Err: err.Error()}
	}
	return ToolPlanReport{Items: f.Store.Probe(plan)}
}

// Housekeep removes the versions unused for longer than the setting; versions of the plans in use stay.
func (f *JobToolFlow) Housekeep(inUse toolstore.Plan) ([]toolstore.Tool, error) {
	return f.Store.Prune(toolstore.PruneOptions{MaxUnused: f.MaxUnused, InUse: inUse})
}

func removeAllQuiet(p string) error { return os.RemoveAll(p) }
