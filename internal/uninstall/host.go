package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exec runs a program and returns what it printed. A program that is not installed returns ErrNotInstalled.
type Exec func(ctx context.Context, name string, args ...string) ([]byte, error)

// ErrNotInstalled says the program is not on this machine (Docker or WSL not installed): nothing to remove.
var ErrNotInstalled = errors.New("not installed")

// RealExec runs a program with os/exec.
func RealExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

// WSLDistro is the WSL distro crsp makes for Windows runners.
const WSLDistro = "crsp"

// Host is the real remove on this machine.
type Host struct {
	GOOS string
	// DataDirs are the folders deleted by a delete: the controller's data folder and the agent's.
	DataDirs []string
	// Services are stopped, in order (the agent first, then the controller).
	Services []string
	Exec     Exec
	// Runners unregisters the runners from GitHub through the controller; nil means no controller is known.
	Runners func(ctx context.Context) (int, error)
	// SystemLog writes a line to the system log (Windows event log, syslog); nil skips it.
	SystemLog func(ctx context.Context, line string) error
}

var _ Steps = (*Host)(nil)

func (h *Host) exec() Exec {
	if h.Exec != nil {
		return h.Exec
	}
	return RealExec
}

// UnregisterRunners asks the controller to unregister every runner from GitHub.
func (h *Host) UnregisterRunners(ctx context.Context) (int, error) {
	if h.Runners == nil {
		return 0, errors.New("no signed-in controller was found, so the runners stay registered in GitHub; delete them in the repository's runner settings")
	}
	return h.Runners(ctx)
}

// StopServices stops the crsp services; one that is not there or already stopped is not an error.
func (h *Host) StopServices(ctx context.Context) error {
	var errs []string
	for _, s := range h.Services {
		var out []byte
		var err error
		switch h.GOOS {
		case "windows":
			out, err = h.exec()(ctx, "net.exe", "stop", s, "/y")
		case "darwin":
			out, err = h.exec()(ctx, "launchctl", "bootout", "system/"+s)
		default:
			out, err = h.exec()(ctx, "systemctl", "stop", s)
		}
		if err != nil && !errors.Is(err, ErrNotInstalled) && !benign(string(out)) {
			errs = append(errs, s+": "+strings.TrimSpace(string(out)))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// benign reports the "was not there" answers of a stop.
func benign(out string) bool {
	o := strings.ToLower(strings.ReplaceAll(out, "\x00", ""))
	for _, m := range []string{"not started", "is not started", "does not exist", "not loaded", "could not be found", "not found", "unit crsp"} {
		if strings.Contains(o, m) {
			return true
		}
	}
	return false
}

func fields(b []byte) []string { return strings.Fields(strings.ReplaceAll(string(b), "\x00", "")) }

// RemoveContainers removes the containers crsp made: those labelled by crsp and those named crsp-*.
func (h *Host) RemoveContainers(ctx context.Context) (int, error) {
	ids := map[string]bool{}
	for _, filter := range []string{"label=crsp.image-key", "label=crsp.pool", "label=crsp.persistent", "name=^crsp-"} {
		out, err := h.exec()(ctx, "docker", "ps", "-aq", "--filter", filter)
		if errors.Is(err, ErrNotInstalled) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("docker ps: %s", strings.TrimSpace(string(out)))
		}
		for _, id := range fields(out) {
			ids[id] = true
		}
	}
	return h.dockerRemove(ctx, ids, []string{"rm", "-f", "-v"})
}

// RemoveImages removes the runner images crsp made (crsp-*).
func (h *Host) RemoveImages(ctx context.Context) (int, error) {
	out, err := h.exec()(ctx, "docker", "image", "ls", "--filter", "reference=crsp-*", "-q")
	if errors.Is(err, ErrNotInstalled) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("docker image ls: %s", strings.TrimSpace(string(out)))
	}
	ids := map[string]bool{}
	for _, id := range fields(out) {
		ids[id] = true
	}
	return h.dockerRemove(ctx, ids, []string{"image", "rm", "-f"})
}

func (h *Host) dockerRemove(ctx context.Context, ids map[string]bool, verb []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := append([]string{}, verb...)
	for id := range ids {
		args = append(args, id)
	}
	if out, err := h.exec()(ctx, "docker", args...); err != nil {
		return 0, fmt.Errorf("docker %s: %s", verb[0], strings.TrimSpace(string(out)))
	}
	return len(ids), nil
}

// UnregisterWSL removes the crsp WSL distro on Windows; a machine with no WSL or no such distro is fine.
func (h *Host) UnregisterWSL(ctx context.Context) error {
	if h.GOOS != "windows" {
		return nil
	}
	out, err := h.exec()(ctx, "wsl.exe", "--unregister", WSLDistro)
	if err == nil || errors.Is(err, ErrNotInstalled) {
		return nil
	}
	o := strings.ToLower(strings.ReplaceAll(string(out), "\x00", ""))
	for _, m := range []string{"no distribution", "there is no distribution", "wsl_e_distro_not_found", "not found", "has no installed distributions", "is not installed"} {
		if strings.Contains(o, m) {
			return nil
		}
	}
	return fmt.Errorf("wsl --unregister %s: %s", WSLDistro, strings.TrimSpace(strings.ReplaceAll(string(out), "\x00", "")))
}

// DeleteData deletes the data folders: settings, database, logs, caches, the certificate authority, backups.
func (h *Host) DeleteData(context.Context) error {
	var errs []string
	for _, d := range h.DataDirs {
		if err := removeAll(d); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Audit writes the row to the audit file in the data folder (kept when the data is kept) and to the system
// log (kept even when the data is deleted). It fails only when neither took the row.
func (h *Host) Audit(ctx context.Context, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var fileErr, logErr error
	wrote := false
	if len(h.DataDirs) > 0 && h.DataDirs[0] != "" && SafeDataDir(h.DataDirs[0]) == nil {
		dir := filepath.Join(h.DataDirs[0], "logs")
		if fileErr = os.MkdirAll(dir, 0o750); fileErr == nil {
			var f *os.File
			if f, fileErr = os.OpenFile(filepath.Join(dir, "remove-audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640); fileErr == nil {
				_, fileErr = f.Write(append(b, '\n'))
				if cerr := f.Close(); fileErr == nil {
					fileErr = cerr
				}
				wrote = fileErr == nil
			}
		}
	}
	if h.SystemLog != nil {
		if logErr = h.SystemLog(ctx, "crsp remove: "+string(b)); logErr == nil {
			wrote = true
		}
	}
	if wrote {
		return nil
	}
	return fmt.Errorf("no audit sink took the row (file: %v; system log: %v)", fileErr, logErr)
}

// SystemLogFor returns the system log writer of goos: the Windows Application event log (source crsp), or
// syslog through logger.
func SystemLogFor(goos string, run Exec) func(ctx context.Context, line string) error {
	if run == nil {
		run = RealExec
	}
	return func(ctx context.Context, line string) error {
		if len(line) > 30000 {
			line = line[:30000]
		}
		var out []byte
		var err error
		if goos == "windows" {
			out, err = run(ctx, "eventcreate.exe", "/L", "APPLICATION", "/T", "INFORMATION", "/SO", "crsp", "/ID", "100", "/D", line)
		} else {
			out, err = run(ctx, "logger", "-t", "crsp", line)
		}
		if err != nil {
			return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}
