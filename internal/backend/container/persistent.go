package container

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/images"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/hygiene"
)

// The persistent Linux runner on a Windows machine (runners R5.93): one
// long-lived container per pool on the machine's container engine, restarted by
// the engine unless stopped, with the caps of the machine's pool size. A
// supervisor inside runs one runner per entry and starts a dead one again with
// back-off. The runners register with `config.sh --unattended --replace`, never
// with --ephemeral, and their hooks clean the job's temporary files before and
// after every job (runners R5.91). The files are in images/persistent.

// Names of the persistent pool container.
const (
	// PersistentPool is the container of the machine's persistent Linux runners.
	PersistentPool = "crsp-persistent-pool"
	// LabelPersistent marks the container, so the agent tells it from a job pool.
	LabelPersistent = "crsp.persistent"
	// StateDir holds the runner folders and the supervisor scripts; DepsDir one
	// dependency folder per runner (runners R5.90).
	StateDir = "/state"
	DepsDir  = "/deps"
)

// PersistentSpec is the persistent pool container.
type PersistentSpec struct {
	ImageTag string
	ImageKey string
	Limits   Limits
	CPUSet   string
	// UID is the pool's persistent uid (runners R5.72); 0 means JobUID.
	UID int
}

// PersistentRunArgs builds the `docker run` arguments of the persistent pool
// container, checked by CheckRunArgs: no --privileged, no engine socket, not
// root, a read-only root with tmpfs work dirs. Only two named volumes are
// writable besides: the runner folders and the dependency folders.
func PersistentRunArgs(p PersistentSpec) ([]string, error) {
	if err := ValidateImageRef(p.ImageTag); err != nil {
		return nil, err
	}
	uid := p.UID
	if uid == 0 {
		uid = JobUID
	}
	a := []string{"run", "-d", "--name", PersistentPool, "--hostname", PersistentPool,
		"--restart", "unless-stopped", "--init",
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(uid),
		"--cap-drop", CapDrop,
		"--security-opt", "no-new-privileges",
		"--pids-limit", "8192",
		"--read-only",
		"--label", LabelPersistent + "=1",
		"--label", LabelImage + "=" + p.ImageKey,
	}
	a = append(a, workTmpfsArgs(false, uid)...)
	a = append(a, ResourceArgs(p.Limits.MemoryMB, p.CPUSet)...)
	if p.Limits.CPUs > 0 {
		a = append(a, "--cpus", strconv.FormatFloat(p.Limits.CPUs, 'f', -1, 64))
	}
	a = append(a, "-v", PersistentPool+"-state:"+StateDir, "-v", PersistentPool+"-deps:"+DepsDir)
	a = append(a, p.ImageTag, "sh", "-c",
		`until [ -f `+StateDir+`/bin/pool-supervisor.sh ]; do sleep 1; done; exec sh `+StateDir+`/bin/pool-supervisor.sh`)
	if err := CheckRunArgs(a); err != nil {
		return nil, err
	}
	return a, nil
}

// ownVolumesArgs makes the two named volumes the pool user's. It is a one-shot
// container that only changes the owner: no network, a read-only root, and the
// one capability CHOWN. It is not a job container and never runs a job.
func ownVolumesArgs(image string, uid int) []string {
	own := fmt.Sprintf("%d:%d", uid, uid)
	return []string{"run", "--rm", "--network", "none", "--read-only", "--cap-drop", CapDrop, "--cap-add", "CHOWN",
		"--security-opt", "no-new-privileges", "--user", "0:0",
		"-v", PersistentPool + "-state:" + StateDir, "-v", PersistentPool + "-deps:" + DepsDir,
		image, "chown", own, StateDir, DepsDir}
}

// persistentFiles are the scripts written into the state volume.
var persistentFiles = []string{"clean.sh", "job-started.sh", "job-completed.sh", "pool-supervisor.sh", "add-runner.sh", "report.sh"}

// IsPersistentPool tells the persistent pool's ref from a job pool's.
func IsPersistentPool(pool string) bool { return pool == PersistentPool }

