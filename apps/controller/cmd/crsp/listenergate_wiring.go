package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/listenergate"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// gateTick is how often the gate's background round runs (stops that failed,
// weekly wakes). A wake is due to the minute, so this is fine enough.
const gateTick = 30 * time.Second

// remoteListener is the agent link's listener command (agentlink.Agent).
type remoteListener interface {
	ControlListener(ctx context.Context, machineID, action, service string, jobAssigned, maintenance bool) (string, error)
}

// listenerService is the gate's Service over the agents: the controller's own
// machine runs the backend of this OS directly, any other machine gets the
// ListenerControl message of the agent link (runners R5.134).
type listenerService struct {
	local  *runners.LocalAgent
	svc    *machines.Service
	remote remoteListener // nil when the link is not wired
}

var errNoListenerLink = errors.New("this machine is not the controller's own and the agent link is not wired")

func (s listenerService) control(ctx context.Context, machineID, action, ref string, assigned, maintenance bool) (listenergate.ListenerState, error) {
	v, err := s.svc.Get(ctx, machineID)
	if err != nil {
		return listenergate.ListenerUnknown, err
	}
	if v.Machine.IsController {
		st, err := s.local.ControlListener(ctx, action, ref, assigned, maintenance)
		return listenergate.ListenerState(st), err
	}
	if s.remote == nil {
		return listenergate.ListenerUnknown, errNoListenerLink
	}
	st, err := s.remote.ControlListener(ctx, machineID, action, ref, assigned, maintenance)
	return listenergate.ListenerState(st), err
}

func (s listenerService) StartListener(ctx context.Context, machineID, ref string, maintenance bool) error {
	_, err := s.control(ctx, machineID, "start", ref, false, maintenance)
	return err
}

func (s listenerService) StopListener(ctx context.Context, machineID, ref string, jobAssigned bool) error {
	_, err := s.control(ctx, machineID, "stop", ref, jobAssigned, false)
	return err
}

func (s listenerService) ListenerState(ctx context.Context, machineID, ref string) (listenergate.ListenerState, error) {
	return s.control(ctx, machineID, "status", ref, false, false)
}

// gatedRunners lists the persistent runners that run on a machine itself: the
// persistent backend (not a container, a WSL2 distro or a VM), made by crsp, not
// a pool runner, and clean (runners R5.134). Only these are offered the gate.
type gatedRunners struct {
	svc      *runners.Service
	resolver runners.Resolver
}

// OnMachineItself says whether a runner record is gated by where it runs.
func onMachineItself(r runners.Record) bool {
	return r.Kind == "persistent" && r.Backend == "persistent" && r.Managed && r.Scope.Kind != runners.ScopePool &&
		r.ServiceName != "" && !strings.Contains(r.ServiceName, ":") && r.GitHubRunnerID != 0 && r.UncleanReason == "" && r.State != runners.Failed && r.State != runners.Removing
}

func (g gatedRunners) Gated(ctx context.Context) ([]listenergate.Runner, error) {
	recs, err := g.svc.List(ctx, runners.Filter{})
	if err != nil {
		return nil, err
	}
	var out []listenergate.Runner
	for _, r := range recs {
		if !onMachineItself(r) {
			continue
		}
		t, err := g.resolver.Resolve(ctx, r.Scope)
		if err != nil {
			continue // a scope that cannot be resolved now: the next round tries again
		}
		out = append(out, listenergate.Runner{ID: r.ID, Name: r.Name, MachineID: r.MachineID, OS: r.OS, Labels: r.Labels,
			ServiceRef: r.ServiceName, GitHubRunnerID: r.GitHubRunnerID, InstallationID: t.InstallationID, Scope: t.Scope})
	}
	return out, nil
}

// gateAudit writes the gate's rows to the audit log (system actor).
type gateAudit struct{ r audit.Recorder }

func (a gateAudit) Record(ctx context.Context, e listenergate.Event) error {
	target := audit.Target{Type: "runner", ID: e.Runner}
	details := map[string]any{"summary": e.Detail, "runner": e.Runner}
	if e.JobID != 0 {
		details["job_id"] = e.JobID
	}
	if e.Label != "" {
		details["label"] = e.Label
	}
	return a.r.Record(ctx, audit.System, e.Action, target, details)
}

// assignGate adapts the gate to assign.Gate: the controller offers an admitted
// job, and the gate picks a runner of that machine (runners R5.134).
type assignGate struct{ g *listenergate.Gate }

func (a assignGate) Admit(ctx context.Context, j assign.GateJob) (assign.GateRunner, bool, error) {
	r, ok, err := a.g.Pick(ctx, j.MachineID, j.OS, j.Labels, runners.IsRoutingLabel)
	if err != nil || !ok {
		return assign.GateRunner{}, false, err
	}
	taken, err := a.g.Admit(ctx, listenergate.Job{JobID: j.JobID, JobLabel: j.JobLabel, Runner: r})
	if err != nil || !taken {
		return assign.GateRunner{}, false, err
	}
	return assign.GateRunner{Name: r.Name}, true, nil
}

func (a assignGate) Holds(ctx context.Context, id int64) bool { return a.g.Holds(ctx, id) }

func (a assignGate) Done(ctx context.Context, id int64) error { return a.g.Completed(ctx, id) }

// wireListenerGate builds the listener gate (runners R5.134 to R5.140): the gate
// over the labels API, the agents and the store, its API service, the
// assign.Gate adapter and the background round. It starts in observe mode
// (setting enforce false): it writes would_start and would_stop rows and changes
// no listener. The adapter is what assign.Controller.Gate takes when that
// controller is wired.
func wireListenerGate(ctx context.Context, db *store.Store, logger *slog.Logger, c *controller, gh *github.REST,
	local *runners.LocalAgent, svc *machines.Service, remote remoteListener, runnerSvc *runners.Service) (*listenergate.Control, assign.Gate) {
	ctl := &listenergate.Control{Settings: listenergate.SettingsStore{DB: db}}
	g := &listenergate.Gate{
		Settings: ctl.Current,
		Store:    listenergate.SQLStore{DB: db},
		Labels:   gh,
		Service:  listenerService{local: local, svc: svc, remote: remote},
		Runners:  gatedRunners{svc: runnerSvc, resolver: runnerSvc.Resolver},
		Audit:    gateAudit{r: audit.New(db)},
	}
	ctl.G = g
	c.Options.ListenerGate = ctl
	wctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(gateTick)
		defer t.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case <-t.C:
				if err := g.Sweep(wctx); err != nil {
					logger.Warn("listener gate: round", "err", err)
				}
			}
		}
	}()
	c.closers = append(c.closers, func() { stop(); <-done })
	return ctl, assignGate{g: g}
}
