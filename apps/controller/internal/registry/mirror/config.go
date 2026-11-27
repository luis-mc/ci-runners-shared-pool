// Package mirror is the pull-through mirror of upstream base images (registry
// R5.63 to R5.67, caches R5.24 to R5.26): one Distribution instance in proxy
// mode per upstream, each with its own folder under the mirror folder, served
// on the controller's listener under PathPrefix. Distribution proxies exactly
// one remote per instance, so docker.io, ghcr.io and the others each get one.
package mirror

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/distribution/v3/configuration"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/fsdriver"
)

// PathPrefix is where the mirror lives on the controller's listener. The main
// registry owns /v2/ (REG1); the mirror never shares that route.
const PathPrefix = "/mirror/"

// FolderName is the default folder of the mirror under the data directory (R5.64).
const FolderName = "registry-mirror"

// DefaultUpstreams are the mirrored registries until the owner changes them (R5.63).
var DefaultUpstreams = []string{"docker.io", "ghcr.io", "mcr.microsoft.com", "quay.io"}

// DefaultTagRecheck is how long a mirrored tag is trusted (R5.66).
const DefaultTagRecheck = 60 * time.Minute

// MaxTagRecheck is the largest re-check time the settings accept (one week).
const MaxTagRecheck = 7 * 24 * time.Hour

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// ErrInvalid is returned for settings the mirror cannot run with.
var ErrInvalid = errors.New("invalid mirror settings")

// NormalizeUpstream lower-cases a registry host and checks its shape: a host
// name and an optional port, no scheme, no path.
func NormalizeUpstream(u string) (string, error) {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "" || !hostRe.MatchString(u) {
		return "", fmt.Errorf("%w: %q is not a registry host (use host or host:port)", ErrInvalid, u)
	}
	return u, nil
}

// Validate checks the settings and returns them with upstreams normalised, with
// and with no duplicate (the folder is left for the caller, which knows the data
// directory).
func Validate(s registry.MirrorSettings) (registry.MirrorSettings, error) {
	seen := map[string]bool{}
	ups := make([]string, 0, len(s.Upstreams))
	for _, u := range s.Upstreams {
		h, err := NormalizeUpstream(u)
		if err != nil {
			return s, err
		}
		if !seen[h] {
			seen[h] = true
			ups = append(ups, h)
		}
	}
	s.Upstreams = ups
	if s.Enabled && len(ups) == 0 {
		return s, fmt.Errorf("%w: name at least one upstream registry", ErrInvalid)
	}
	if s.BudgetBytes < 0 {
		return s, fmt.Errorf("%w: the budget cannot be negative", ErrInvalid)
	}
	if s.TagRecheck < time.Minute || s.TagRecheck > MaxTagRecheck {
		return s, fmt.Errorf("%w: the tag re-check time is 1 minute to 7 days", ErrInvalid)
	}
	return s, nil
}

// Folder is the mirror folder: the setting, or <dataDir>/registry-mirror (R5.64).
func Folder(s registry.MirrorSettings, dataDir string) string {
	if strings.TrimSpace(s.Folder) != "" {
		return filepath.Clean(s.Folder)
	}
	return filepath.Join(dataDir, FolderName)
}

// UpstreamDir is the storage folder of one upstream.
func UpstreamDir(folder, upstream string) string {
	return filepath.Join(folder, strings.ReplaceAll(upstream, ":", "_"))
}

// RemoteURL is the API endpoint of an upstream: Docker Hub's registry API is not
// on docker.io, every other registry answers on its own host.
func RemoteURL(upstream string) string { return addresses.RegistryAPIURL(upstream) }

// ProxyConfig is the Distribution configuration of one upstream in proxy mode.
// The instance is never reachable on its own: the controller serves it under
// PathPrefix behind the registry token check (R5.67).
func ProxyConfig(s registry.MirrorSettings, dataDir, upstream string) (*configuration.Configuration, error) {
	h, err := NormalizeUpstream(upstream)
	if err != nil {
		return nil, err
	}
	// Content expires after the longest time a cached tag could still be asked
	// for; the tag itself is re-checked upstream by the proxy, and a cached tag is
	// served when the upstream cannot be reached (R5.66).
	ttl := 7 * 24 * time.Hour
	if s.TagRecheck > ttl {
		ttl = s.TagRecheck
	}
	cfg := &configuration.Configuration{
		Version: "0.1",
		Storage: configuration.Storage{
			fsdriver.Name: configuration.Parameters{"rootdirectory": UpstreamDir(Folder(s, dataDir), h)},
			"cache":       configuration.Parameters{"blobdescriptor": "inmemory"},
		},
		Proxy: configuration.Proxy{RemoteURL: RemoteURL(h), TTL: &ttl},
	}
	cfg.Log.Level = "warn"
	cfg.Log.AccessLog.Disabled = true
	cfg.HTTP.Addr = net.JoinHostPort("127.0.0.1", "0") // never bound: the handler is mounted, not listened on
	return cfg, nil
}
