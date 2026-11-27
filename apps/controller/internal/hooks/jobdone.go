package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// WorkFolder is the job's checkout folder when it is safe to empty: inside a
// _work folder, and not _work itself (R5.29; runner_job_done.py work_folder).
func WorkFolder(workspace string) string {
	if workspace == "" {
		return ""
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(abs, `\`, "/")), "/")
	hasWork := false
	for _, p := range parts {
		if p == "_work" {
			hasWork = true
		}
	}
	if !hasWork || parts[len(parts)-1] == "_work" {
		return ""
	}
	return abs
}

// EmptyFolder deletes everything inside path and returns how many entries it
// could not remove. The folder itself stays (R5.29).
func EmptyFolder(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	failed := 0
	for _, e := range entries {
		if os.RemoveAll(filepath.Join(path, e.Name())) != nil {
			failed++
		}
	}
	return failed
}

// DoneConfig is everything one run of the job-completed hook needs.
type DoneConfig struct {
	Client    Client
	MachineID string
	Job       scheduler.JobRef
	Workspace string
	KeepWork  bool
	Log       func(string)
	// Caches are the cache folders to bring under their quota between jobs
	// (caches R5.37). CacheFS is the file access for them; nil is the real one.
	Caches  []runnercache.Cap
	CacheFS runnercache.FS
	// Report and Healthy are as in GateConfig (R5.61). Both may be nil.
	Report  func(string)
	Healthy func()
	// Clean is the always-clean rule of a persistent runner. When set it
	// replaces KeepWork: the temporary files of a job are always removed
	// (runners R5.91).
	Clean func() CleanResult
}

// RunJobDone frees every slot of every kind the job holds with the outcome ran
// and empties its work folder. It always returns 0, even on an error, and logs
// the error: a hook that fails here would fail a job that already finished
// (R5.28 to R5.30).
func RunJobDone(ctx context.Context, cfg DoneConfig) (code int) {
	log := func(format string, a ...any) {
		if cfg.Log != nil {
			cfg.Log("runner job done: " + fmt.Sprintf(format, a...))
		}
	}
	defer func() {
		if r := recover(); r != nil {
			log("skipped (%v)", r)
			code = 0
		}
	}()
	if cfg.Client != nil {
		// The decision first, then the call that can be slow (R5.63).
		log("freeing this job's slots (the agent answers within %.0f s or the slots are freed later by the stale rules)", CallTimeout.Seconds())
	}
	if cfg.Client == nil {
		log("agent not reachable: slots are freed later by the stale rules")
		if cfg.Report != nil {
			cfg.Report(unreachableText("job-completed", ErrUnreachable))
		}
	} else {
		machine := cfg.MachineID
		if machine == "" {
			if h, err := cfg.Client.Hello(ctx); err == nil {
				machine = h.MachineID
			}
		}
		freed, err := cfg.Client.Release(ctx, machine, cfg.Job, nil)
		switch {
		case err != nil:
			log("skipped (%v): slots are freed later by the stale rules", err)
			if cfg.Report != nil {
				cfg.Report(unreachableText("job-completed", err))
			}
		case len(freed) == 0:
			if cfg.Healthy != nil {
				cfg.Healthy()
			}
			log("this job held no slot")
		default:
			if cfg.Healthy != nil {
				cfg.Healthy()
			}
			for _, f := range freed {
				if f.Number > 0 {
					log("freed slot %s of %s run %d job %s", slots.SlotLabel(slots.Kind(f.Kind), f.Number), cfg.Job.Repository, cfg.Job.RunID, cfg.Job.Job)
				}
			}
		}
	}
	if cfg.Clean != nil {
		res := cfg.Clean()
		if why := res.Unclean(); why != "" {
			log("not clean, the runner takes no new job until it is: %s", why)
			if cfg.Report != nil {
				cfg.Report("this runner is not clean (" + why + "), so it takes no new job until it is")
			}
		}
	} else if !cfg.KeepWork {
		if work := WorkFolder(cfg.Workspace); work != "" {
			log("emptying %s", work)
			bad := EmptyFolder(work)
			if bad > 0 {
				log("emptied %s (%d entries stay)", work, bad)
			} else {
				log("emptied %s", work)
			}
		}
	}
	sweepCaches(&cfg, log)
	return 0
}

// sweepCaches evicts the oldest entries of each cache over its quota. The job
// that ran here is done and the runner takes no other job before this hook
// ends, so no job reads the folder now (caches R5.37).
func sweepCaches(cfg *DoneConfig, log func(string, ...any)) {
	SweepCaches(cfg.CacheFS, cfg.Caches, log)
}

// SweepCaches brings each cache under its quota. The Rust build cache has the
// extra rules of caches R5.45: the cap of one key, the key in use kept, the
// incremental folders first. The job-started hook calls it before the job and
// the job-completed hook after it; both run while no job reads the folder.
// An eviction that fails is logged and never fails the hook.
func SweepCaches(fsys runnercache.FS, caches []runnercache.Cap, log func(string, ...any)) {
	if fsys == nil {
		fsys = runnercache.OSFS{}
	}
	for _, c := range caches {
		log("%s %s: checking the quota", c.Name, c.Dir)
		if c.Rust {
			res, err := runnercache.EnforceRust(fsys, c.Dir, runnercache.RustLimits{Total: c.Bytes, Key: c.KeyBytes}, c.Keep, false)
			for _, ln := range res.Lines() {
				log("%s", ln)
			}
			switch {
			case err != nil:
				log("%s %s: eviction stopped (%v)", c.Name, c.Dir, err)
			case len(res.Actions) > 0:
				log("%s over its cap: %d MiB to %d MiB (cap %d MiB, one key %d MiB)",
					c.Name, res.Before>>20, res.After>>20, c.Bytes>>20, c.KeyBytes>>20)
			}
			continue
		}
		res, err := runnercache.Enforce(fsys, c.Dir, c.Bytes, false)
		switch {
		case err != nil:
			log("%s %s: eviction skipped (%v)", c.Name, c.Dir, err)
		case len(res.Removed) > 0:
			log("%s over its quota: removed %d entries, %d MiB to %d MiB (quota %d MiB)",
				c.Name, len(res.Removed), res.Before>>20, res.After>>20, res.Quota>>20)
		}
	}
}
