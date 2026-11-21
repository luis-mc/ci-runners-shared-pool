package container

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// fakeDocker is a fake engine with a small model of containers, images and the
// files inside a pool container. It is both the Engine and the command runner
// the backend gets, so every call is recorded in one list. Nothing real runs.
type fakeDocker struct {
	mu         sync.Mutex
	calls      []string          // "docker <args>" of every call
	stdins     []string          // stdin of every call, same index
	containers map[string]*fakeC // by name
	images     map[string]fakeImg
	workers    int // Runner.Worker processes, in whatever pool is asked
	// onBusyCheck runs at every pgrep, so a test can change the world.
	onBusyCheck func(n int)
	busyChecks  int
	failBuild   string
	failRunTag  string // `run` of this image fails
	failStart   string
	failNetwork string
	wineLine    string // `wine --version` prints wine-<line>; empty: wine is not there
	wineBoots   int
	inspectFail string
	chowned     []string // owner given to the volumes by a one-shot chown
	engineDown  bool     // `docker version` fails: the engine does not answer
	now         time.Time
	// registry
	pullFail    int      // the next n pulls fail
	pullBadSum  string   // the digest a pull really gets, when not the asked one
	pushFail    int      // the next n pushes fail
	pullLines   []string // output lines of a pull, given to a streaming caller
	repoDigests map[string]string
	pulledSize  int64
}

type fakeC struct {
	args    []string
	running bool
	label   string
	key     string
	image   string
	files   map[string]string // path -> content ("" for a dir)
	started []string          // JIT configs the start script read from stdin
	owners  map[string]string // path -> uid:gid of the exec that wrote it
}

// user is the Config.User of the container: the --user of its run arguments.
func (c *fakeC) user() string {
	for i, x := range c.args {
		if x == "--user" {
			return c.args[i+1]
		}
	}
	return ""
}

type fakeImg struct {
	size   int64
	layers string
	at     time.Time
}

func newFake() *fakeDocker {
	return &fakeDocker{containers: map[string]*fakeC{}, images: map[string]fakeImg{}, now: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
}

func (f *fakeDocker) Detect(context.Context) (Status, error) {
	return Status{Present: true, Usable: true, Route: RouteNative, Version: "27.3.1"}, nil
}
func (f *fakeDocker) Command(args ...string) (sys.Command, error) {
	return sys.Command{Name: "docker", Args: args}, nil
}
func (f *fakeDocker) Run(ctx context.Context, args ...string) (sys.Result, error) {
	return f.handle(sys.Command{Name: "docker", Args: args}), nil
}

// Cmd side: the backend's command runner.
func (f *fakeDocker) Runner() sys.Commands { return cmdAdapter{f} }

type cmdAdapter struct{ f *fakeDocker }

func (c cmdAdapter) Run(_ context.Context, cmd sys.Command) (sys.Result, error) {
	return c.f.handle(cmd), nil
}

// RunStream is Run that also hands the pull's output lines to onLine.
func (c cmdAdapter) RunStream(_ context.Context, cmd sys.Command, onLine func(string)) (sys.Result, error) {
	res := c.f.handle(cmd)
	for _, a := range cmd.Args {
		if a == "pull" && res.ExitCode == 0 {
			for _, l := range c.f.pullLines {
				onLine(l)
			}
		}
	}
	return res, nil
}

func ok0() sys.Result              { return sys.Result{} }
func fail1(m string) sys.Result    { return sys.Result{ExitCode: 1, Stderr: m} }
func (f *fakeDocker) text() string { return strings.Join(f.calls, "\n") }

func (f *fakeDocker) count(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "docker "+prefix) {
			n++
		}
	}
	return n
}

