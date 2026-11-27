package agent

import (
	"context"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// The runner commands of the controller (runners R5.1, R5.25, R5.27). The agent
// runs each in its own goroutine, because an add may build an image, and
// answers with one RunnerResult that says what happened. A token or JIT config
// of the message goes to the redactor first and never to a log or an answer
// (runners R5.11, R6.2).

var errNoRunners = errors.New("this agent has no runner backend")

func (l *link) redactor() *redact.Redactor {
	if l.cfg.Redactor != nil {
		return l.cfg.Redactor
	}
	return redact.New()
}

func (l *link) answer(id, runnerID string, ok bool, outcome, ref string, err error) {
	res := &agentv1.RunnerResult{CommandId: id, Ok: ok, Outcome: outcome, RunnerId: runnerID, ServiceRef: ref}
	if err != nil {
		res.Error = l.redactor().Redact(err.Error())
	}
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: res}})
}

// SpecFromProto converts the wire runner spec.
func SpecFromProto(p *agentv1.RunnerSpec) backend.RunnerSpec {
	return backend.RunnerSpec{
		ID: p.GetRunnerId(), Name: p.GetName(), OS: p.GetOs(), Arch: p.GetArch(), Labels: p.GetLabels(),
		GitHubURL: p.GetGithubUrl(), Group: p.GetRunnerGroup(), RunnerVersion: p.GetRunnerVersion(),
		RunnerSHA256: p.GetRunnerSha256(), WindowsSymlinkRight: p.GetWindowsSymlinkRight(),
		ExtraPackages: p.GetExtraPackages(), KeepDependencies: p.KeepDependencies == nil || p.GetKeepDependencies(),
		Container: p.GetKind() == "persistent" && p.GetBackend() == string(backend.Docker),
	}
}

func (l *link) runnerCmd(ctx context.Context, msg *agentv1.SessionResponse) {
	run := l.cfg.Runners
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionResponse_AddRunner:
		a := m.AddRunner
		cfg := backend.StartConfig{Registration: a.GetRegistration(), JITConfig: a.GetJitConfig()}
		l.redactor().Add(cfg.Registration)
		l.redactor().Add(cfg.JITConfig)
		go func() {
			if run == nil {
				l.answer(a.GetCommandId(), a.GetRunner().GetRunnerId(), false, "failed", "", errNoRunners)
				return
			}
			spec := SpecFromProto(a.GetRunner())
			if err := l.preparePackages(ctx, spec, cfg); err != nil {
				l.answer(a.GetCommandId(), a.GetRunner().GetRunnerId(), false, "failed", "", err)
				return
			}
			h, err := run.AddRunner(ctx, "", spec, cfg)
			if err != nil {
				l.answer(a.GetCommandId(), a.GetRunner().GetRunnerId(), false, "failed", "", err)
				return
			}
			l.answer(a.GetCommandId(), a.GetRunner().GetRunnerId(), true, "done", h.Ref, nil)
		}()
	case *agentv1.SessionResponse_StopRunner:
		s := m.StopRunner
		go func() {
			if run == nil {
				l.answer(s.GetCommandId(), s.GetRunnerId(), false, "failed", "", errNoRunners)
				return
			}
			var err error
			if s.GetCleanUp() {
				err = run.CleanUp(ctx, "", s.GetServiceName())
			} else {
				err = run.StopService(ctx, "", s.GetServiceName())
			}
			l.answer(s.GetCommandId(), s.GetRunnerId(), err == nil, outcomeOf(err), s.GetServiceName(), err)
		}()
	case *agentv1.SessionResponse_RemoveRunner:
		r := m.RemoveRunner
		l.redactor().Add(r.GetRemoval())
		go func() {
			if run == nil {
				l.answer(r.GetCommandId(), r.GetRunnerId(), false, "failed", "", errNoRunners)
				return
			}
			err := run.RemoveRunner(ctx, "", runners.RemoveRequest{
				RunnerID: r.GetRunnerId(), ServiceName: r.GetServiceName(), RemoveToken: r.GetRemoval(), KeepFiles: !r.GetDeleteFolder(),
			})
			l.answer(r.GetCommandId(), r.GetRunnerId(), err == nil, outcomeOf(err), r.GetServiceName(), err)
		}()
	case *agentv1.SessionResponse_DrainRunner:
		d := m.DrainRunner
		// One runner cannot be drained alone yet: the machine drain (Drain with
		// runner names) is the step that exists. Say so rather than pretend.
		go l.answer(d.GetCommandId(), d.GetRunnerId(), false, "failed", "", errors.New("draining one runner is not built in this agent; use the machine drain with the runner's name"))
	}
}

func outcomeOf(err error) string {
	if err != nil {
		return "failed"
	}
	return "done"
}

// preparePackages checks a runner's extra packages and, for a persistent
// runner, installs them into its prefix (runners R5.86). A bad name refuses
// the add before anything runs. A container runner (JIT config) gets the
// packages in its image build, so nothing is installed here for it.
func (l *link) preparePackages(ctx context.Context, spec backend.RunnerSpec, cfg backend.StartConfig) error {
	if len(spec.ExtraPackages) == 0 {
		return nil
	}
	if err := runners.ValidatePackageNames(spec.ExtraPackages); err != nil {
		return err
	}
	if cfg.JITConfig != "" {
		return nil
	}
	if l.cfg.InstallPackages == nil {
		return errors.New("this agent cannot install extra packages into a runner environment")
	}
	return l.cfg.InstallPackages(ctx, spec)
}
