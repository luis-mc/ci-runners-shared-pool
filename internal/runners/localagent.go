package runners

import (
	"context"
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
)

// LocalAgent runs the Agent calls on this machine through the backend
// registry. The agent process uses it to carry out what the controller sends
// over the agent link (decision 0004); the M1 agent on the controller's own
// machine uses the same path. The machine id of a call is the link's business,
// so LocalAgent ignores it.
type LocalAgent struct {
	Registry backend.Registry
}

var _ Agent = (*LocalAgent)(nil)

// NewLocalAgent builds a LocalAgent over a registry.
func NewLocalAgent(reg backend.Registry) *LocalAgent { return &LocalAgent{Registry: reg} }

func (l *LocalAgent) persistent() (backend.Backend, error) {
	b, ok := l.Registry.Get(backend.Persistent)
	if !ok {
		return nil, errors.New("this machine has no persistent runner backend")
	}
	return b, nil
}

// container returns the container backend, when this binary has one.
func (l *LocalAgent) container() (backend.Backend, error) {
	b, ok := l.Registry.Get(backend.Docker)
	if !ok {
		return nil, errors.New("this machine has no container runner backend")
	}
	return b, nil
}

// isContainerRef tells a container handle (`<pool>:<runner>`) from the name of
// an OS service, which never holds a colon.
func isContainerRef(ref string) bool { return strings.Contains(ref, ":") }

// forRef picks the backend that owns a handle.
func (l *LocalAgent) forRef(ref string) (backend.Backend, error) {
	if isContainerRef(ref) {
		return l.container()
	}
	return l.persistent()
}

// AddRunner starts a runner. A JIT config means an ephemeral runner in a
// container (runners R5.25, R5.27); a registration token means a persistent
// one. The caller has checked that the machine's allow list names the backend.
func (l *LocalAgent) AddRunner(ctx context.Context, _ string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	if cfg.JITConfig != "" {
		b, err := l.container()
		if err != nil {
			return backend.Handle{}, err
		}
		if spec.Image.ID == "" {
			img, err := b.PrepareImage(ctx, backend.ImageSpec{OS: spec.OS, Arch: spec.Arch, Layers: spec.Pool.Layers, Extras: spec.ExtraPackages})
			if err != nil {
				return backend.Handle{}, err
			}
			spec.Image = img
		}
		return b.Start(ctx, spec, cfg)
	}
	if spec.Container {
		// A persistent Linux runner on a Windows machine: one long-lived container (runners R5.93).
		b, err := l.container()
		if err != nil {
			return backend.Handle{}, err
		}
		return b.Start(ctx, spec, cfg)
	}
	b, err := l.persistent()
	if err != nil {
		return backend.Handle{}, err
	}
	return b.Start(ctx, spec, cfg)
}

// StopService stops the runner's service.
func (l *LocalAgent) StopService(ctx context.Context, _ string, service string) error {
	b, err := l.forRef(service)
	if err != nil {
		return err
	}
	return b.Stop(ctx, backend.Handle{Ref: service})
}

// RemoveRunner deregisters with the remove token and checks the result; unless
// KeepFiles is set it then cleans up. A failed deregistration stops here and
// leaves the runner as it was (runners R5.19).
func (l *LocalAgent) RemoveRunner(ctx context.Context, _ string, req RemoveRequest) error {
	b, err := l.forRef(req.ServiceName)
	if err != nil {
		return err
	}
	h := backend.Handle{RunnerID: req.RunnerID, Ref: req.ServiceName}
	d, ok := b.(backend.Deregisterer)
	if !ok {
		return errors.New("this backend cannot deregister a runner")
	}
	if err := d.Deregister(ctx, h, req.RemoveToken); err != nil {
		return err
	}
	if req.KeepFiles {
		return nil
	}
	return b.CleanUp(ctx, h)
}

// CleanUp removes the service entry, the account and the folder.
func (l *LocalAgent) CleanUp(ctx context.Context, _ string, service string) error {
	b, err := l.forRef(service)
	if err != nil {
		return err
	}
	return b.CleanUp(ctx, backend.Handle{Ref: service})
}

// Found lists the runners in the OS service list (runners R5.5).
func (l *LocalAgent) Found(ctx context.Context) ([]backend.Found, error) {
	b, err := l.persistent()
	if err != nil {
		return nil, err
	}
	f, ok := b.(backend.Finder)
	if !ok {
		return nil, nil
	}
	return f.Find(ctx)
}

// HealServices re-asserts the logon right of every runner service account and
// starts a stopped service, on backends that can (Windows; runners R5.89). A
// backend without the step answers nothing.
func (l *LocalAgent) HealServices(ctx context.Context) ([]backend.ServiceHealth, error) {
	b, err := l.persistent()
	if err != nil {
		return nil, nil //nolint:nilerr // no persistent backend: no services to heal
	}
	h, ok := b.(backend.ServiceHealer)
	if !ok {
		return nil, nil
	}
	return h.HealServices(ctx)
}

// containerFinder is the container backend's list of its persistent runners.
type containerFinder interface {
	FindPersistent(ctx context.Context) ([]backend.Found, error)
}

// Services lists what the machine runs for the runner view: the OS services
// and the persistent runners of the pool container, whose ServiceName is the
// handle ref a record stores (runners R5.93, OPS70). A machine without a
// persistent OS backend still answers with its container runners; a container
// that cannot be read is an error, so the view claims no service fact.
func (l *LocalAgent) Services(ctx context.Context) ([]backend.Found, error) {
	out, err := l.Found(ctx)
	b, cerr := l.container()
	if cerr != nil {
		return out, err
	}
	cf, ok := b.(containerFinder)
	if !ok {
		return out, err
	}
	got, ferr := cf.FindPersistent(ctx)
	if ferr != nil {
		return nil, ferr
	}
	return append(out, got...), nil
}

// ControlListener starts, stops or reads the listener of a persistent runner
// on this machine (runners R5.134). action is start, stop or status. A backend
// that cannot do it (a container runner, a VM) says so.
func (l *LocalAgent) ControlListener(ctx context.Context, action, service string, jobAssigned, maintenance bool) (backend.ListenerState, error) {
	if isContainerRef(service) {
		return "", errors.New("only a persistent runner on the machine itself has a gated listener")
	}
	b, err := l.persistent()
	if err != nil {
		return "", err
	}
	lc, ok := b.(backend.ListenerControl)
	if !ok {
		return "", errors.New("this backend cannot control a runner listener")
	}
	switch action {
	case "start":
		err = lc.StartListener(ctx, service, maintenance)
	case "stop":
		err = lc.StopListener(ctx, service, jobAssigned)
	case "status":
	default:
		return "", errors.New("unknown listener action " + action)
	}
	if err != nil {
		return "", err
	}
	return lc.ListenerState(ctx, service)
}