func (f *fakeDocker) handle(cmd sys.Command) sys.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "docker "+strings.Join(cmd.Args, " "))
	f.stdins = append(f.stdins, cmd.Stdin)
	a := cmd.Args
	if a[0] == "--config" { // the token folder of a registry call
		a = a[2:]
	}
	switch a[0] {
	case "version":
		if f.engineDown {
			return fail1("error during connect: the engine does not answer")
		}
		return sys.Result{Stdout: "27.3.1\n"}
	case "inspect":
		name := a[len(a)-1]
		if f.inspectFail != "" {
			return fail1(f.inspectFail)
		}
		c, ok := f.containers[name]
		if !ok {
			return fail1("Error: No such object: " + name)
		}
		if strings.Contains(a[2], "Config.User") {
			return sys.Result{Stdout: c.user() + " " + c.image + "\n"}
		}
		return sys.Result{Stdout: fmt.Sprintf("%t %s\n", c.running, c.key)}
	case "network":
		if f.failNetwork != "" {
			return fail1(f.failNetwork)
		}
		return ok0()
	case "run":
		return f.run(a)
	case "start":
		if c, ok := f.containers[a[1]]; ok {
			c.running = true
			return ok0()
		}
		return fail1("Error: No such container: " + a[1])
	case "stop":
		if c, ok := f.containers[a[len(a)-1]]; ok {
			c.running = false
			return ok0()
		}
		return fail1("Error: No such container")
	case "rm":
		delete(f.containers, a[1])
		return ok0()
	case "exec":
		return f.exec(a, cmd.Stdin)
	case "build":
		if f.failBuild != "" {
			return fail1(f.failBuild)
		}
		tag, layers := "", ""
		for i, x := range a {
			if x == "-t" {
				tag = a[i+1]
			}
			if strings.HasPrefix(x, "crsp.layers=") {
				layers = strings.TrimPrefix(x, "crsp.layers=")
			}
		}
		f.now = f.now.Add(time.Minute)
		f.images[tag] = fakeImg{size: 900 << 20, layers: layers, at: f.now}
		return ok0()
	case "image":
		return f.image(a)
	case "ps":
		var out []string
		for _, c := range f.containers {
			out = append(out, c.image)
		}
		return sys.Result{Stdout: strings.Join(out, "\n")}
	case "rmi":
		delete(f.images, a[1])
		return ok0()
	case "pull":
		if f.pullFail > 0 {
			f.pullFail--
			return fail1("error pulling image: connection reset")
		}
		ref := a[1]
		_, digest, _ := strings.Cut(ref, "@")
		if f.pullBadSum != "" {
			digest = f.pullBadSum
		}
		size := f.pulledSize
		if size == 0 {
			size = 3400 << 20
		}
		f.images[ref] = fakeImg{size: size, layers: "", at: f.now}
		if f.repoDigests == nil {
			f.repoDigests = map[string]string{}
		}
		f.repoDigests[ref] = strings.SplitN(ref, "@", 2)[0] + "@" + digest
		return sys.Result{Stdout: "Digest: " + digest + "\n"}
	case "tag":
		im, ok := f.images[a[1]]
		if !ok {
			return fail1("Error: No such image: " + a[1])
		}
		f.images[a[2]] = im
		return ok0()
	case "push":
		if f.pushFail > 0 {
			f.pushFail--
			return fail1("denied: requested access to the resource is denied")
		}
		return sys.Result{Stdout: "k: digest: sha256:" + strings.Repeat("c", 64) + " size: 1234\n"}
	}
	return fail1("unknown call " + a[0])
}

func (f *fakeDocker) run(a []string) sys.Result {
	for _, x := range a { // the engine refuses an empty image like this
		if x == "" {
			return fail1("docker: invalid reference format\nRun 'docker run --help' for more information")
		}
	}
	if i := slices.Index(a, "chown"); i > 0 && i+1 < len(a) { // the one-shot volume owner
		f.chowned = append(f.chowned, a[i+1])
		return ok0()
	}
	c := &fakeC{args: a, running: true, files: map[string]string{}}
	name := ""
	for i, x := range a {
		switch x {
		case "--name":
			name = a[i+1]
		case "--label":
			if v, ok := strings.CutPrefix(a[i+1], LabelImage+"="); ok {
				c.key = v
			}
			if v, ok := strings.CutPrefix(a[i+1], LabelPool+"="); ok {
				c.label = v
			}
		}
	}
	for i, x := range a {
		if x == "sleep" && i > 0 {
			c.image = a[i-1]
		}
		if x == "sh" && i > 0 && i+1 < len(a) && a[i+1] == "-c" && c.image == "" {
			c.image = a[i-1]
		}
	}
	if c.image == f.failRunTag {
		return fail1("Error response from daemon: cannot start")
	}
	if _, dup := f.containers[name]; dup {
		return fail1("Conflict. The container name is already in use")
	}
	f.containers[name] = c
	return sys.Result{Stdout: "abc123\n"}
}

