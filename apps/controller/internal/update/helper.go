package update

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Plan is what the controller hands the helper process. The helper outlives
// the service it stops, so it carries everything in this file and holds no
// state of the controller's.
type Plan struct {
	Kind          string        `json:"kind"` // "update" or "restart"
	GOOS          string        `json:"goos"`
	InstallKind   string        `json:"install_kind"`
	From          string        `json:"from"`
	To            string        `json:"to"`
	File          string        `json:"file"`    // update: the MSI (windows) or the verified new binary (unix)
	Exe           string        `json:"exe"`     // unix: the running binary to replace
	Service       string        `json:"service"` // OS service name
	HealthURL     string        `json:"health_url"`
	HealthTimeout time.Duration `json:"health_timeout"`
	Dir           string        `json:"dir"` // work folder: lock, log, result, failures
	Attempt       string        `json:"attempt,omitempty"`
}

// Env is what the helper uses from the machine; tests replace it.
type Env struct {
	// Exec runs a command to the end and returns its exit code. A command that
	// cannot start returns an error.
	Exec   func(ctx context.Context, name string, args ...string) (int, error)
	Now    func() time.Time
	Sleep  func(time.Duration)
	Health func(ctx context.Context, url string) (version string, err error)
	Log    io.Writer
}

func (e *Env) fill() {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Sleep == nil {
		e.Sleep = time.Sleep
	}
	if e.Health == nil {
		e.Health = httpHealth
	}
	if e.Log == nil {
		e.Log = io.Discard
	}
	if e.Exec == nil {
		e.Exec = osExec
	}
}

func (e *Env) logf(format string, a ...any) {
	_, _ = fmt.Fprintf(e.Log, e.Now().Format(time.RFC3339)+" "+format+"\n", a...)
}

// msiOK are the exit codes of msiexec that mean the install worked (3010: a
// restart is pending, which /norestart leaves to the owner).
func msiOK(code int) bool { return code == 0 || code == 3010 || code == 1641 }

// RunPlan carries out a plan: it installs (or restarts), waits for the service
// to report the expected version, and on unix rolls back to the previous binary
// when it does not. It always writes the result and releases the lock.
func RunPlan(ctx context.Context, p Plan, env Env) error {
	env.fill()
	err := runPlan(ctx, p, &env)
	res := Result{OK: err == nil, Kind: p.Kind, From: p.From, To: p.To, At: env.Now(), Attempt: p.Attempt}
	stage := Stage{Name: StageDone, Percent: 100, Version: p.To, At: env.Now()}
	if err != nil {
		stage = Stage{Name: StageFailed, Percent: -1, Version: p.To, Error: err.Error(), LogPath: filepath.Join(p.Dir, LogFile), FailedAt: StageInstalling, At: env.Now()}
	}
	if prev, ok := ReadStage(p.Dir); ok {
		stage.StartedBy = prev.StartedBy
		stage.Attempt, stage.AttemptKind, stage.AttemptStarted = prev.Attempt, prev.AttemptKind, prev.AttemptStarted
	}
	defer func() { _ = WriteStage(p.Dir, stage) }()
	if err != nil {
		res.Error = err.Error()
		bad := errors.Is(err, errNotHealthy)
		_ = RecordFailure(p.Dir, p.To, env.Now(), bad)
		env.logf("update failed: %v", err)
	} else {
		env.logf("update done: %s", p.To)
	}
	_ = WriteResult(p.Dir, res)
	(&Lock{path: filepath.Join(p.Dir, LockFile)}).Release()
	return err
}

var errNotHealthy = errors.New("the new build did not report healthy in time")

func runPlan(ctx context.Context, p Plan, env *Env) error {
	first := Stage{Name: StageInstalling, Percent: -1, Version: p.To, At: env.Now()}
	if prev, ok := ReadStage(p.Dir); ok {
		first.StartedBy = prev.StartedBy
		first.Attempt, first.AttemptKind, first.AttemptStarted = prev.Attempt, prev.AttemptKind, prev.AttemptStarted
	}
	_ = WriteStage(p.Dir, first)
	want := p.To
	if p.Kind == "restart" {
		want = p.From
	}
	if p.Kind == "update" && (p.InstallKind == KindPKG || p.InstallKind == KindDEB || p.InstallKind == KindRPM) {
		if err := installPackage(ctx, p, env); err != nil {
			return err
		}
		if err := restartService(ctx, p, env); err != nil {
			return err
		}
		return waitHealthy(ctx, p, env, want)
	}
	switch p.GOOS {
	case "windows":
		if p.Kind == "update" {
			return errors.New("a Windows update is started by the controller itself, not by the helper")
		}
		if err := restartService(ctx, p, env); err != nil {
			return err
		}
		return waitHealthy(ctx, p, env, want)
	default:
		var prev string
		if p.Kind == "update" {
			prev = p.Exe + ".previous"
			if err := copyFile(p.Exe, prev, 0o755); err != nil {
				return fmt.Errorf("keep the previous binary: %w", err)
			}
			if err := os.Rename(p.File, p.Exe); err != nil {
				return fmt.Errorf("put the new binary in place: %w", err)
			}
		}
		if err := restartService(ctx, p, env); err != nil {
			return rollback(ctx, p, env, prev, err)
		}
		if err := waitHealthy(ctx, p, env, want); err != nil {
			return rollback(ctx, p, env, prev, err)
		}
		return nil
	}
}

