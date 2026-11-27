// Package container holds the engine access of the container backend
// (runners R5.27, R5.47, R5.48). The backend talks to the container engine
// only through Engine, so a test uses a fake and a later engine (rootless
// Podman in a crsp-owned WSL2 distro) replaces Docker with no change to the
// runners.
package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Route says how the engine is reached.
type Route string

// Routes. On Windows the Windows docker CLI comes first, then `wsl docker`
// (runners R5.47, machines R5.18). Elsewhere the engine is the local docker.
const (
	RouteNone       Route = ""
	RouteWindowsCLI Route = "windows-cli"
	RouteWSL        Route = "wsl"
	RouteNative     Route = "native"
)

// Reasons the engine is not usable, so a console and the doctor show the
// cause and not "failed" (runners R5.9, R5.47).
const (
	ReasonCLIMissing    = "the docker command is not installed"
	ReasonWSLMissing    = "WSL2 is not installed or not usable"
	ReasonEngineDown    = "the docker command is installed but the engine does not answer"
	ReasonWSLNoDocker   = "WSL2 is there but has no docker engine"
	ReasonNoEngineWinOS = "no docker engine: the Windows docker command cannot run and WSL2 has no docker"
)

// Status is what Detect found.
type Status struct {
	Present bool
	Usable  bool
	Route   Route
	Version string // engine (server) version
	WSL2    string // WSL2 state on Windows: "ok", "missing" or "" when not asked
	Reason  string // why it is not usable
}

// Engine is the container engine as the backend sees it.
type Engine interface {
	// Detect finds the engine, its version and the WSL2 state, and reports the
	// reason when it is not usable. It never edits an engine setting.
	Detect(ctx context.Context) (Status, error)
	// Command builds the call that runs `docker <args>` on the engine that
	// Detect found. It fails when Detect found none.
	Command(args ...string) (sys.Command, error)
	// Run runs `docker <args>`.
	Run(ctx context.Context, args ...string) (sys.Result, error)
}

// ErrNoEngine is returned when Run or Command is called with no usable engine.
var ErrNoEngine = errors.New("no usable container engine")

// NoEngineError is ErrNoEngine with the reason detection gave, so a failed
// add or clean-up says why ("the docker command is installed but the engine
// does not answer") and not only that there is no engine. errors.Is(err,
// ErrNoEngine) is true for it.
type NoEngineError struct{ Reason string }

// Error implements error.
func (e *NoEngineError) Error() string {
	if e.Reason == "" {
		return ErrNoEngine.Error()
	}
	return e.Reason
}

// Is makes errors.Is(err, ErrNoEngine) true.
func (e *NoEngineError) Is(target error) bool { return target == ErrNoEngine }

// detectTimeout bounds the detection that Command and Run start themselves.
const detectTimeout = 60 * time.Second

// Docker is the Docker engine. GOOS is runtime.GOOS in the product; tests set
// it. Command and Run detect the engine themselves when no Detect has found it
// yet, and again when an engine was not there at the last look, so nothing has
// to prime the instance and an engine started later is found.
type Docker struct {
	Cmd  sys.Commands
	GOOS string

	mu     sync.Mutex // guards route and reason
	route  Route
	reason string // why the last detection found no usable engine
}

const serverVersionFormat = "{{.Server.Version}}"

// Detect implements Engine.
func (d *Docker) Detect(ctx context.Context) (Status, error) {
	st := d.find(ctx)
	d.mu.Lock()
	d.route, d.reason = RouteNone, st.Reason
	if st.Usable {
		d.route = st.Route
	}
	d.mu.Unlock()
	return st, nil
}

