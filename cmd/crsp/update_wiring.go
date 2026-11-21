package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// idleHook lets the machines service call the update manager, which is built
// after it. The machines service runs OnIdle when a drain has finished.
type idleHook struct {
	fn atomic.Pointer[func(context.Context, machines.Change) error]
}

func (h *idleHook) call(ctx context.Context, c machines.Change) error {
	if f := h.fn.Load(); f != nil {
		return (*f)(ctx, c)
	}
	return nil
}

// machineDrainer drains the controller's own machine through the machines
// service: no new jobs, running jobs finish, then OnIdle (tray R5.17). It never
// stops a job.
type machineDrainer struct{ svc *machines.Service }

func (d machineDrainer) local(ctx context.Context) (machines.View, error) {
	vs, err := d.svc.List(ctx)
	if err != nil {
		return machines.View{}, err
	}
	for _, v := range vs {
		if v.Machine.IsController {
			return v, nil
		}
	}
	return machines.View{}, errors.New("this machine has not joined the pool yet, so it cannot drain")
}

// policy reads this machine's mode and whether it takes new jobs, for the tray
// (tray R5.42). ok is false when the machine has not joined yet.
func (d machineDrainer) policy(ctx context.Context) (machines.View, bool) {
	if d.svc == nil {
		return machines.View{}, false
	}
	v, err := d.local(ctx)
	return v, err == nil
}

// setPolicy changes this machine's mode or pause as the tray's user. The
// console shows the same change, because both write the machine's settings.
func (d machineDrainer) setPolicy(ctx context.Context, actor string, u machines.SettingsUpdate) error {
	if d.svc == nil {
		return errors.New("this machine has not joined the pool yet")
	}
	v, err := d.local(ctx)
	if err != nil {
		return err
	}
	_, _, err = d.svc.UpdateSettings(ctx, actor, v.Machine.ID, u)
	return err
}

func (d machineDrainer) RunningJobs(ctx context.Context) ([]update.Job, error) {
	v, err := d.local(ctx)
	if err != nil {
		return nil, nil // no machine yet means no job to wait for
	}
	out := make([]update.Job, 0, len(v.RunningJobs))
	for _, j := range v.RunningJobs {
		out = append(out, update.Job{Repository: j.Repository, RunID: j.RunID, Job: j.Job})
	}
	return out, nil
}

func (d machineDrainer) BeginDrain(ctx context.Context, kind update.Kind, actor string) error {
	v, err := d.local(ctx)
	if err != nil {
		return err
	}
	k, reason := machines.KindAgentUpdate, "update to a newer release"
	if kind == update.KindRestart {
		k, reason = machines.KindServiceRestart, "restart the service"
	}
	_, err = d.svc.StartChange(ctx, actor, v.Machine.ID, k, reason)
	return err
}

func (d machineDrainer) CancelDrain(ctx context.Context, actor string) error {
	v, err := d.local(ctx)
	if err != nil {
		return err
	}
	_, err = d.svc.Resume(ctx, actor, v.Machine.ID, true)
	return err
}

func (d machineDrainer) DrainTimeoutMinutes(ctx context.Context) int {
	if v, err := d.local(ctx); err == nil {
		return v.Settings.DrainTimeoutMinutes
	}
	return machines.DefaultSettings().DrainTimeoutMinutes
}

// healthURL is the address the update helper asks after a restart. It is always on
// loopback with the listen port: the controller keeps 127.0.0.1 open next to any other
// address, and a listen address that changed (a new DHCP lease) must not fail a good restart.
func healthURL(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8460"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
}

// startUpdates builds the update manager, binds it to the machines drain and
// starts the 6-hourly check (operations R5.11).
func startUpdates(ctx context.Context, cfg config.Config, logger *slog.Logger, mr *machinesRuntime, set *settings.Service, idle *idleHook, log *audit.Log) (*update.Manager, *update.Service) {
	exe, err := update.ExecutablePath()
	if err != nil {
		logger.Warn("self-update: cannot find the running binary", "err", err)
	}
	key, keyErr := update.ReleaseKey()
	if keyErr != nil {
		// No release key is committed yet: every manifest is refused, so no update installs (a missing key is a missing signature).
		logger.Warn("self-update: no release key; updates are refused until one is committed", "err", keyErr)
	}
	m := update.New(update.Options{
		Key: key, RequireManifest: true,
		Current:  version,
		Source:   update.GitHubSource{Base: cfg.Addresses.GitHubAPIURL},
		Attester: update.SigstoreAttester{APIBase: cfg.Addresses.GitHubAPIURL},
		Drainer:  machineDrainer{svc: mr.Service},
		AutoUpdate: func(ctx context.Context) bool {
			if set == nil {
				return settings.Defaults.AutoUpdate
			}
			s, err := set.Get(ctx)
			return err != nil || s.AutoUpdate
		},
		Dir:       filepath.Join(cfg.DataDir, "updates"),
		HealthURL: healthURL(cfg.ListenAddr),
		Exe:       exe,
		Logger:    logger.With("component", "update"),
		// The MSI ends every tray; after it the service starts the tray again for the signed-in user (CRSP147).
		AfterInstall: func(v string) {
			n, err := tray.StartForSignedInUsers(exe)
			if err != nil {
				logger.Warn("could not start the tray after the update", "version", v, "started", n, "err", err)
				return
			}
			logger.Info("tray started after the update", "version", v, "started", n)
		},
	})
	fn := func(ctx context.Context, c machines.Change) error {
		switch c.Kind {
		case machines.KindAgentUpdate:
			return m.OnIdle(ctx, update.KindUpdate)
		case machines.KindServiceRestart:
			return m.OnIdle(ctx, update.KindRestart)
		}
		return nil
	}
	idle.fn.Store(&fn)
	drainer := machineDrainer{svc: mr.Service}
	svc := update.NewService(ctx, update.ServiceOptions{
		Engine: m, Current: version, Key: key, Dir: filepath.Join(cfg.DataDir, "updates"),
		// No check runs before this machine has joined the pool.
		Paired: func(ctx context.Context) bool { _, ok := drainer.policy(ctx); return ok },
		Idle: func(ctx context.Context) bool {
			jobs, err := drainer.RunningJobs(ctx)
			return err == nil && len(jobs) == 0
		},
		Audit:   lifecycleAudit(log, func(err error) { logger.Warn("update audit write failed", "err", err) }),
		History: lifecycleHistory(log),
		Logger:  logger.With("component", "update-lifecycle"),
	})
	// The state machine checks every 6 h +/- 20%, the first 1 to 10 min after start, and backs off after a failed check.
	go svc.Run(ctx, 15*time.Second)
	// Connected agent machines follow the controller's version (operations R5.39).
	fol := &update.Follower{Fleet: machineFleet{svc: mr.Service}, Current: version, Logger: logger.With("component", "follow")}
	go fol.Run(ctx, 2*time.Minute)
	return m, svc
}

// machineFleet shows the machines service to the follower.
type machineFleet struct{ svc *machines.Service }

func (f machineFleet) Agents(ctx context.Context) ([]update.Agent, error) {
	as, err := f.svc.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]update.Agent, 0, len(as))
	for _, a := range as {
		out = append(out, update.Agent{ID: a.ID, Name: a.Name, Version: a.Version, Updating: a.Update.Active(), UpdatingSince: a.Update.At})
	}
	return out, nil
}

func (f machineFleet) AskUpdate(id, v string) error { return f.svc.AskUpdate(id, v) }
