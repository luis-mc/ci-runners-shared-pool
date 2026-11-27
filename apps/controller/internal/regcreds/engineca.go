package regcreds

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// The engine CA step of the image registry (registry R5.12): the container
// engine of a machine trusts the controller's CA for the registry's host:port.
// Docker reads <certs.d>/<host:port>/ca.crt of /etc/docker, Podman and Buildah
// read /etc/containers/certs.d/<host:port>/ca.crt, and Docker Desktop (macOS
// and Windows) reads ~/.docker/certs.d/<host>/ca.crt of the user.

// EngineCAFile is the name of the CA file inside the folder of a registry host.
const EngineCAFile = "ca.crt"

// HostFiles is the part of the machine's file system the step uses.
type HostFiles interface {
	MkdirAll(path string, perm fs.FileMode) error
	WriteFile(path string, data []byte, perm fs.FileMode) error
	ReadFile(path string) ([]byte, error)
	Exists(path string) bool
}

// EngineCAOptions describe one install.
type EngineCAOptions struct {
	// GOOS is the host system: linux, darwin or windows.
	GOOS string
	// Home is the home folder of the user whose Docker Desktop runs (darwin,
	// windows). Empty on Linux.
	Home string
	// Host is the registry host:port the engine pulls from.
	Host string
	// CA is the PEM of the controller's CA certificate, and nothing else.
	CA []byte
}

// EngineCATarget is one place the CA goes.
type EngineCATarget struct {
	Engine string // docker or podman
	Dir    string // the certs.d folder of the host
	File   string // Dir + ca.crt
}

// hostDirName is how an engine names the folder of a host:port. The Windows
// file system has no colon in a name, so Docker Desktop reads host_port there.
func hostDirName(goos, host string) string {
	if goos == "windows" {
		return strings.ReplaceAll(host, ":", "_")
	}
	return host
}

// EngineCATargets lists where the CA goes on this system. On Linux a target
// is listed only when its engine's folder (/etc/docker, /etc/containers)
// exists, and Docker's is listed when neither does, since the engine of a
// crsp machine is Docker unless Podman is there.
func EngineCATargets(f HostFiles, o EngineCAOptions) ([]EngineCATarget, error) {
	if !hostRe.MatchString(o.Host) {
		return nil, fmt.Errorf("%w: registry host %q", ErrInvalid, o.Host)
	}
	name := hostDirName(o.GOOS, o.Host)
	// The Linux engine's folders are Linux paths whatever system runs this code;
	// Docker Desktop's folder is a path of the host system.
	t := func(engine, certs string) EngineCATarget {
		join := filepath.Join
		if o.GOOS == "linux" {
			join = path.Join
		}
		d := join(certs, name)
		return EngineCATarget{Engine: engine, Dir: d, File: join(d, EngineCAFile)}
	}
	switch o.GOOS {
	case "linux":
		var out []EngineCATarget
		if f.Exists("/etc/containers") {
			out = append(out, t("podman", "/etc/containers/certs.d"))
		}
		if f.Exists("/etc/docker") || len(out) == 0 {
			out = append([]EngineCATarget{t("docker", "/etc/docker/certs.d")}, out...)
		}
		return out, nil
	case "darwin", "windows":
		if o.Home == "" {
			return nil, fmt.Errorf("%w: the home folder of the user is needed for Docker Desktop", ErrInvalid)
		}
		return []EngineCATarget{t("docker", filepath.Join(o.Home, ".docker", "certs.d"))}, nil
	}
	return nil, fmt.Errorf("%w: no engine CA folder on %q", ErrInvalid, o.GOOS)
}

// EngineCAResult says what an install did.
type EngineCAResult struct {
	Written   []string // files written now
	Unchanged []string // files that already held this CA
}

// InstallEngineCA writes the CA for the host into each target, one file each,
// and leaves a file that already holds the same bytes. The CA is public data
// (0644). It never changes an engine setting, a trust store or another file of
// the folder. A write that is refused (not root) is returned with the target
// so the caller can say what to do.
func InstallEngineCA(f HostFiles, o EngineCAOptions) (EngineCAResult, error) {
	var res EngineCAResult
	ca, err := ValidateBundle(o.CA)
	if err != nil {
		return res, err
	}
	targets, err := EngineCATargets(f, o)
	if err != nil {
		return res, err
	}
	for _, t := range targets {
		if have, rerr := f.ReadFile(t.File); rerr == nil && bytes.Equal(have, ca) {
			res.Unchanged = append(res.Unchanged, t.File)
			continue
		}
		if err := f.MkdirAll(t.Dir, 0o755); err != nil {
			return res, fmt.Errorf("make %s for %s: %w", t.Dir, t.Engine, err)
		}
		if err := f.WriteFile(t.File, ca, 0o644); err != nil {
			return res, fmt.Errorf("write %s for %s: %w", t.File, t.Engine, err)
		}
		res.Written = append(res.Written, t.File)
	}
	return res, nil
}
