// Package agentlink carries the runner commands of the controller to a remote
// machine's agent over the agent link, and keeps the machine reports the agents
// send back (runners R5.1, R5.25, R5.27; machines R5.53; doctor R5.32, R5.36).
//
// A command gets a command id, goes down the machine's open Session, and waits
// for the RunnerResult with the same id. The result is what the agent says
// happened; nothing is assumed. A JIT config or token travels in the message
// once and is never kept, logged or put in an error here.
package agentlink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// Sender sends a message down a machine's open stream (machines.Service).
type Sender interface {
	Send(machineID string, msg *agentv1.SessionResponse) error
}

// DefaultTimeout is how long a command waits for its answer. An add may build
// an image first, so it is long (runners R6.5).
const DefaultTimeout = 15 * time.Minute

// ErrNoAnswer is a command the agent did not answer in time.
var ErrNoAnswer = errors.New("the agent did not answer in time")

// Link is the controller's side of the runner commands and machine reports.
type Link struct {
	Sender  Sender
	Timeout time.Duration
	// Now is a seam for tests.
	Now func() time.Time

	// Hygiene takes the clean-up reports of persistent runners and answers
	// with their settings (runners R5.90, R5.91). Nil ignores them.
	Hygiene HygieneSink

	// Registry answers the image registry calls (protocol version 3, REG10).
	// Nil answers "not in the registry" with no staging name (registry.go).
	Registry RegistrySink

	// Builds keeps the build reports of agents as build records (IMG1, R5.68).
	// Nil drops them.
	Builds   builds.Sink
	buildIDs map[string]string // machine id + agent build id -> record id

	// Deps takes the dependency setup events and the returning agents (CRSP248, deps.go). Nil ignores them.
	Deps DepsSink

	mu      sync.Mutex
	pending map[string]chan *agentv1.RunnerResult
	// pendingDeps waits for DepsReport answers (deps.go).
	pendingDeps map[string]chan *agentv1.DepsReport
	// pendingLO waits for RunnerLeftoversReport answers (OPS33).
	pendingLO map[string]chan *agentv1.RunnerLeftoversReport
	reports   map[string]Report
}

// Report is a machine's last MachineReport and when the controller got it.
type Report struct {
	At time.Time
	*agentv1.MachineReport
}

// New returns a Link that sends through s.
func New(s Sender) *Link {
	return &Link{Sender: s, pending: map[string]chan *agentv1.RunnerResult{}, pendingLO: map[string]chan *agentv1.RunnerLeftoversReport{}, reports: map[string]Report{}}
}

var _ runners.Agent = (*Agent)(nil)

// Agent is the runners.Agent of one remote machine set: every call names the
// machine, and the link sends it to that machine's agent.
type Agent struct{ L *Link }

func (l *Link) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func newCommandID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "cmd_" + hex.EncodeToString(b[:])
}

// OnHello asks a machine that just connected for its report.
func (l *Link) OnHello(machineID string) {
	_ = l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_ReportMachine{ReportMachine: &agentv1.ReportMachine{}}})
}

// OnMessage takes the runner results and machine reports. It returns true when
// it handled the message.
func (l *Link) OnMessage(machineID string, msg *agentv1.SessionRequest) bool {
	if l.onRegistry(machineID, msg) {
		return true
	}
	if l.onDeps(machineID, msg) {
		return true
	}
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionRequest_RunnerResult:
		l.mu.Lock()
		ch := l.pending[m.RunnerResult.GetCommandId()]
		l.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m.RunnerResult:
			default:
			}
		}
		return true
	case *agentv1.SessionRequest_RunnerClean:
		l.onRunnerClean(machineID, m.RunnerClean)
		return true
	case *agentv1.SessionRequest_RunnerLeftoversReport:
		l.mu.Lock()
		ch := l.pendingLO[m.RunnerLeftoversReport.GetCommandId()]
		l.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m.RunnerLeftoversReport:
			default:
			}
		}
		return true
	case *agentv1.SessionRequest_MachineReport:
		l.mu.Lock()
		l.reports[machineID] = Report{At: l.now(), MachineReport: m.MachineReport}
		l.mu.Unlock()
		l.afterReport(machineID, m.MachineReport)
		return true
	}
	return false
}

// Reports returns the last report of each machine that sent one.
func (l *Link) Reports() map[string]Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]Report, len(l.reports))
	for k, v := range l.reports {
		out[k] = v
	}
	return out
}

// Forget drops a machine's report (the machine was removed).
func (l *Link) Forget(machineID string) {
	l.mu.Lock()
	delete(l.reports, machineID)
	l.mu.Unlock()
}

// call sends the message built with the command id and waits for the result.
// The returned error holds the agent's redacted text and never the message.
func (l *Link) call(ctx context.Context, machineID string, build func(id string) *agentv1.SessionResponse) (*agentv1.RunnerResult, error) {
	return l.callWait(ctx, machineID, 0, build)
}

