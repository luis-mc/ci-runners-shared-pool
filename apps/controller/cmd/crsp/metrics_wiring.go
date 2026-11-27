package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/metrics"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// maxMetricJobs bounds the finished jobs one scrape reads.
const maxMetricJobs = 1000

// newMetrics builds the metrics export (observability R5.40 to R5.47). It reads
// the other areas through their public methods only. It is off until the owner
// turns it on in Settings.
func newMetrics(db *store.Store, sec secrets.Store, rs *runners.Service, ms *machines.Service, logger *slog.Logger) *metrics.Service {
	hosted := map[string]bool{}
	for _, l := range repos.DefaultHostedLabels {
		hosted[l] = true
	}
	host, _ := os.Hostname()
	src := &metrics.PoolSource{Started: time.Now(), Feeds: metrics.Feeds{
		RunnerStates: func(ctx context.Context) (map[string]int, error) {
			recs, err := rs.List(ctx, runners.Filter{})
			if err != nil {
				return nil, err
			}
			out := map[string]int{}
			for _, r := range recs {
				out[string(r.State)]++
			}
			return out, nil
		},
		FinishedJobs: func(ctx context.Context, since time.Time) ([]metrics.JobFact, error) {
			rows, _, err := db.Jobs().ListJobs(ctx, jobsdata.JobFilter{State: jobsdata.Done, From: since, Limit: maxMetricJobs})
			if err != nil {
				return nil, err
			}
			out := make([]metrics.JobFact, 0, len(rows))
			for _, j := range rows {
				if j.CompletedAt.Before(since) {
					continue
				}
				f := metrics.JobFact{ID: j.ID, Queued: j.QueuedAt, Started: j.StartedAt, Completed: j.CompletedAt, Cache: j.Cache}
				if f.Queued.IsZero() {
					f.Queued = j.CreatedAt
				}
				if j.MachineID == "" {
					for _, l := range j.Labels {
						if hosted[l] {
							f.Hosted = true
						}
					}
				}
				out = append(out, f)
			}
			return out, nil
		},
		Machines: func(ctx context.Context) ([]metrics.MachineFact, error) {
			views, err := ms.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]metrics.MachineFact, 0, len(views))
			for _, v := range views {
				out = append(out, metrics.MachineFact{Name: v.Machine.Name, OS: v.Machine.OS, Online: v.Machine.Online})
			}
			return out, nil
		},
	}}
	return &metrics.Service{
		Store: metrics.DBStore{DB: db}, Token: metrics.SecretToken{Secrets: sec}, Source: src,
		Version: version, Host: host,
		OnError: func(err error) { logger.Warn("metrics push failed", "err", err) },
	}
}