// find probes the engine. It writes no field of d.
func (d *Docker) find(ctx context.Context) Status {
	if d.GOOS != "windows" {
		return d.detectNative(ctx)
	}
	st := d.wslState(ctx)
	// 1. The Windows docker CLI, which talks to the engine in WSL2.
	if v, present, why := d.probe(ctx, "docker"); v != "" {
		return Status{Present: true, Usable: true, Route: RouteWindowsCLI, Version: v, WSL2: st}
	} else if present {
		// Installed, engine silent: try WSL before giving up.
		if v2, _, _ := d.probe(ctx, "wsl", "docker"); v2 != "" {
			return Status{Present: true, Usable: true, Route: RouteWSL, Version: v2, WSL2: st}
		}
		return Status{Present: true, Route: RouteWindowsCLI, WSL2: st, Reason: ReasonEngineDown + " (" + why + ")"}
	}
	// 2. The CLI cannot run at all: `wsl docker`.
	if v, present, _ := d.probe(ctx, "wsl", "docker"); v != "" {
		return Status{Present: true, Usable: true, Route: RouteWSL, Version: v, WSL2: st}
	} else if present {
		return Status{Present: true, Route: RouteWSL, WSL2: st, Reason: ReasonWSLNoDocker}
	}
	reason := ReasonNoEngineWinOS
	if st == "missing" {
		reason = ReasonCLIMissing + "; " + ReasonWSLMissing
	}
	return Status{WSL2: st, Reason: reason}
}

func (d *Docker) detectNative(ctx context.Context) Status {
	v, present, why := d.probe(ctx, "docker")
	switch {
	case v != "":
		return Status{Present: true, Usable: true, Route: RouteNative, Version: v}
	case present:
		return Status{Present: true, Route: RouteNative, Reason: ReasonEngineDown + " (" + why + ")"}
	}
	return Status{Reason: ReasonCLIMissing}
}

// probe runs `<prog> [pre...] version --format ...`. It returns the server
// version when the engine answers, whether the program ran at all, and a short
// reason otherwise.
func (d *Docker) probe(ctx context.Context, prog string, pre ...string) (version string, ran bool, why string) {
	args := append(append([]string{}, pre...), "version", "--format", serverVersionFormat)
	res, err := d.Cmd.Run(ctx, sys.Command{Name: prog, Args: args})
	if err != nil {
		return "", false, err.Error()
	}
	if res.ExitCode == 0 {
		if v := strings.TrimSpace(res.Stdout); v != "" {
			return v, true, ""
		}
	}
	return "", true, firstLine(res.Stderr, fmt.Sprintf("exit %d", res.ExitCode))
}

func (d *Docker) wslState(ctx context.Context) string {
	res, err := d.Cmd.Run(ctx, sys.Command{Name: "wsl", Args: []string{"--status"}})
	if err != nil || res.ExitCode != 0 {
		return "missing"
	}
	return "ok"
}

// current returns the route. With none it detects once more, so an engine that
// came up after the last look is found, and a refusal carries the reason.
func (d *Docker) current(ctx context.Context) (Route, error) {
	d.mu.Lock()
	r := d.route
	d.mu.Unlock()
	if r != RouteNone {
		return r, nil
	}
	if _, err := d.Detect(ctx); err != nil {
		return RouteNone, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.route == RouteNone {
		return RouteNone, &NoEngineError{Reason: d.reason}
	}
	return d.route, nil
}

func buildCommand(r Route, args []string) sys.Command {
	if r == RouteWSL {
		return sys.Command{Name: "wsl", Args: append([]string{"docker"}, args...)}
	}
	return sys.Command{Name: "docker", Args: args}
}

// Command implements Engine.
func (d *Docker) Command(args ...string) (sys.Command, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()
	r, err := d.current(ctx)
	if err != nil {
		return sys.Command{}, err
	}
	return buildCommand(r, args), nil
}

// Run implements Engine.
func (d *Docker) Run(ctx context.Context, args ...string) (sys.Result, error) {
	r, err := d.current(ctx)
	if err != nil {
		return sys.Result{}, err
	}
	return d.Cmd.Run(ctx, buildCommand(r, args))
}

func firstLine(s, def string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
