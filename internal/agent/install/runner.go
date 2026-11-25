package install

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// EngineInfo is what the agent reads of the container engine (the agent wires
// DetectEngine into it; this package cannot import the agent).
type EngineInfo struct {
	Usable      bool
	Version     string
	ContainerOS string // "linux" or "windows"; empty when unknown
	Reason      string
	// DockerPrefix is the argv that reaches the docker command ("docker", or
	// "wsl -d crsp-docker -u root -- docker"). Empty means "docker".
	DockerPrefix []string
}

// Config is what a Runner needs.
type Config struct {
	Sys    System
	Pinned depsetup.List
	// Emit gets every DepsEvent, including the final one.
	Emit func(*agentv1.DepsEvent)
	// State reads one item now: ok, missing, stopped or wrong_kind (read-only).
	State func(ctx context.Context, itemID string) string
	// Engine reads the engine for Verify.
	Engine func(ctx context.Context) EngineInfo
	// VirtualizationOff is true when Windows firmware has virtualization off.
	VirtualizationOff func() bool
	// OSRelease returns the text of /etc/os-release.
	OSRelease func() string
	// Prepare makes the image and the pool container. Nil: prepared at the first runner start.
	Prepare func(ctx context.Context) error
	Redact  func(string) string
	Now     func() time.Time
	// RemoveConflicts is the owner's yes to remove unofficial Docker packages
	// (default no: the install is refused and names them).
	RemoveConflicts bool
	// TestImage is the tiny image of the Verify test container.
	TestImage string
}

// Runner runs dependency jobs.
type Runner struct{ cfg Config }

// New builds a Runner.
func New(c Config) *Runner {
	if c.TestImage == "" {
		c.TestImage = "hello-world"
	}
	return &Runner{cfg: c}
}

// Message of a refused or mismatched download (spec 8).
const (
	MsgHashMismatch = "The file did not match; nothing was installed."
	MsgNotPinned    = "This installer is not pinned in this release, so crsp will not run it. Use \"Do it myself\"."
	MsgNeedsAdmin   = "This needs an administrator. Run the agent with administrator rights, or use \"Do it myself\"."
)

// StopPrefix: a DepsInstall with no item ids and a command_id of
// "stop:<job_id>" asks the agent to stop that job at the next safe point.
const StopPrefix = "stop:"

var phaseRank = map[string]int{"download": 1, "install": 2, "restart": 3, "verify": 4, "prepare": 5}

type todoItem struct {
	p     depsetup.Pinned
	state string
}

func itemRank(id string) int {
	if id == depsetup.ItemWSL2 {
		return 0
	}
	return 1
}