// rollback puts the previous binary back and restarts, then reports the cause.
func rollback(ctx context.Context, p Plan, env *Env, prev string, cause error) error {
	if prev == "" {
		return cause
	}
	env.logf("rolling back to the previous binary: %v", cause)
	if err := os.Rename(prev, p.Exe); err != nil {
		return fmt.Errorf("%w; and the previous binary could not be put back: %v", cause, err)
	}
	if err := restartService(ctx, p, env); err != nil {
		return fmt.Errorf("%w; and the old service did not restart: %v", cause, err)
	}
	return fmt.Errorf("%w (the previous build is back)", cause)
}

func restartService(ctx context.Context, p Plan, env *Env) error {
	var name string
	var args []string
	switch p.GOOS {
	case "windows":
		// Restart through the service control manager; msiexec already restarts it after an upgrade.
		name, args = "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", "Restart-Service -Name '" + p.Service + "' -Force"}
	case "darwin":
		name, args = "launchctl", []string{"kickstart", "-k", "system/" + p.Service}
	default:
		name, args = "systemctl", []string{"restart", p.Service}
	}
	code, err := env.Exec(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("could not restart the service: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("restarting the service failed with exit code %d", code)
	}
	return nil
}

func waitHealthy(ctx context.Context, p Plan, env *Env, want string) error {
	if p.HealthURL == "" {
		return waitServiceActive(ctx, p, env)
	}
	limit := p.HealthTimeout
	if limit <= 0 {
		limit = 60 * time.Second
	}
	deadline := env.Now().Add(limit)
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		v, err := env.Health(hctx, p.HealthURL)
		cancel()
		if err == nil && v == want {
			return nil
		}
		if !env.Now().Before(deadline) || ctx.Err() != nil {
			return errNotHealthy
		}
		env.Sleep(2 * time.Second)
	}
}

func httpHealth(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := http.DefaultClient
	if req.URL.Scheme == "https" {
		// The console off loopback is TLS with the controller's own CA. This is
		// a health read of this machine's own port, with no data sent and none
		// trusted beyond the version string.
		client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}}} //nolint:gosec // local health read
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("health answered %d", resp.StatusCode)
	}
	var h struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&h); err != nil {
		return "", err
	}
	if h.Status != "ok" {
		return "", fmt.Errorf("health says %q", h.Status)
	}
	return h.Version, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // the running binary
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // next to the binary
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// RunHelper is the hidden `crsp update-helper PLAN` mode: read the plan file
// and carry it out. It returns the process exit code.
func RunHelper(ctx context.Context, args []string, logw io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(logw, "usage: crsp update-helper PLAN.json")
		return 2
	}
	b, err := os.ReadFile(args[0]) //nolint:gosec // written by the controller
	if err != nil {
		_, _ = fmt.Fprintln(logw, "error:", err)
		return 1
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		_, _ = fmt.Fprintln(logw, "error: bad plan:", err)
		return 1
	}
	lf, err := os.OpenFile(filepath.Join(p.Dir, LogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // work folder
	if err == nil {
		defer func() { _ = lf.Close() }()
		logw = io.MultiWriter(logw, lf)
	}
	if err := RunPlan(ctx, p, Env{Log: logw}); err != nil {
		return 1
	}
	return 0
}

// installPackage installs a native package over the old one: pkg with
// installer, deb with apt-get (dpkg -i when apt-get cannot), rpm with rpm -U.
func installPackage(ctx context.Context, p Plan, env *Env) error {
	var tries [][]string
	switch p.InstallKind {
	case KindPKG:
		tries = [][]string{{"installer", "-pkg", p.File, "-target", "/"}}
	case KindDEB:
		tries = [][]string{{"apt-get", "install", "-y", aptPath(p.File)}, {"dpkg", "-i", p.File}}
	case KindRPM:
		tries = [][]string{{"rpm", "-U", p.File}}
	}
	var last error
	for _, c := range tries {
		code, err := env.Exec(ctx, c[0], c[1:]...)
		if err == nil && code == 0 {
			return nil
		}
		if err != nil {
			last = fmt.Errorf("%s: %w", c[0], err)
		} else {
			last = fmt.Errorf("%s failed with exit code %d", c[0], code)
		}
	}
	return fmt.Errorf("the package install failed, the old version stays: %w", last)
}

// aptPath makes apt read the argument as a file, not a package name.
func aptPath(f string) string {
	if filepath.IsAbs(f) || strings.HasPrefix(f, "./") {
		return f
	}
	return "./" + f
}
