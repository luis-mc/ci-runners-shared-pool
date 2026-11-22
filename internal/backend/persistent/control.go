package persistent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// The listener control of a gated persistent runner (runners R5.134): start,
// stop and status of the runner service on the host, the same logic on Linux
// (systemd), Windows (service) and macOS (launchd). Each OS backend gives the
// few steps that differ; the rules sit here once:
//   - a stop is refused while a job is assigned, and while a Runner.Worker of
//     the runner folder runs, whatever the controller said;
//   - a stop waits for the listener to exit by itself and never kills it: a
//     kill loses a job that was just assigned (runners R5.134);
//   - a start does nothing when the listener is already on, and never starts a
//     second one (R5.46).

// ErrJobAssigned is the refusal of a stop while a job is assigned.
var ErrJobAssigned = errors.New("a job is assigned to the runner: its listener is not stopped")

// ErrStopTimeout means the listener did not exit in time; it is still on.
var ErrStopTimeout = errors.New("the listener did not exit in time and was left running")

// StopWait is how long a clean stop waits for the listener to exit.
const StopWait = 90 * time.Second

// Control is the listener control of one OS backend.
type Control struct {
	// Folder gives the runner folder of a service ref.
	Folder func(ctx context.Context, ref string) (string, bool, error)
	// Listeners and Workers list the process ids of the listener and of the
	// worker of a runner folder.
	Listeners func(ctx context.Context, folder string) ([]int, error)
	Workers   func(ctx context.Context, folder string) ([]int, error)
	// StartService and StopService are the OS service steps: they only start or
	// stop the service and do not wait.
	StartService func(ctx context.Context, ref string, maintenance bool) error
	StopService  func(ctx context.Context, ref string) error
	Sleep        func(context.Context, time.Duration)
	Poll         time.Duration
	Wait         time.Duration
}

var _ backend.ListenerControl = (*Control)(nil)

func (c *Control) folder(ctx context.Context, ref string) (string, error) {
	f, ok, err := c.Folder(ctx, ref)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no runner service %q on this machine", ref)
	}
	return f, nil
}

// ListenerState implements backend.ListenerControl.
func (c *Control) ListenerState(ctx context.Context, ref string) (backend.ListenerState, error) {
	folder, err := c.folder(ctx, ref)
	if err != nil {
		return "", err
	}
	w, err := c.Workers(ctx, folder)
	if err != nil {
		return "", err
	}
	if len(w) > 0 {
		return backend.ListenerBusy, nil
	}
	l, err := c.Listeners(ctx, folder)
	if err != nil {
		return "", err
	}
	if len(l) > 0 {
		return backend.ListenerOnline, nil
	}
	return backend.ListenerOffline, nil
}

// StartListener implements backend.ListenerControl.
func (c *Control) StartListener(ctx context.Context, ref string, maintenance bool) error {
	folder, err := c.folder(ctx, ref)
	if err != nil {
		return err
	}
	l, err := c.Listeners(ctx, folder)
	if err != nil {
		return err // no start on a guess
	}
	if len(l) > 0 {
		return nil // already on
	}
	return c.StartService(ctx, ref, maintenance)
}

// StopListener implements backend.ListenerControl.
func (c *Control) StopListener(ctx context.Context, ref string, jobAssigned bool) error {
	if jobAssigned {
		return ErrJobAssigned
	}
	folder, err := c.folder(ctx, ref)
	if err != nil {
		return err
	}
	w, err := c.Workers(ctx, folder)
	if err != nil {
		return err
	}
	if len(w) > 0 {
		return ErrJobAssigned
	}
	if err := c.StopService(ctx, ref); err != nil {
		return err
	}
	wait := c.Wait
	if wait <= 0 {
		wait = StopWait
	}
	err = WaitNoListener(ctx, func(ctx context.Context) ([]int, error) { return c.Listeners(ctx, folder) }, wait, c.Poll, c.Sleep)
	if errors.Is(err, ErrListenerAlive) {
		return ErrStopTimeout
	}
	return err
}

// WorkerCommand lists the process ids of the workers that run from folder on
// Linux and macOS (pgrep exit 1 means none).
func WorkerCommand(folder string) sys.Command {
	return sys.Command{Name: "pgrep", Args: []string{"-f", "--", "^" + regexp.QuoteMeta(strings.TrimRight(folder, "/")) + "/bin/Runner\\.Worker"}}
}

// UnixWorkers returns the worker process ids of a runner folder.
func UnixWorkers(ctx context.Context, cmd sys.Commands, folder string) ([]int, error) {
	res, err := cmd.Run(ctx, WorkerCommand(folder))
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	if res.ExitCode > 1 {
		return nil, fmt.Errorf("list workers: pgrep exited %d", res.ExitCode)
	}
	return ParsePIDs(res.Stdout), nil
}
