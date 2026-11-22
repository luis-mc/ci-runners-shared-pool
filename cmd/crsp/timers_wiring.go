package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/internal/timers"
)

// timerTick is how often the job and run clocks are looked at. A grace of 30 s
// is seen to within this.
const timerTick = 5 * time.Second

// wireTimers builds crsp's job and run timers (scheduling R5.99 to R5.106):
// the engine over the store, the agent's stop-runner command and GitHub's run
// cancel, the scheduler's job start and end, the API service and the tick. The
// queued and start-up timers have their types and settings here; their handlers
// come from the runner assignment code that calls Engine.SweepQueued and
// Engine.SweepStartup.
func wireTimers(ctx context.Context, db *store.Store, logger *slog.Logger, c *controller, queue scheduler.SlotQueue,
	agent timers.ServiceStopper, gh *github.REST, repos *repoLookup, runnerSvc *runners.Service) *timers.Service {
	svc := &timers.Service{Settings: timers.SettingsStore{DB: db}}
	eng := timers.New(timers.Config{
		Settings: svc.Current,
		Clock:    timers.ClockFunc(func() time.Time { return time.Now().UTC() }),
		Agent:    timers.AgentAdapter{S: agent},
		GitHub: timers.GitHubAdapter{C: gh, Installation: func(ctx context.Context, repo string) (int64, error) {
			r, ok := repos.find(ctx, repo)
			if !ok {
				return 0, errNotServed
			}
			return r.InstallationID, nil
		}},
		Audit: timers.AuditAdapter{R: audit.New(db)},
		Store: timers.StoreAdapter{DB: db},
	})
	svc.E = eng
	if err := eng.Restore(ctx); err != nil {
		logger.Warn("timers: open clocks not restored", "err", err)
	}
	if q, ok := queue.(interface{ SetLifecycle(scheduler.Lifecycle) }); ok {
		q.SetLifecycle(&timers.Hook{E: eng,
			RunnerRef: func(machineID, name string) string {
				rs, err := runnerSvc.List(context.Background(), runners.Filter{MachineID: machineID})
				if err == nil {
					for _, r := range rs {
						if r.Name == name && r.ServiceName != "" {
							return r.ServiceName
						}
					}
				}
				return name
			},
			Log: func(msg string, err error) { logger.Warn("timers: "+msg, "err", err) },
		})
	}
	c.Options.Timers = svc
	wctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		eng.Watch(wctx, timerTick, func(err error) { logger.Warn("timers: tick", "err", err) })
	}()
	c.closers = append(c.closers, func() { stop(); <-done })
	return svc
}
