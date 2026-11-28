package agent

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent/install"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// depsManager lets one dependency job run at a time on a machine and stops it.
type depsManager struct {
	mu     sync.Mutex
	jobID  string
	cancel context.CancelFunc
}

// begin takes the single slot; ok is false while another job runs.
func (m *depsManager) begin(jobID string, cancel context.CancelFunc) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return false
	}
	m.jobID, m.cancel = jobID, cancel
	return true
}

func (m *depsManager) end() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.jobID, m.cancel = "", nil
}

// stop asks the running job with this id to stop at its next safe point.
func (m *depsManager) stop(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil || m.jobID != jobID {
		return false
	}
	m.cancel()
	return true
}

func (l *link) sendDepsEvent(e *agentv1.DepsEvent) {
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_DepsEvent{DepsEvent: e}})
}

// depsRunner builds the installer of this machine for one runner choice.
func (l *link) depsRunner(runnerOS, runnerKind string) *install.Runner {
	sys := l.cfg.sys()
	osys := install.NewOSSystem(install.OSOptions{
		Admin:      Elevated,
		ActiveJobs: func() int { return len(l.cfg.Gate.Running()) },
	})
	return install.New(install.Config{
		Sys:    osys,
		Pinned: depsetup.MustLoad(),
		Emit:   l.sendDepsEvent,
		State: func(_ context.Context, id string) string {
			return depsItemState(sys, runnerOS, runnerKind, id)
		},
		Engine:            func(context.Context) install.EngineInfo { return depsEngine(sys) },
		VirtualizationOff: func() bool { return virtualizationOff(sys) },
		OSRelease: func() string {
			s, _ := sys.ReadFile("/etc/os-release")
			return s
		},
		FromController: l.fromController,
		Prepare:        l.preparePool(runnerOS, runnerKind),
		Redact:         l.cfg.redactor().Redact,
	})
}

// depFilesPath is where the controller serves its stored dependencies on the agent link (internal/depstore.PathPrefix).
const depFilesPath = "/dep-files/"

// fromController downloads the controller's copy of a dependency over the pinned
// link (DEP15). A 404 means the controller holds none; the caller then goes to the vendor.
func (l *link) fromController(ctx context.Context, name, dest string, onProgress func(bytes, total int64)) error {
	if l.cfg.Controller == "" || l.pin == "" {
		return errors.New("this agent has no controller address")
	}
	osys := install.NewOSSystem(install.OSOptions{Client: pinnedClient(l.pin)})
	return osys.Download(ctx, "https://"+l.cfg.Controller+depFilesPath+url.PathEscape(name), dest, onProgress)
}

// depsCheck answers DepsCheck with one DepsReport (it reads only).
func (l *link) depsCheck(m *agentv1.DepsCheck) {
	rep := DetectDeps(l.cfg.sys(), m.GetRunnerOs(), m.GetRunnerKind())
	rep.CommandId = m.GetCommandId()
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_DepsReport{DepsReport: rep}})
}

// depsBusy is the final event of a job that could not start.
func (l *link) depsBusy(jobID, name, role, phase string) {
	l.sendDepsEvent(&agentv1.DepsEvent{
		JobId: jobID, Phase: phase, PhaseIndex: 1, PhaseCount: 1, MachineName: name, MachineRole: role,
		MachineOs: l.cfg.sys().GOOS(), Stream: "status", Final: true, Outcome: depsetup.OutcomeFailed,
		Error: "Another install is already running on this machine", Line: "Another install is already running on this machine",
		Reason: "The job did not start.",
	})
}

// depsInstall runs DepsInstall in a goroutine, one job at a time. A DepsInstall
// with empty item_ids and a command_id of "stop:<job_id>" stops that job.
func (l *link) depsInstall(ctx context.Context, m *agentv1.DepsInstall) {
	if len(m.GetItemIds()) == 0 && strings.HasPrefix(m.GetCommandId(), install.StopPrefix) {
		l.deps.stop(strings.TrimPrefix(m.GetCommandId(), install.StopPrefix))
		return
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx)) // a lost link does not cut an install in half
	if !l.deps.begin(m.GetJobId(), cancel) {
		cancel()
		l.depsBusy(m.GetJobId(), m.GetMachineName(), m.GetMachineRole(), "install")
		return
	}
	go func() {
		defer l.deps.end()
		l.depsRunner(m.GetRunnerOs(), m.GetRunnerKind()).Install(jobCtx, m)
	}()
}

// depsUndo removes what a run added, under the same one-job slot.
func (l *link) depsUndo(ctx context.Context, m *agentv1.DepsUndo) {
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if !l.deps.begin(m.GetJobId(), cancel) {
		cancel()
		l.depsBusy(m.GetJobId(), m.GetMachineName(), m.GetMachineRole(), "undo")
		return
	}
	go func() {
		defer l.deps.end()
		l.depsRunner("linux", "").Undo(jobCtx, m)
	}()
}

// depsRestartHost restarts the machine unless a job runs.
func (l *link) depsRestartHost(m *agentv1.DepsRestartHost) {
	go l.depsRunner("linux", "").RestartHost(m.GetJobId(), "", "")
}

// preparePool is DEP9: after Verify, make the image `crsp-pool:<key>` and, for
// a persistent Linux runner on a machine that runs containers in a VM (Windows,
// macOS), the persistent pool container and its two volumes. A persistent
// runner on a Linux machine runs on the host or in a job pool: only the image is
// made. Nil when nothing is to prepare or the agent has no container backend; the
// job then says the image is made when the first runner starts.
func (l *link) preparePool(runnerOS, runnerKind string) func(context.Context) error {
	if runnerOS != "linux" || l.cfg.PreparePool == nil {
		return nil
	}
	sys := l.cfg.sys()
	persistent := runnerKind == "persistent" && sys.GOOS() != "linux"
	return func(ctx context.Context) error {
		return l.cfg.PreparePool(ctx, sys.Arch(), persistent)
	}
}