// Install runs the phases of a job and sends events; the last one is final.
func (r *Runner) Install(ctx context.Context, req *agentv1.DepsInstall) {
	sys := r.cfg.Sys
	j := &job{r: r, id: req.GetJobId(), name: req.GetMachineName(), role: req.GetMachineRole(), os: sys.GOOS()}
	j.added = append(j.added, req.GetAdded()...)
	goos := sys.GOOS()
	resume := phaseRank[req.GetResumeFrom()]

	// 1. Validate everything before anything runs (DEP5, DEP16).
	var todo []todoItem
	seen := map[string]bool{}
	for _, id := range req.GetItemIds() {
		if seen[id] {
			continue
		}
		seen[id] = true
		p, ok := r.cfg.Pinned.Find(id, goos)
		if !ok {
			j.phases = []string{"install"}
			j.final("install", "failed", fmt.Sprintf("crsp has no installer called %q for this machine; nothing was installed.", id), "")
			return
		}
		st := depsetup.StateMissing
		if r.cfg.State != nil {
			st = r.cfg.State(ctx, id)
		}
		if st == depsetup.StateOK || resume >= phaseRank["verify"] {
			continue
		}
		todo = append(todo, todoItem{p, st})
	}
	sort.SliceStable(todo, func(a, b int) bool { return itemRank(todo[a].p.ID) < itemRank(todo[b].p.ID) })
	if msg := r.validate(todo, req.GetAcceptLicence(), sys); msg != "" {
		j.phases = []string{"install"}
		j.final("install", "failed", msg, "")
		return
	}

	// 2. The phases this job runs.
	needDL, needInst, needRestart := false, len(todo) > 0, false
	for _, t := range todo {
		if t.p.Method == depsetup.MethodDownload {
			needDL = true
		}
		if t.p.RestartLikely {
			needRestart = true
		}
	}
	if resume > phaseRank["install"] {
		needDL, needInst = false, false
	}
	if resume > phaseRank["restart"] {
		needRestart = false
	}
	if resume > phaseRank["download"] && resume > phaseRank["install"] {
		needDL = false
	}
	if needDL {
		j.phases = append(j.phases, "download")
	}
	if needInst {
		j.phases = append(j.phases, "install")
	}
	if needRestart {
		j.phases = append(j.phases, "restart")
	}
	j.phases = append(j.phases, "verify")
	if resume <= phaseRank["prepare"] {
		j.phases = append(j.phases, "prepare")
	}
	if len(todo) == 0 && resume == 0 {
		j.status("verify", "Everything asked for is already there; nothing was changed.")
	}

	// 3. Download.
	files := map[string]string{}
	if needDL {
		for _, t := range todo {
			if t.p.Method != depsetup.MethodDownload {
				continue
			}
			path, err := r.download(ctx, j, t.p)
			if err != nil {
				r.finishErr(ctx, j, "download", err)
				return
			}
			files[t.p.ID] = path
		}
	}

	// 4. Install.
	restartNeeded := false
	if needInst {
		for _, t := range todo {
			if ctx.Err() != nil {
				j.final("install", "stopped", "", "")
				return
			}
			need, err := r.installItem(ctx, j, req, t, files[t.p.ID])
			if err != nil {
				r.finishErr(ctx, j, "install", err)
				return
			}
			if need {
				restartNeeded = true
				break // later items need the restarted machine
			}
		}
	}
	if restartNeeded {
		j.status("restart", "A restart is needed to finish. crsp does not restart this PC by itself.")
		j.final("restart", "paused_restart", "", "")
		return
	}

	// 5. Verify.
	if ctx.Err() != nil {
		j.final("verify", "stopped", "", "")
		return
	}
	version, err := r.verify(ctx, j, req)
	if err != nil {
		r.finishErr(ctx, j, "verify", err)
		return
	}

	// 6. Prepare.
	if resume <= phaseRank["prepare"] {
		if ctx.Err() != nil {
			j.final("prepare", "stopped", "", "")
			return
		}
		if r.cfg.Prepare == nil {
			j.status("prepare", "The image and the pool container are made when the first runner starts.")
		} else {
			j.status("prepare", "Making the runner image and the pool container.")
			if err := r.cfg.Prepare(ctx); err != nil {
				r.finishErr(ctx, j, "prepare", fmt.Errorf("the runner image could not be prepared: %w", err))
				return
			}
		}
	}
	j.final(j.phases[len(j.phases)-1], "done", "", version)
}

// finishErr ends the job as stopped (context cancelled) or failed.
func (r *Runner) finishErr(ctx context.Context, j *job, phase string, err error) {
	if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(err, ctx.Err())) {
		j.final(phase, "stopped", "", "")
		return
	}
	j.final(phase, "failed", err.Error(), "")
}

// validate checks the plan before anything runs; it returns plain words.
func (r *Runner) validate(todo []todoItem, licence bool, sys System) string {
	for _, t := range todo {
		p := t.p
		if t.state == depsetup.StateWrongKind {
			return fmt.Sprintf("%s is installed, but not in the kind crsp needs (for example Windows containers, or the snap package). crsp never changes it. Use \"Do it myself\".", p.Name)
		}
		if p.NeedsAdmin && !sys.IsAdmin() {
			return MsgNeedsAdmin
		}
		if p.Method != depsetup.MethodDownload {
			continue
		}
		if p.URL == "" || p.SHA256 == "" {
			return MsgNotPinned
		}
		if !allowedURL(p.URL) {
			return MsgNotPinned
		}
		if p.ID == depsetup.ItemDockerDesktop && !licence {
			return "Docker Desktop needs your yes that you have the right to use it. Tick the box and try again."
		}
	}
	return ""
}

var allowedHosts = map[string]bool{"download.docker.com": true, "desktop.docker.com": true}

func allowedURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && allowedHosts[strings.ToLower(u.Hostname())]
}