// StartPersistent makes sure the pool container runs, registers the runner and
// hands it to the supervisor. The registration token goes in on standard input
// only (runners R6.2).
func (b *Backend) StartPersistent(ctx context.Context, r backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	b.learn(cfg.Registration, cfg.Removal)
	h := backend.Handle{RunnerID: r.ID, Ref: PersistentPool + ":" + r.Name}
	if cfg.Registration == "" {
		return h, errors.New("a persistent runner needs a registration token")
	}
	if cfg.JITConfig != "" {
		return h, errors.New("a persistent runner is never started from a JIT config")
	}
	if r.OS != "" && r.OS != "linux" {
		return h, fmt.Errorf("the container backend runs Linux jobs only, not %s", r.OS)
	}
	if !safeName.MatchString(r.Name) {
		return h, fmt.Errorf("runner name %q cannot be used in a container", r.Name)
	}
	// The image comes the same way as a job pool's: local, registry, build
	// (REG2). It is done before the lock: a pull takes the lock to install the CA.
	r, err := b.withImage(ctx, r)
	if err != nil {
		return h, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensurePersistentPool(ctx, r); err != nil {
		return h, err
	}
	keep := "1"
	if !r.KeepDependencies {
		keep = "0"
	}
	if err := b.step(ctx, "register", []string{"exec", "-i", PersistentPool, "sh", StateDir + "/bin/add-runner.sh",
		r.Name, r.GitHubURL, strings.Join(r.Labels, ","), r.Group, keep}, cfg.Registration+"\n"); err != nil {
		return h, err
	}
	b.log("start", "persistent runner "+r.Name+" added to "+PersistentPool)
	return h, nil
}

// withImage gives a runner spec without an image the image of its layers, through
// PrepareImage (local, then registry, then build), as LocalAgent does for a job
// pool. A spec that has an image keeps it.
func (b *Backend) withImage(ctx context.Context, r backend.RunnerSpec) (backend.RunnerSpec, error) {
	if r.Image.ID != "" {
		return r, nil
	}
	img, err := b.PrepareImage(ctx, backend.ImageSpec{OS: r.OS, Arch: r.Arch, Layers: r.Pool.Layers, Extras: r.ExtraPackages})
	if err != nil {
		return r, err
	}
	r.Image = img
	return r, nil
}

func (b *Backend) ensurePersistentPool(ctx context.Context, r backend.RunnerSpec) error {
	exists, running, _, err := b.poolState(ctx, PersistentPool)
	if err != nil {
		return err
	}
	if exists && !running {
		if err := b.step(ctx, "pool start", []string{"start", PersistentPool}, ""); err != nil {
			return err
		}
	}
	if exists {
		if err := b.repairPersistentVolumes(ctx); err != nil {
			return err
		}
	}
	if !exists {
		tag, key, err := b.persistentImage(ctx, r)
		if err != nil {
			return err
		}
		if err := ValidateImageRef(tag); err != nil {
			return err
		}
		uid, err := b.identity(ctx, PersistentPool)
		if err != nil {
			return err
		}
		if uid == 0 {
			uid = JobUID
		}
		if err := b.step(ctx, "pool volumes", ownVolumesArgs(tag, uid), ""); err != nil {
			return err
		}
		args, err := PersistentRunArgs(PersistentSpec{ImageTag: tag, ImageKey: key, Limits: b.limitsFor(r), UID: uid})
		if err != nil {
			return err
		}
		if err := b.step(ctx, "pool create", args, ""); err != nil {
			return err
		}
	}
	return b.writePersistentFiles(ctx)
}

// repairPersistentVolumes gives the two volumes of an existing pool to the uid
// the container was made with (its Config.User), the user every exec runs as.
// A volume left with another owner is repaired here, never left broken (OPS62).
func (b *Backend) repairPersistentVolumes(ctx context.Context) error {
	res, err := b.run(ctx, "inspect", "--format", "{{.Config.User}} {{.Config.Image}}", PersistentPool)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("inspect %s: %s", PersistentPool, b.redact(strings.TrimSpace(res.Stderr)))
	}
	f := strings.Fields(res.Stdout)
	if len(f) != 2 {
		return nil // no user or image to go by: nothing to repair
	}
	uidText, _, _ := strings.Cut(f[0], ":")
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid <= 0 {
		return nil
	}
	if err := ValidateImageRef(f[1]); err != nil {
		return nil
	}
	return b.step(ctx, "pool volumes", ownVolumesArgs(f[1], uid), "")
}

// persistentImage is the base image of the machine (the same one as a job
// pool's, runners R5.49).
func (b *Backend) persistentImage(ctx context.Context, r backend.RunnerSpec) (tag, key string, err error) {
	plan, err := PlanPool(b.cfg, r, "persistent", "pool", KVMFacts{})
	if err != nil {
		return "", "", err
	}
	if err := ValidateImageRef(plan.Pool.ImageTag); err != nil {
		return "", "", err
	}
	return plan.Pool.ImageTag, plan.Pool.ImageKey, nil
}

// Every exec into a pool omits -u: the engine then runs it as the container's
// Config.User, the uid:gid the pool was created with (identity(), runners
// R5.72), never as the image's `runner` user (uid 1001) (OPS62).