func (f *fakeDocker) exec(a []string, stdin string) sys.Result {
	// docker exec [-i] [-d] [-u U] [-w W] pool cmd...
	i := 1
	user := ""
	for i < len(a) {
		switch a[i] {
		case "-i":
			i++
		case "-d":
			i++
		case "-u":
			user = a[i+1]
			i += 2
		case "-w", "-e":
			i += 2
		default:
			goto found
		}
	}
found:
	c, ok := f.containers[a[i]]
	if !ok {
		return fail1("Error response from daemon: No such container: " + a[i])
	}
	if !c.running {
		return fail1("Error response from daemon: container is not running")
	}
	rest := a[i+1:]
	if user == "" {
		user = c.user() // the engine's default: the container's Config.User
	}
	// A user that is not the container's cannot write what the container owns.
	if user != c.user() && rest[0] == "sh" && strings.Contains(rest[2], "mkdir -p") {
		return fail1("mkdir: cannot create directory '" + rest[4] + "': Permission denied")
	}
	if c.owners == nil {
		c.owners = map[string]string{}
	}
	switch rest[0] {
	case "pgrep":
		f.busyChecks++
		if f.onBusyCheck != nil {
			f.mu.Unlock()
			f.onBusyCheck(f.busyChecks)
			f.mu.Lock()
		}
		if f.workers > 0 {
			return sys.Result{Stdout: "1234\n"}
		}
		return sys.Result{ExitCode: 1}
	case "pkill":
		return sys.Result{ExitCode: 1}
	case "cat":
		return sys.Result{Stdout: c.files[rest[1]]}
	case "test":
		if _, there := c.files[rest[2]]; there {
			return ok0()
		}
		return sys.Result{ExitCode: 1}
	case "mkdir":
		c.files[rest[2]] = ""
		return ok0()
	case "rm":
		delete(c.files, rest[3])
		return ok0()
	case "env":
		// env -u TMPDIR <tool> ...
		switch {
		case len(rest) > 4 && rest[3] == "wine" && rest[4] == "--version":
			if f.wineLine == "" {
				return fail1("wine: not found")
			}
			return sys.Result{Stdout: "wine-" + f.wineLine + " (Ubuntu)\n"}
		case len(rest) > 5 && rest[5] == "wineboot":
			f.wineBoots++
			c.files["/wine/system.reg"] = ""
		}
		return ok0()
	case "sh":
		script := rest[2]
		switch {
		case strings.HasPrefix(script, "mkdir -p") && strings.Contains(script, `cat >"$1.new"`):
			c.files[rest[4]] = stdin
			c.owners[rest[4]] = user
		case strings.HasPrefix(script, "mkdir -p"):
			c.files[rest[4]] = ""
		case strings.HasPrefix(script, "umask 077 && cat >"), strings.HasPrefix(script, "cat >"):
			c.files[rest[4]] = stdin
			c.owners[rest[4]] = user
		case strings.HasPrefix(script, "rm -rf"):
			for _, p := range rest[4:] {
				delete(c.files, p)
				for k := range c.files {
					if strings.HasPrefix(k, p+"/") {
						delete(c.files, k)
					}
				}
			}
		case strings.HasPrefix(script, "IFS= read -r c"):
			// The start script reads the config from stdin only.
			if f.failStart != "" {
				return fail1(f.failStart)
			}
			if strings.TrimSpace(stdin) == "" {
				return fail1("no config on stdin")
			}
			c.started = append(c.started, strings.TrimSpace(stdin))
		}
	}
	return ok0()
}

func (f *fakeDocker) image(a []string) sys.Result {
	switch a[1] {
	case "inspect":
		tag := a[len(a)-1]
		im, ok := f.images[tag]
		if !ok {
			return fail1("Error: No such image: " + tag)
		}
		format := a[3]
		if strings.Contains(format, "RepoDigests") {
			return sys.Result{Stdout: f.repoDigests[tag] + " "}
		}
		if strings.Contains(format, "|") {
			return sys.Result{Stdout: fmt.Sprintf("%d|%s|%s\n", im.size, im.at.Format(time.RFC3339Nano), im.layers)}
		}
		return sys.Result{Stdout: fmt.Sprintf("%d\n", im.size)}
	case "ls":
		var tags []string
		for t := range f.images {
			tags = append(tags, t)
		}
		return sys.Result{Stdout: strings.Join(tags, "\n")}
	}
	return fail1("unknown image call")
}
