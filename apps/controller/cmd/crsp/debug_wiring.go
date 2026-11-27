package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/debugaccess"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// debugTick is how often the controller ends sessions that are due (time
// limit, idle, job ended) and prunes transcripts (debug-access R5.17, R5.18).
const debugTick = 5 * time.Second

// debugJobs reads a job's facts for the debug policy from the Jobs section.
type debugJobs struct{ r jobsdata.Reader }

func (d debugJobs) DebugJob(ctx context.Context, id int64) (debugaccess.Job, bool, error) {
	j, ok, err := d.r.GetJob(ctx, id)
	if err != nil || !ok {
		return debugaccess.Job{}, ok, err
	}
	return debugaccess.Job{ID: j.ID, RepositoryID: j.RepositoryID, Repository: j.Repository, Name: j.Name,
		Running: j.State == jobsdata.Running, Event: j.Event, MachineID: j.MachineID, MachineName: j.MachineName,
		RunnerName: j.RunnerName, Backend: j.Backend, Isolation: j.Isolation}, true, nil
}

// RunningBeside lists the other running jobs of the same machine and runner:
// the jobs that share a repository container (runners R5.27, debug-access R5.7).
func (d debugJobs) RunningBeside(ctx context.Context, j debugaccess.Job) ([]int64, error) {
	open, err := d.r.OpenJobs(ctx)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, o := range open {
		if o.ID != j.ID && o.State == jobsdata.Running && o.MachineID == j.MachineID && o.RunnerName == j.RunnerName {
			out = append(out, o.ID)
		}
	}
	return out, nil
}

// debugRepos reads a repository as the installation knows it.
type debugRepos struct{ s *repos.MemStore }

func (d debugRepos) DebugRepo(ctx context.Context, id int64) (debugaccess.Repo, error) {
	r, err := d.s.GetRepository(ctx, id)
	if errors.Is(err, auth.ErrNotFound) {
		return debugaccess.Repo{}, nil
	}
	if err != nil {
		return debugaccess.Repo{}, err
	}
	return debugaccess.Repo{Known: !r.Archived, FullName: r.FullName, Visibility: r.Visibility}, nil
}

// wireDebugAccess builds the debug access area (docs/features/debug-access,
// CRSP331). No agent can enter a job's environment yet, so Enter stays nil and
// every open is refused with its reason. At start every session still marked
// open is ended as link lost (R6.4): no stream survives a restart.
func wireDebugAccess(ctx context.Context, db *store.Store, logger *slog.Logger, c *controller, jobs jobsdata.Reader, repoStore *repos.MemStore) {
	svc := &debugaccess.Service{DB: db, Jobs: debugJobs{r: jobs}, Repos: debugRepos{s: repoStore}, Redact: secretRedactor.Redact}
	if n, err := svc.Reconcile(ctx, nil); err != nil {
		logger.Warn("debug access: close the sessions left open", "err", err)
	} else if n > 0 {
		logger.Info("debug access: closed sessions left open by the last run", "count", n)
	}
	c.Options.Debug = svc
	wctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(debugTick)
		defer t.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case <-t.C:
				if _, err := svc.Sweep(wctx); err != nil {
					logger.Warn("debug access: sweep", "err", err)
				}
			}
		}
	}()
	c.closers = append(c.closers, func() { stop(); <-done })
}
