package hooks

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// The job-started hook points Cargo and apt at the hub when the runner got
// the hub's values at start (caches R5.81, R5.82). Neither tool can take this
// from the environment alone: Cargo's source replacement and apt's sources
// are files. Every step is best effort and never fails the job: a file the
// runner user may not write is skipped and said in the agent log.

// HubFiles says where the hook writes. Empty fields take the host's own
// places.
type HubFiles struct {
	// AptDir is /etc/apt; set to "-" to skip apt (not Linux).
	AptDir string
	// CargoHome is $CARGO_HOME, else ~/.cargo.
	CargoHome string
}

func (f HubFiles) aptDir() string {
	if f.AptDir != "" {
		return f.AptDir
	}
	if runtime.GOOS != "linux" {
		return "-"
	}
	return "/etc/apt"
}

// ApplyHubConfig writes the Cargo config and the apt files for one job from
// the runner's environment. It returns the lines it wants in the agent log.
func ApplyHubConfig(getenv func(string) string, f HubFiles) []string {
	var out []string
	if idx := getenv(lancache.EnvCargoIndex); idx != "" {
		home := f.CargoHome
		if home == "" {
			home = getenv("CARGO_HOME")
		}
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = filepath.Join(h, ".cargo")
			}
		}
		if home != "" {
			if err := writeCargo(filepath.Join(home, "config.toml"), idx, getenv(lancache.EnvCargoCAInfo)); err != nil {
				out = append(out, "job-started: cargo hub config skipped: "+err.Error())
			}
		}
	}
	if mirror, ca := getenv(lancache.EnvAptMirror), getenv(lancache.EnvHubCA); mirror != "" && ca != "" && f.aptDir() != "-" {
		if err := writeApt(f.aptDir(), mirror, ca); err != nil {
			out = append(out, "job-started: apt hub config skipped: "+err.Error())
		}
	}
	return out
}

// writeCargo writes path unless a config of the user's own is there.
func writeCargo(path, index, ca string) error {
	if old, err := os.ReadFile(path); err == nil && !strings.HasPrefix(string(old), lancache.CargoConfigMarker) {
		return fmt.Errorf("%s is the user's own", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the runner user's own Cargo folder
		return err
	}
	return writeIfChanged(path, lancache.CargoConfig(index, ca))
}

// writeApt writes the CA line and rewrites the sources of the allowed hosts.
func writeApt(dir, mirror, ca string) error {
	u, err := url.Parse(mirror)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad apt mirror address")
	}
	host := u.Hostname()
	if err := writeIfChanged(filepath.Join(dir, "apt.conf.d", "90crsp-hub"), lancache.AptConf(host, ca)); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "sources.list"))
	for _, pat := range []string{"*.list", "*.sources"} {
		m, _ := filepath.Glob(filepath.Join(dir, "sources.list.d", pat))
		files = append(files, m...)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if next := lancache.AptSources(string(b), u.Host, mirror); next != string(b) {
			if err := writeIfChanged(f, next); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeIfChanged(path, data string) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == data {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // apt and Cargo folders are world-readable
		return err
	}
	tmp := path + ".crsp.tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o644); err != nil { //nolint:gosec // apt and Cargo read these as other users
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
