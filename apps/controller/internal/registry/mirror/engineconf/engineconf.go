// Package engineconf writes the pull-through mirror settings of the container
// engines on a pool machine (caches R5.25, registry R5.65): Docker's
// registry-mirrors for Docker Hub, containerd's hosts.toml and Podman's
// registries.conf drop-in for every upstream. It is the body of the agent's
// host step. Applying the same settings twice changes nothing on disk.
//
// The package has no Distribution import, so the agent binary stays small.
package engineconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
)

// Command is the first argument of the host command that runs this step.
const Command = "engine-mirror"

// PathPrefix is where the mirror lives on the controller's listener
// (mirror.PathPrefix; repeated so this package imports nothing).
const pathPrefix = "/mirror/"

// marker names a file as ours; a file without it is never changed or removed.
const marker = "# managed by crsp: registry mirror (registry R5.65)"

// Settings say which mirror the engines use.
type Settings struct {
	// Host is host:port of the controller's HTTPS listener.
	Host      string
	Enabled   bool
	Upstreams []string
}

// Paths are the engine configuration locations. A test points them into a folder.
type Paths struct {
	DockerDaemon     string // daemon.json
	ContainerdCerts  string // certs.d
	PodmanRegistries string // registries.conf.d
	SkipDocker       bool
	SkipContainerd   bool
	SkipPodman       bool
}

// DefaultPaths are the Linux locations. Windows and macOS use Docker Desktop's
// own setting and have no file step here.
func DefaultPaths() Paths {
	return Paths{
		DockerDaemon:     "/etc/docker/daemon.json",
		ContainerdCerts:  "/etc/containerd/certs.d",
		PodmanRegistries: "/etc/containers/registries.conf.d",
	}
}

// ParseArgs reads the arguments of the host command: host:port, on or off, and
// the comma separated upstream list.
func ParseArgs(args []string) (Settings, error) {
	if len(args) != 3 {
		return Settings{}, errors.New("engine-mirror needs host:port, on or off, and the upstream list")
	}
	s := Settings{Host: args[0]}
	switch args[1] {
	case "on":
		s.Enabled = true
	case "off":
	default:
		return Settings{}, fmt.Errorf("engine-mirror: %q is not on or off", args[1])
	}
	for _, u := range strings.Split(args[2], ",") {
		if u = strings.TrimSpace(u); u != "" {
			s.Upstreams = append(s.Upstreams, strings.ToLower(u))
		}
	}
	if s.Host == "" || strings.ContainsAny(s.Host, "/ \t\"\\") {
		return Settings{}, fmt.Errorf("engine-mirror: %q is not a host:port", s.Host)
	}
	for _, u := range s.Upstreams {
		if strings.ContainsAny(u, "/ \t\"\\") {
			return Settings{}, fmt.Errorf("engine-mirror: %q is not a registry host", u)
		}
	}
	return s, nil
}

// mirrorURL is where engines reach one upstream's mirror.
func (s Settings) mirrorURL(up string) string { return "https://" + s.Host + pathPrefix + up }

// serverURL is the upstream's own endpoint, the fallback of a mirror entry.
func serverURL(up string) string { return addresses.RegistryAPIURL(up) }

func (s Settings) active() []string {
	if !s.Enabled {
		return nil
	}
	out := append([]string{}, s.Upstreams...)
	sort.Strings(out)
	return out
}