// writePersistentFiles puts the scripts into the state volume, each time the
// container is used, so a new crsp release brings its newer scripts.
func (b *Backend) writePersistentFiles(ctx context.Context) error {
	for _, n := range persistentFiles {
		body, err := images.FS.ReadFile("persistent/" + n)
		if err != nil {
			return err
		}
		if err := b.step(ctx, "pool files", []string{"exec", "-i", PersistentPool, "sh", "-c",
			`mkdir -p "$(dirname "$1")" && cat >"$1.new" && chmod 0755 "$1.new" && mv -f "$1.new" "$1"`, "sh", StateDir + "/bin/" + n}, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// stopPersistent holds the runner: the supervisor ends it and does not start it
// again until the marker is removed. A job that runs is never cut; callers
// drain first (runners R6.4).
func (b *Backend) stopPersistent(ctx context.Context, name string) error {
	return b.step(ctx, "stop", []string{"exec", PersistentPool, "sh", "-c", `: >"$1"`, "sh", StateDir + "/runners.d/" + name + ".stopped"}, "")
}

// cleanUpPersistent removes the runner's entry, its folder and its dependency
// folder; safe to call twice (runners R5.10).
func (b *Backend) cleanUpPersistent(ctx context.Context, name string) error {
	res, err := b.run(ctx, "exec", PersistentPool, "sh", "-c",
		`rm -f "$1/runners.d/$2.env" "$1/runners.d/$2.stopped"; sleep 6; rm -rf -- "$1/$2" "$3/$2"`, "sh", StateDir, name, DepsDir)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && !noSuchContainer(res.Stderr) && !strings.Contains(strings.ToLower(res.Stderr), "is not running") {
		return fmt.Errorf("clean up: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	return nil
}

// Deregister removes the runner from GitHub with a remove token (runners R5.19).
func (b *Backend) Deregister(ctx context.Context, h backend.Handle, removeToken string) error {
	pool, name, ok := splitRef(h.Ref)
	if !ok || !IsPersistentPool(pool) {
		return nil
	}
	b.learn(removeToken)
	if err := b.stopPersistent(ctx, name); err != nil {
		return err
	}
	return b.step(ctx, "deregister", []string{"exec", "-i", PersistentPool, "sh", "-c",
		`IFS= read -r ACTIONS_RUNNER_INPUT_TOKEN && export ACTIONS_RUNNER_INPUT_TOKEN && cd "$1/$2" && ./config.sh remove`, "sh", StateDir, name}, removeToken+"\n")
}

// PersistentState is one persistent runner as the pool container reports it.
type PersistentState struct {
	Name            string
	DependencyBytes int64
	UncleanReason   string
}

// PersistentStates reads the clean-up state and the dependency size of every
// persistent runner of the container (runners R5.90, R5.91). A container that
// is not there has none.
func (b *Backend) PersistentStates(ctx context.Context) ([]PersistentState, error) {
	res, err := b.run(ctx, "exec", PersistentPool, "sh", StateDir+"/bin/report.sh")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if noSuchContainer(res.Stderr) || strings.Contains(strings.ToLower(res.Stderr), "is not running") {
			return nil, nil
		}
		return nil, fmt.Errorf("persistent report: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	return ParsePersistentStates(res.Stdout), nil
}

// ParsePersistentStates reads the lines of report.sh: NAME|BYTES|REASON.
func ParsePersistentStates(out string) []PersistentState {
	var res []PersistentState
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.SplitN(strings.TrimSpace(ln), "|", 3)
		if len(f) < 3 || f[0] == "" {
			continue
		}
		n, _ := strconv.ParseInt(f[1], 10, 64)
		res = append(res, PersistentState{Name: f[0], DependencyBytes: n, UncleanReason: f[2]})
	}
	return res
}

// ApplyHygiene writes the setting Keep downloaded dependencies of one runner
// and, when asked, clears its dependency folder. The caller has checked the
// runner is idle (runners R5.90).
func (b *Backend) ApplyHygiene(ctx context.Context, name string, keep, clear bool) error {
	script := `printf '%s\n' "$3" >"$1/$2/crsp-hygiene.env"`
	args := []string{"exec", PersistentPool, "sh", "-c", script, "sh", StateDir, name, strings.TrimSpace(hygiene.SettingsText(keep))}
	if clear {
		args = []string{"exec", PersistentPool, "sh", "-c", script + `; for e in "$4/$2"/* "$4/$2"/.[!.]*; do rm -rf -- "$e"; done`,
			"sh", StateDir, name, strings.TrimSpace(hygiene.SettingsText(keep)), DepsDir}
	}
	return b.step(ctx, "hygiene", args, "")
}
