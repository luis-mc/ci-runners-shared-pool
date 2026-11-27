package persistent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// The runner listener (Runner.Listener) is the process that holds the
// runner's one session with GitHub. A second listener for a registered runner
// is refused ("a session for this runner already exists") and the first one
// keeps the session. crsp therefore supervises the listener, not the service
// wrapper around it (systemd unit, launchd daemon, Windows service, runsvc):
// it never starts a service while a listener of that runner folder is alive,
// and after a stop it waits for the listener itself to be gone (runners R5.46).

// ErrListenerAlive means a listener of the runner is still running, so no
// second one is started.
var ErrListenerAlive = errors.New("a listener of this runner is still running")

// Defaults for WaitNoListener.
const (
	ListenerWait = 30 * time.Second
	ListenerPoll = time.Second
)

// ListenerCommand lists the process ids of the listeners that run from folder
// on Linux and macOS (pgrep prints one id per line; exit 1 means none).
func ListenerCommand(folder string) sys.Command {
	return sys.Command{Name: "pgrep", Args: []string{"-f", "--", "^" + regexp.QuoteMeta(strings.TrimRight(folder, "/")) + "/bin/Runner\\.Listener"}}
}

// ParsePIDs reads one process id per line and ignores everything else.
func ParsePIDs(out string) []int {
	var pids []int
	for _, l := range strings.Fields(out) {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			pids = append(pids, n)
		}
	}
	return pids
}

// UnixListeners returns the listener process ids of a runner folder.
func UnixListeners(ctx context.Context, cmd sys.Commands, folder string) ([]int, error) {
	res, err := cmd.Run(ctx, ListenerCommand(folder))
	if err != nil {
		return nil, fmt.Errorf("list listeners: %w", err)
	}
	if res.ExitCode > 1 { // 1 is "no match"
		return nil, fmt.Errorf("list listeners: pgrep exited %d", res.ExitCode)
	}
	return ParsePIDs(res.Stdout), nil
}

// WaitNoListener polls list until no listener of the runner is alive. When the
// wait runs out it returns ErrListenerAlive with the ids, and the caller must
// not start the service. A list that cannot be read is an error too: crsp does
// not start a second listener on a guess. sleep may be nil.
func WaitNoListener(ctx context.Context, list func(context.Context) ([]int, error), wait, poll time.Duration, sleep func(context.Context, time.Duration)) error {
	if wait <= 0 {
		wait = ListenerWait
	}
	if poll <= 0 {
		poll = ListenerPoll
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
			}
		}
	}
	var waited time.Duration
	for {
		pids, err := list(ctx)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if waited >= wait || ctx.Err() != nil {
			return fmt.Errorf("%w (process %v); no second listener is started", ErrListenerAlive, pids)
		}
		sleep(ctx, poll)
		waited += poll
	}
}

// CheckNoListener is the start guard (runners R5.59): it looks once and returns
// ErrListenerAlive with the process ids when a listener of the runner folder
// runs, so the caller reports it instead of starting a duplicate. A list that
// cannot be read is an error too.
func CheckNoListener(ctx context.Context, list func(context.Context) ([]int, error)) error {
	pids, err := list(ctx)
	if err != nil {
		return err
	}
	if len(pids) > 0 {
		return fmt.Errorf("%w (process %v); no second listener is started", ErrListenerAlive, pids)
	}
	return nil
}

// Ender ends the listeners of one runner after its service stopped (R5.59).
type Ender struct {
	// List returns the process ids of the runner's listeners (by runner folder).
	List func(context.Context) ([]int, error)
	// Signal asks the listeners and their process groups to end (force false:
	// terminate) or kills them (force true).
	Signal func(ctx context.Context, pids []int, force bool) error
	// Wait is how long the listener may take to end by itself; Grace is the
	// time given after the terminate and after the kill. Zero means the
	// defaults. Sleep may be nil.
	Wait, Grace, Poll time.Duration
	Sleep             func(context.Context, time.Duration)
	// Log receives one line per escalation; may be nil.
	Log func(string)
}

// EndListeners waits for the listeners to end, then escalates: terminate the
// process groups, wait a bounded grace, kill them, wait again. It returns nil
// only when no listener of the runner is left; otherwise ErrListenerAlive.
func EndListeners(ctx context.Context, e Ender) error {
	say := func(s string) {
		if e.Log != nil {
			e.Log(s)
		}
	}
	err := WaitNoListener(ctx, e.List, e.Wait, e.Poll, e.Sleep)
	for _, force := range []bool{false, true} {
		if err == nil || !errors.Is(err, ErrListenerAlive) || ctx.Err() != nil {
			return err
		}
		pids, lerr := e.List(ctx)
		if lerr != nil {
			return lerr
		}
		if len(pids) == 0 {
			return nil
		}
		verb := "terminating"
		if force {
			verb = "killing"
		}
		say(fmt.Sprintf("listener %v outlived its service; %s its process group", pids, verb))
		if e.Signal != nil {
			if serr := e.Signal(ctx, pids, force); serr != nil {
				say("signal failed: " + serr.Error())
			}
		}
		g := e.Grace
		if g <= 0 {
			g = ListenerGrace
		}
		err = WaitNoListener(ctx, e.List, g, e.Poll, e.Sleep)
	}
	return err
}

// ListenerGrace is the default grace after a terminate and after a kill.
const ListenerGrace = 10 * time.Second

// UnixSignal returns a Signal func for Linux and macOS: it signals each
// listener and, when ps knows it, the listener's whole process group.
func UnixSignal(cmd sys.Commands) func(context.Context, []int, bool) error {
	return func(ctx context.Context, pids []int, force bool) error {
		sig := "TERM"
		if force {
			sig = "KILL"
		}
		var first error
		for _, pid := range pids {
			targets := []string{strconv.Itoa(pid)}
			if res, err := cmd.Run(ctx, sys.Command{Name: "ps", Args: []string{"-o", "pgid=", "-p", strconv.Itoa(pid)}}); err == nil && res.ExitCode == 0 {
				if g := ParsePIDs(res.Stdout); len(g) == 1 && g[0] > 1 {
					targets = append(targets, "-"+strconv.Itoa(g[0]))
				}
			}
			for _, t := range targets {
				if _, err := cmd.Run(ctx, sys.Command{Name: "kill", Args: []string{"-s", sig, "--", t}}); err != nil && first == nil {
					first = err
				}
			}
		}
		return first
	}
}
