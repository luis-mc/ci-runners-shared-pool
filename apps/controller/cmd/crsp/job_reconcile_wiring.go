package main

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/collect"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// jobReconcileTTL is how long the repository and runner answers of one pass are reused.
const jobReconcileTTL = time.Minute

// installationLookup answers the installation of a repository id for the reconcile, from one list of the
// repositories read at most once a minute.
type installationLookup struct {
	store repos.Store
	mu    sync.Mutex
	at    time.Time
	byID  map[int64]int64
}

func (l *installationLookup) of(ctx context.Context, repositoryID int64) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil || time.Since(l.at) > jobReconcileTTL {
		rs, err := l.store.ListRepositories(ctx, repos.ListFilter{})
		if err != nil {
			return 0, false
		}
		l.byID = make(map[int64]int64, len(rs))
		for _, r := range rs {
			l.byID[r.ID] = r.InstallationID
		}
		l.at = time.Now()
	}
	id, ok := l.byID[repositoryID]
	return id, ok && id != 0
}

// crspRunnerNames says whether a runner name is one crsp made, from its runner records.
type crspRunnerNames struct {
	list func(context.Context, runners.Filter) ([]runners.Record, error)
	mu   sync.Mutex
	at   time.Time
	set  map[string]bool
}

func (c *crspRunnerNames) IsCrsp(ctx context.Context, name string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set == nil || time.Since(c.at) > jobReconcileTTL {
		rs, err := c.list(ctx, runners.Filter{})
		if err != nil {
			return false, err
		}
		c.set = make(map[string]bool, len(rs))
		for _, r := range rs {
			c.set[strings.ToLower(r.Name)] = true
		}
		c.at = time.Now()
	}
	return c.set[strings.ToLower(name)], nil
}

// startJobReconcile closes the jobs crsp holds as queued or running that GitHub finished or no longer has
// (CRSP392). It does one pass at start and one every five minutes; each read is a background call within the
// insights share, so it never takes the room of webhooks or of a user's call. The returned function stops it.
func startJobReconcile(ctx context.Context, logger *slog.Logger, db *store.Store, gh *github.REST, rs repos.Store,
	list func(context.Context, runners.Filter) ([]runners.Record, error), now func() time.Time) func() {
	ij := db.InsightsJobs()
	log := audit.New(db)
	rec := reconcile.New(reconcile.Config{
		GitHub: gh, Store: ij, Jobs: ij,
		Classify:     reconcile.NewClassifier(&crspRunnerNames{list: list}),
		Budget:       &collect.HourlyBudget{Now: now},
		Installation: (&installationLookup{store: rs}).of,
		Now:          now, Log: logger,
		OnCorrected: func(ctx context.Context, jobID int64, repository, said string) {
			_ = log.Record(ctx, audit.System, "job.reconciled", audit.Target{Type: "job", ID: strconv.FormatInt(jobID, 10)},
				map[string]any{"repository": repository, "github said": said})
		},
	})
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reconcile.Run(rctx, rec, now, logger)
	}()
	return func() { cancel(); <-done }
}