// Apply writes the engine files and returns the files it changed (none when the
// settings are already in place). The caller restarts an engine only when a
// file of it changed.
func Apply(p Paths, s Settings) ([]string, error) {
	var changed []string
	add := func(path string, c bool, err error) error {
		if err != nil {
			return err
		}
		if c {
			changed = append(changed, path)
		}
		return nil
	}
	if !p.SkipDocker && p.DockerDaemon != "" {
		c, err := applyDocker(p.DockerDaemon, s)
		if err := add(p.DockerDaemon, c, err); err != nil {
			return changed, err
		}
	}
	if !p.SkipContainerd && p.ContainerdCerts != "" {
		c, err := applyContainerd(p.ContainerdCerts, s)
		if err != nil {
			return changed, err
		}
		changed = append(changed, c...)
	}
	if !p.SkipPodman && p.PodmanRegistries != "" {
		f := filepath.Join(p.PodmanRegistries, "50-crsp-mirror.conf")
		c, err := applyPodman(f, s)
		if err := add(f, c, err); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// ---------------------------------------------------------------- Docker

func isOurs(entry, host string) bool {
	return strings.HasPrefix(entry, "https://"+host+pathPrefix)
}

func applyDocker(path string, s Settings) (bool, error) {
	cur := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case err == nil && len(bytes.TrimSpace(b)) > 0:
		if err := json.Unmarshal(b, &cur); err != nil {
			return false, fmt.Errorf("%s is not valid JSON; left alone: %w", path, err)
		}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, err
	case err != nil && !s.Enabled:
		return false, nil // no file and nothing to write
	}
	var keep []any
	if old, ok := cur["registry-mirrors"].([]any); ok {
		for _, e := range old {
			if str, ok := e.(string); ok && isOurs(str, s.Host) {
				continue
			}
			keep = append(keep, e)
		}
	}
	for _, up := range s.active() {
		if up == "docker.io" {
			// Docker's mirror setting is for Docker Hub only.
			keep = append(keep, s.mirrorURL(up))
		}
	}
	next := map[string]any{}
	for k, v := range cur {
		next[k] = v
	}
	if len(keep) > 0 {
		next["registry-mirrors"] = keep
	} else {
		delete(next, "registry-mirrors")
	}
	if reflect.DeepEqual(normalize(cur), normalize(next)) {
		return false, nil
	}
	out, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeFile(path, append(out, '\n'))
}

// normalize makes a JSON value comparable whatever its Go types.
func normalize(m map[string]any) any {
	b, _ := json.Marshal(m)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

// ---------------------------------------------------------------- containerd

func hostsToml(s Settings, up string) string {
	return fmt.Sprintf("%s\nserver = %q\n\n[host.%q]\n  capabilities = [\"pull\", \"resolve\"]\n  override_path = true\n",
		marker, serverURL(up), s.mirrorURL(up)+"/v2")
}

func applyContainerd(dir string, s Settings) ([]string, error) {
	var changed []string
	want := map[string]bool{}
	for _, up := range s.active() {
		want[up] = true
		f := filepath.Join(dir, up, "hosts.toml")
		c, err := writeIfChanged(f, []byte(hostsToml(s, up)), true)
		if err != nil {
			return changed, err
		}
		if c {
			changed = append(changed, f)
		}
	}
	// Remove the files of upstreams that are no longer mirrored, ours only.
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return changed, nil
	}
	if err != nil {
		return changed, err
	}
	for _, e := range entries {
		if !e.IsDir() || want[e.Name()] {
			continue
		}
		f := filepath.Join(dir, e.Name(), "hosts.toml")
		b, err := os.ReadFile(f)
		if err != nil || !bytes.HasPrefix(b, []byte(marker)) {
			continue
		}
		if err := os.Remove(f); err != nil {
			return changed, err
		}
		_ = os.Remove(filepath.Join(dir, e.Name())) // only when empty
		changed = append(changed, f)
	}
	return changed, nil
}

// ---------------------------------------------------------------- Podman

func registriesConf(s Settings) string {
	var b strings.Builder
	b.WriteString(marker + "\n")
	for _, up := range s.active() {
		fmt.Fprintf(&b, "\n[[registry]]\nprefix = %q\nlocation = %q\n\n[[registry.mirror]]\nlocation = %q\n",
			up, up, s.Host+pathPrefix+up)
	}
	return b.String()
}

func applyPodman(file string, s Settings) (bool, error) {
	if len(s.active()) == 0 {
		b, err := os.ReadFile(file)
		if err != nil || !bytes.HasPrefix(b, []byte(marker)) {
			return false, nil
		}
		return true, os.Remove(file)
	}
	return writeIfChanged(file, []byte(registriesConf(s)), true)
}

// ---------------------------------------------------------------- files

// writeIfChanged writes b unless the file already holds it. A file that exists
// without our marker is left alone when ownOnly is set.
func writeIfChanged(path string, b []byte, ownOnly bool) (bool, error) {
	cur, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(cur, b) {
			return false, nil
		}
		if ownOnly && !bytes.HasPrefix(cur, []byte(marker)) {
			return false, fmt.Errorf("%s exists and is not managed by crsp; left alone", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, writeFile(path, b)
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // engine config folders are world readable
		return err
	}
	tmp := path + ".crsp-tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil { //nolint:gosec // engine config is world readable
		return err
	}
	return os.Rename(tmp, path)
}