// callWait is call with its own wait; zero takes the link's timeout.
func (l *Link) callWait(ctx context.Context, machineID string, wait time.Duration, build func(id string) *agentv1.SessionResponse) (*agentv1.RunnerResult, error) {
	id := newCommandID()
	ch := make(chan *agentv1.RunnerResult, 1)
	l.mu.Lock()
	l.pending[id] = ch
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.pending, id); l.mu.Unlock() }()
	if err := l.Sender.Send(machineID, build(id)); err != nil {
		return nil, fmt.Errorf("the machine's agent is not connected: %w", err)
	}
	if wait <= 0 {
		wait = l.Timeout
	}
	if wait <= 0 {
		wait = DefaultTimeout
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case r := <-ch:
		if !r.GetOk() || r.GetOutcome() == "failed" {
			text := r.GetError()
			if text == "" {
				text = "the agent reported a failure without a reason"
			}
			return r, errors.New(text)
		}
		return r, nil
	case <-t.C:
		return nil, ErrNoAnswer
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AddRunner asks the agent to start a runner. A JIT config makes an ephemeral
// container runner; a registration token a persistent one.
func (a *Agent) AddRunner(ctx context.Context, machineID string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	kind, be := "persistent", string(backend.Persistent)
	if cfg.JITConfig != "" {
		kind, be = "ephemeral", string(backend.Docker)
	} else if spec.Container {
		be = string(backend.Docker) // a persistent Linux runner in the pool container (runners R5.93)
	}
	r, err := a.L.call(ctx, machineID, func(id string) *agentv1.SessionResponse {
		add := &agentv1.AddRunner{CommandId: id, Runner: &agentv1.RunnerSpec{
			RunnerId: spec.ID, Name: spec.Name, Os: spec.OS, Arch: spec.Arch, Kind: kind, Backend: be,
			Labels: spec.Labels, GithubUrl: spec.GitHubURL, RunnerGroup: spec.Group,
			RunnerVersion: spec.RunnerVersion, RunnerSha256: spec.RunnerSHA256, WindowsSymlinkRight: spec.WindowsSymlinkRight,
			ExtraPackages: spec.ExtraPackages, KeepDependencies: &spec.KeepDependencies,
		}}
		if cfg.JITConfig != "" {
			add.Start = &agentv1.AddRunner_JitConfig{JitConfig: cfg.JITConfig}
		} else {
			add.Start = &agentv1.AddRunner_Registration{Registration: cfg.Registration}
		}
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_AddRunner{AddRunner: add}}
	})
	if err != nil {
		return backend.Handle{}, err
	}
	return backend.Handle{RunnerID: spec.ID, Ref: r.GetServiceRef()}, nil
}

// StopService stops a runner's service without removing anything.
func (a *Agent) StopService(ctx context.Context, machineID, service string) error {
	return a.stop(ctx, machineID, service, false)
}

// listenerWait is how long a listener command may take: a clean stop waits for
// the listener to exit by itself.
const listenerWait = 3 * time.Minute

// ControlListener starts, stops or reads the listener of a gated persistent
// runner (runners R5.134). action is start, stop or status. It returns the
// listener state the agent saw after the step: offline, online or busy.
func (a *Agent) ControlListener(ctx context.Context, machineID, action, service string, jobAssigned, maintenance bool) (string, error) {
	r, err := a.L.callWait(ctx, machineID, listenerWait, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_ListenerControl{ListenerControl: &agentv1.ListenerControl{
			CommandId: id, ServiceName: service, Action: action, JobAssigned: jobAssigned, Maintenance: maintenance}}}
	})
	if err != nil {
		return "", err
	}
	return r.GetListener(), nil
}

// CleanUp deletes the service entry, the account and the folder of a runner.
func (a *Agent) CleanUp(ctx context.Context, machineID, service string) error {
	return a.stop(ctx, machineID, service, true)
}

func (a *Agent) stop(ctx context.Context, machineID, service string, cleanUp bool) error {
	_, err := a.L.call(ctx, machineID, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_StopRunner{StopRunner: &agentv1.StopRunner{CommandId: id, ServiceName: service, CleanUp: cleanUp}}}
	})
	return err
}

// RemoveRunner deregisters the runner with the remove token on the agent.
func (a *Agent) RemoveRunner(ctx context.Context, machineID string, req runners.RemoveRequest) error {
	_, err := a.L.call(ctx, machineID, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RemoveRunner{RemoveRunner: &agentv1.RemoveRunner{
			CommandId: id, RunnerId: req.RunnerID, ServiceName: req.ServiceName, Removal: req.RemoveToken, DeleteFolder: !req.KeepFiles,
		}}}
	})
	return err
}

// DrainRunner asks the agent to stop one runner taking new jobs.
func (a *Agent) DrainRunner(ctx context.Context, machineID, runnerID string) error {
	_, err := a.L.call(ctx, machineID, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DrainRunner{DrainRunner: &agentv1.DrainRunner{CommandId: id, RunnerId: runnerID}}}
	})
	return err
}

// HygieneSink is the controller side of the clean-up of persistent runners
// (runners.Service satisfies it).
type HygieneSink interface {
	ReportClean(ctx context.Context, machineID, runnerName string, cacheBytes int64, uncleanReason string) error
	KeepDependencies(ctx context.Context, machineID, runnerName string) bool
	ClearCachePending(machineID, runnerName string) bool
}

// onRunnerClean records what the agent read from each runner folder and answers
// each runner with its settings: the keep setting, and a Clear now that is
// waiting. The agent clears when the runner is idle and reports again, which
// ends the pending flag (runners R5.90).
func (l *Link) onRunnerClean(machineID string, rc *agentv1.RunnerClean) {
	if l.Hygiene == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, st := range rc.GetRunners() {
		name := st.GetRunnerName()
		pending := l.Hygiene.ClearCachePending(machineID, name)
		// A report taken while a Clear now waits does not end it: the agent has
		// not cleared yet.
		if !pending {
			_ = l.Hygiene.ReportClean(ctx, machineID, name, st.GetDependencyCacheBytes(), st.GetUncleanReason())
		}
		_ = l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RunnerHygiene{RunnerHygiene: &agentv1.RunnerHygiene{
			RunnerName: name, ServiceName: st.GetServiceName(),
			KeepDependencies: l.Hygiene.KeepDependencies(ctx, machineID, name),
			ClearCache:       l.Hygiene.ClearCachePending(machineID, name),
		}}})
	}
}
