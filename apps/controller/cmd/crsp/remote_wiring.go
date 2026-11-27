package main

import (
	"context"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// remoteAgent is the runners.Agent for machines other than the controller's
// own. A nil link gives a nil agent (not an interface holding a nil pointer).
func remoteAgent(l *agentlink.Link) runners.Agent {
	if l == nil {
		return nil
	}
	return &agentlink.Agent{L: l}
}

// agentRunners lets a remote agent carry out the controller's runner commands
// with the backends of its own OS (runners R5.1, R5.25).
func agentRunners() runners.Agent {
	reg := registry.Platform(realDeps(func(string, string) {}, secretRedactor.Redact, secretRedactor.Add))
	return runners.NewLocalAgent(reg)
}

// agentPreparePool lets dependency setup make the runner image and the
// persistent pool container (DEP9) through the container backend; a machine
// without that backend returns nil and the image is made at the first runner.
func agentPreparePool() func(context.Context, string, bool) error {
	reg := registry.Platform(realDeps(func(string, string) {}, secretRedactor.Redact, secretRedactor.Add))
	return func(ctx context.Context, arch string, persistent bool) error {
		b, ok := reg.Get(backend.Docker)
		if !ok {
			return errors.New("this machine has no container backend to prepare")
		}
		p, ok := b.(interface {
			PreparePool(context.Context, string, bool) (backend.ImageRef, error)
		})
		if !ok {
			return errors.New("this machine has no container backend to prepare")
		}
		_, err := p.PreparePool(ctx, arch, persistent)
		return err
	}
}

// agentImageSource lists the job images of the container backend for the
// machine report; a machine without that backend reports the list as unknown.
func agentImageSource() agent.ImageSource {
	reg := registry.Platform(realDeps(func(string, string) {}, secretRedactor.Redact, secretRedactor.Add))
	return func(ctx context.Context) ([]runners.Image, bool, error) {
		b, ok := reg.Get(backend.Docker)
		if !ok {
			return nil, false, nil
		}
		l, ok := b.(interface {
			ListImages(context.Context) ([]runners.Image, error)
		})
		if !ok {
			return nil, false, nil
		}
		imgs, err := l.ListImages(ctx)
		return imgs, err == nil, err
	}
}

// reportSources feeds the doctor from the machine reports the agents sent over
// the link, instead of local facts (doctor R5.32, R5.36). A machine that is
// offline or has not reported is left out: its checks have nothing to say.
type reportSources struct {
	fleet doctor.Fleet
	link  *agentlink.Link
}

var (
	_ doctor.KVMSource    = reportSources{}
	_ doctor.EngineSource = reportSources{}
)

func (r reportSources) reported(ctx context.Context) ([]doctor.Machine, map[string]agentlink.Report, error) {
	if r.link == nil {
		return nil, nil, doctor.ErrUnreachable
	}
	reps := r.link.Reports()
	if len(reps) == 0 {
		return nil, nil, doctor.ErrUnreachable
	}
	ms, err := r.fleet.Machines(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []doctor.Machine
	for _, m := range ms {
		if _, ok := reps[m.ID]; ok && m.Online {
			out = append(out, m)
		}
	}
	return out, reps, nil
}

// KVM lists the KVM state each online machine reported.
func (r reportSources) KVM(ctx context.Context) ([]doctor.KVMState, error) {
	ms, reps, err := r.reported(ctx)
	if err != nil {
		return nil, err
	}
	var out []doctor.KVMState
	for _, m := range ms {
		k := reps[m.ID].GetKvm()
		if k == nil {
			continue
		}
		out = append(out, doctor.KVMState{
			Machine: m, HostDevice: k.GetDeviceKnown() && k.GetDevicePresent(),
			NestedKnown: k.GetNestedKnown(), Nested: k.GetNestedVirtualization(),
			ModuleKnown: k.GetModuleKnown(), ModuleLoaded: k.GetModuleLoaded(),
		})
	}
	return out, nil
}

// Engines lists the container engine each online machine reported.
func (r reportSources) Engines(ctx context.Context) ([]doctor.EngineState, error) {
	ms, reps, err := r.reported(ctx)
	if err != nil {
		return nil, err
	}
	var out []doctor.EngineState
	for _, m := range ms {
		e := reps[m.ID].GetEngine()
		if e == nil {
			continue
		}
		out = append(out, doctor.EngineState{Machine: m, Kind: e.GetKind(), Usable: e.GetUsable(), Version: e.GetVersion(), Reason: e.GetReason()})
	}
	return out, nil
}