func (r *Runner) download(ctx context.Context, j *job, p depsetup.Pinned) (string, error) {
	sys := r.cfg.Sys
	ext := ".bin"
	switch sys.GOOS() {
	case "windows":
		ext = ".exe"
	case "darwin":
		ext = ".dmg"
	}
	sep := "/"
	if sys.GOOS() == "windows" {
		sep = `\`
	}
	path := strings.TrimRight(sys.TempDir(), `/\`) + sep + "crsp-" + p.ID + ext
	if sys.Exists(path) {
		if h, err := sys.SHA256(path); err == nil && strings.EqualFold(h, p.SHA256) {
			j.status("download", p.Name+" was downloaded before and the file matches.")
			return path, nil
		}
		_ = sys.Remove(path)
	}
	j.status("download", "Downloading "+p.Name+" from "+p.Source)
	if err := sys.Download(ctx, p.URL, path, j.progress("download", "Downloading "+p.Name)); err != nil {
		_ = sys.Remove(path)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("the download of %s failed: %s", p.Name, err.Error())
	}
	got, err := sys.SHA256(path)
	if err != nil || !strings.EqualFold(got, p.SHA256) {
		_ = sys.Remove(path)
		return "", errors.New(MsgHashMismatch)
	}
	j.status("download", "The file matches the checksum in this release.")
	return path, nil
}

// run runs one command: the first event carries the redacted command line.
func (r *Runner) run(ctx context.Context, j *job, phase string, argv []string) (int, error) {
	e := j.base(phase)
	e.Stream, e.Command = "status", r.redact(strings.Join(argv, " "))
	e.Line = "$ " + e.Command
	e.Reason = "The command does not report a percentage."
	j.send(e)
	j.last = nil
	// The command runs to its end even when Stop is pressed: Stop acts between commands.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Minute)
	defer cancel()
	code, err := r.cfg.Sys.Run(runCtx, argv, func(stream, line string) {
		line = r.redact(line)
		if strings.TrimSpace(line) == "" {
			return
		}
		j.last = append(j.last, line)
		if len(j.last) > 20 {
			j.last = j.last[1:]
		}
		ev := j.base(phase)
		ev.Stream, ev.Line = stream, line
		ev.Reason = "The command does not report a percentage."
		j.send(ev)
	})
	return code, err
}

func (j *job) lastWords() string {
	if len(j.last) == 0 {
		return ""
	}
	return " Last line: " + j.last[len(j.last)-1]
}

// mustRun runs a command and turns a failure into plain words.
func (r *Runner) mustRun(ctx context.Context, j *job, phase, what string, argv []string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code, err := r.run(ctx, j, phase, argv)
	if err != nil {
		return fmt.Errorf("%s could not start: %s", what, r.redact(err.Error()))
	}
	if code != 0 {
		return fmt.Errorf("%s stopped with code %d.%s", what, code, j.lastWords())
	}
	return nil
}

func wantsRestart(code int, lines []string) bool {
	if code == 3010 {
		return true
	}
	for _, l := range lines {
		ll := strings.ToLower(l)
		if strings.Contains(ll, "restart") || strings.Contains(ll, "reboot") {
			return true
		}
	}
	return false
}

// installItem installs one item; need says a restart is needed before going on.
func (r *Runner) installItem(ctx context.Context, j *job, req *agentv1.DepsInstall, t todoItem, file string) (need bool, err error) {
	p, goos := t.p, r.cfg.Sys.GOOS()
	j.status("install", "Installing "+p.Name)
	if t.state == depsetup.StateStopped {
		return false, r.startStopped(ctx, j, p)
	}
	switch {
	case p.ID == depsetup.ItemWSL2 && goos == "windows":
		return r.installWSL2(ctx, j, p)
	case p.ID == depsetup.ItemDockerWSL2 && goos == "windows":
		return false, r.installDockerWSL2(ctx, j, p)
	case p.ID == depsetup.ItemDockerEngine && goos == "linux":
		return false, r.installDockerEngine(ctx, j, p)
	case p.ID == depsetup.ItemDockerDesktop && goos == "windows":
		return r.installDesktop(ctx, j, p, file)
	case p.ID == depsetup.ItemDockerDesktop && goos == "darwin":
		return false, errors.New("needs a person at the screen: approve one prompt. Download Docker Desktop, open it and approve the system prompt once, then check again. " + strings.Join(p.ManualSteps, " "))
	}
	return false, fmt.Errorf("crsp has no install method for %s on this machine", p.Name)
}

func (r *Runner) startStopped(ctx context.Context, j *job, p depsetup.Pinned) error {
	if p.ID == depsetup.ItemDockerEngine && r.cfg.Sys.GOOS() == "linux" {
		return r.mustRun(ctx, j, "install", "Starting Docker", []string{"systemctl", "start", "docker"})
	}
	return fmt.Errorf("%s is installed but not running. Start it, then check again; crsp does not start it for you.", p.Name) //nolint:revive,staticcheck // a sentence for the user
}

func (r *Runner) installWSL2(ctx context.Context, j *job, p depsetup.Pinned) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	code, err := r.run(ctx, j, "install", []string{"wsl", "--install", "--no-distribution"})
	if err != nil {
		return false, fmt.Errorf("WSL could not start: %s", r.redact(err.Error()))
	}
	if code != 0 && code != 3010 {
		return false, fmt.Errorf("the WSL install stopped with code %d.%s", code, j.lastWords())
	}
	j.add(p.ID, "wsl-feature", "wsl2", p.Version)
	return wantsRestart(code, j.last), nil
}

func (r *Runner) installDockerWSL2(ctx context.Context, j *job, p depsetup.Pinned) error {
	sys := r.cfg.Sys
	var list []string
	code, err := r.cfgRunCapture(ctx, j, []string{"wsl", "--list", "--quiet"}, &list)
	if err != nil || code != 0 {
		return errors.New("WSL2 is not ready yet. Install it (and restart if asked), then try again")
	}
	have := false
	for _, l := range list {
		if strings.EqualFold(strings.TrimSpace(l), DistroName) {
			have = true
		}
	}
	if !have {
		if err := r.mustRun(ctx, j, "install", "Making the crsp WSL distro", []string{"wsl", "--install", "-d", "Ubuntu-24.04", "--name", DistroName, "--no-launch"}); err != nil {
			return err
		}
		j.add(p.ID, "wsl-distro", DistroName, "Ubuntu-24.04")
	}
	w := []string{"wsl", "-d", DistroName, "-u", "root", "--"}
	line := strings.TrimSuffix(RepoLine("ubuntu", "noble", sys.Arch()), "\n")
	apt := func(args ...string) []string {
		return append(append(append([]string(nil), w...), "env", "DEBIAN_FRONTEND=noninteractive", "apt-get"), args...)
	}
	steps := []struct {
		what string
		argv []string
	}{
		{"Updating the package list", apt("update")},
		{"Installing the certificates package", apt("install", "-y", "ca-certificates", "curl")},
		{"Making the key folder", append(append([]string(nil), w...), "install", "-m", "0755", "-d", "/etc/apt/keyrings")},
		{"Fetching Docker's signing key", append(append([]string(nil), w...), "curl", "-fsSL", KeyURL("ubuntu"), "-o", KeyPath)},
		{"Making the key readable", append(append([]string(nil), w...), "chmod", "a+r", KeyPath)},
		{"Adding Docker's package source", append(append([]string(nil), w...), "sh", "-c", `printf '%s\n' "$1" > `+RepoPath, "sh", line)},
		{"Updating the package list", apt("update")},
		{"Installing Docker Engine", apt(append([]string{"install", "-y"}, DockerPackages...)...)},
	}
	for _, s := range steps {
		if err := r.mustRun(ctx, j, "install", s.what, s.argv); err != nil {
			return err
		}
	}
	if err := r.mustRun(ctx, j, "install", "Starting Docker", append(append([]string(nil), w...), "systemctl", "enable", "--now", "docker")); err != nil {
		// A distro without systemd: start the service the old way.
		if err2 := r.mustRun(ctx, j, "install", "Starting Docker", append(append([]string(nil), w...), "service", "docker", "start")); err2 != nil {
			return err2
		}
	}
	return nil
}

// cfgRunCapture runs a command and keeps its stdout lines (NULs of wsl.exe removed).
func (r *Runner) cfgRunCapture(ctx context.Context, j *job, argv []string, out *[]string) (int, error) {
	code, err := r.cfg.Sys.Run(context.WithoutCancel(ctx), argv, func(stream, line string) {
		if stream == "stdout" {
			*out = append(*out, strings.ReplaceAll(line, "\x00", ""))
		}
	})
	_ = j
	return code, err
}

func (r *Runner) installDockerEngine(ctx context.Context, j *job, p depsetup.Pinned) error {
	sys := r.cfg.Sys
	text := ""
	if r.cfg.OSRelease != nil {
		text = r.cfg.OSRelease()
	}
	family, codename, ok := ParseOSRelease(text).DockerRepo()
	if !ok {
		return errors.New("crsp installs Docker only on Ubuntu and Debian in this release. Use \"Do it myself\": " + strings.Join(ManualDockerEngine(), " "))
	}
	have, err := r.installedPackages(ctx, j, append(append([]string(nil), ConflictPackages...), DockerPackages...))
	if err != nil {
		return err
	}
	var conflicts []string
	for _, c := range ConflictPackages {
		if have[c] {
			conflicts = append(conflicts, c)
		}
	}
	if len(conflicts) > 0 {
		if !r.cfg.RemoveConflicts {
			return fmt.Errorf("these packages conflict with Docker's own: %s. crsp removes them only with your yes; remove them yourself or say yes and try again", strings.Join(conflicts, ", "))
		}
		if err := r.mustRun(ctx, j, "install", "Removing the conflicting packages", append([]string{"apt-get", "remove", "-y"}, conflicts...)); err != nil {
			return err
		}
	}
	env := []string{"env", "DEBIAN_FRONTEND=noninteractive", "apt-get"}
	aptc := func(args ...string) []string { return append(append([]string(nil), env...), args...) }
	if err := r.mustRun(ctx, j, "install", "Updating the package list", aptc("update")); err != nil {
		return err
	}
	if err := r.mustRun(ctx, j, "install", "Installing the certificates package", aptc("install", "-y", "ca-certificates")); err != nil {
		return err
	}
	if err := r.mustRun(ctx, j, "install", "Making the key folder", []string{"install", "-m", "0755", "-d", "/etc/apt/keyrings"}); err != nil {
		return err
	}
	keyWas, repoWas := sys.Exists(KeyPath), sys.Exists(RepoPath)
	if !allowedURL(KeyURL(family)) {
		return errors.New(MsgNotPinned)
	}
	j.status("install", "Fetching Docker's signing key from download.docker.com")
	if err := sys.Download(ctx, KeyURL(family), KeyPath, j.progress("install", "Fetching Docker's signing key")); err != nil {
		return fmt.Errorf("the download of Docker's signing key failed: %s", r.redact(err.Error()))
	}
	if !keyWas {
		j.add(p.ID, "apt-key", KeyPath, "")
	}
	if err := r.mustRun(ctx, j, "install", "Making the key readable", []string{"chmod", "a+r", KeyPath}); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sys.WriteFile(RepoPath, []byte(RepoLine(family, codename, sys.Arch())), 0o644); err != nil { //nolint:gosec // a package source list is world-readable
		return fmt.Errorf("Docker's package source could not be written: %s", r.redact(err.Error())) //nolint:staticcheck // a sentence for the user
	}
	if !repoWas {
		j.add(p.ID, "apt-repo-file", RepoPath, "")
	}
	if err := r.mustRun(ctx, j, "install", "Updating the package list", aptc("update")); err != nil {
		return err
	}
	if err := r.mustRun(ctx, j, "install", "Installing Docker Engine", aptc(append([]string{"install", "-y"}, DockerPackages...)...)); err != nil {
		return err
	}
	for _, pk := range DockerPackages {
		if !have[pk] {
			j.add(p.ID, "apt-package", pk, "")
		}
	}
	enabled, _ := r.cfg.Sys.Run(context.WithoutCancel(ctx), []string{"systemctl", "is-enabled", "docker"}, func(string, string) {})
	if err := r.mustRun(ctx, j, "install", "Starting Docker", []string{"systemctl", "enable", "--now", "docker"}); err != nil {
		return err
	}
	if enabled != 0 {
		j.add(p.ID, "systemd-enable", "docker", "")
	}
	return nil
}

// installedPackages asks dpkg which of names are installed (read only).
func (r *Runner) installedPackages(ctx context.Context, j *job, names []string) (map[string]bool, error) {
	var out []string
	argv := append([]string{"dpkg-query", "-W", "-f=${Package} ${Status}\\n"}, names...)
	_, err := r.cfgRunCapture(ctx, j, argv, &out)
	if err != nil {
		return nil, fmt.Errorf("the installed packages could not be read: %s", r.redact(err.Error()))
	}
	have := map[string]bool{}
	for _, l := range out {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.HasSuffix(strings.TrimSpace(l), "install ok installed") {
			have[f[0]] = true
		}
	}
	return have, nil
}

func (r *Runner) installDesktop(ctx context.Context, j *job, p depsetup.Pinned, file string) (bool, error) {
	if file == "" {
		return false, errors.New(MsgNotPinned)
	}
	defer func() { _ = r.cfg.Sys.Remove(file) }()
	argv := append([]string{file}, p.SilentArgs...)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	code, err := r.run(ctx, j, "install", argv)
	if err != nil {
		return false, fmt.Errorf("the Docker Desktop installer could not start: %s", r.redact(err.Error()))
	}
	if code != 0 && code != 3010 {
		return false, fmt.Errorf("the Docker Desktop installer stopped with code %d.%s", code, j.lastWords())
	}
	j.add(p.ID, "windows-installer", "Docker Desktop", p.Version)
	return wantsRestart(code, j.last), nil
}

// verify proves the engine works: it answers, runs the right containers and a
// test container runs. A test container that fails fails the job, whatever the
// install commands returned (DEP8).
func (r *Runner) verify(ctx context.Context, j *job, req *agentv1.DepsInstall) (string, error) {
	j.status("verify", "Checking that the engine answers and runs a test container.")
	need := req.GetRunnerOs()
	if need == "" {
		need = "linux"
	}
	if need != "linux" {
		j.status("verify", "This runner needs nothing beyond crsp; nothing to check.")
		return "", nil
	}
	var eng EngineInfo
	if r.cfg.Engine != nil {
		eng = r.cfg.Engine(ctx)
	}
	if !eng.Usable {
		if r.cfg.Sys.GOOS() == "windows" && r.cfg.VirtualizationOff != nil && r.cfg.VirtualizationOff() {
			return "", errors.New("Virtualization is turned off in this PC's firmware (BIOS or UEFI). Turn it on there, then check again; crsp cannot change it") //nolint:staticcheck // a sentence for the user
		}
		why := eng.Reason
		if why == "" {
			why = "it does not answer"
		}
		return "", fmt.Errorf("The container engine is not working: %s.", strings.TrimSuffix(why, ".")) //nolint:staticcheck // a sentence for the user
	}
	if eng.ContainerOS != "linux" {
		got := eng.ContainerOS
		if got == "" {
			got = "an unknown kind of"
		}
		return "", fmt.Errorf("The engine runs %s containers, but Linux containers are needed. crsp does not switch it; switch it in Docker Desktop yourself or use \"Do it myself\".", got) //nolint:staticcheck // a sentence for the user
	}
	prefix := eng.DockerPrefix
	if len(prefix) == 0 {
		prefix = []string{"docker"}
	}
	argv := append(append([]string(nil), prefix...), "run", "--rm", r.cfg.TestImage)
	code, err := r.run(ctx, j, "verify", argv)
	if err != nil {
		return "", fmt.Errorf("The test container could not start: %s", r.redact(err.Error())) //nolint:staticcheck // a sentence for the user
	}
	if code != 0 {
		return "", fmt.Errorf("The test container failed with code %d, so the engine is not ready.%s", code, j.lastWords()) //nolint:staticcheck // a sentence for the user
	}
	j.status("verify", "The test container ran and was removed. Docker "+eng.Version+" works.")
	return eng.Version, nil
}

// RestartHost restarts the machine after the user chose Restart now. It
// refuses while a job runs (D-DEP-2, DEP7) and sends one final event.
func (r *Runner) RestartHost(jobID, name, role string) {
	j := &job{r: r, id: jobID, name: name, role: role, os: r.cfg.Sys.GOOS(), phases: []string{"restart"}}
	if n := r.cfg.Sys.ActiveJobs(); n > 0 {
		j.final("restart", "failed", fmt.Sprintf("%d jobs still run on this machine. crsp restarts it only when none runs; try again when they are done.", n), "")
		return
	}
	j.status("restart", "Restarting this machine in about a minute.")
	if err := r.cfg.Sys.Reboot(); err != nil {
		j.final("restart", "failed", "The restart could not be started: "+err.Error(), "")
		return
	}
	j.final("restart", "done", "", "")
}
