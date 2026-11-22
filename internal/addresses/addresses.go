// Package addresses holds the external addresses crsp calls or links to, read
// from the CRSP_GITHUB_URL family of variables (operations OPS35). It is the
// one place that holds a default address; every other package receives the
// value through its struct. It imports no other package of this module, so any
// package can use it.
package addresses

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Environment variables that name the external addresses.
const (
	EnvGitHubURL         = "CRSP_GITHUB_URL"
	EnvGitHubAPIURL      = "CRSP_GITHUB_API_URL"
	EnvRunnerDownloadURL = "CRSP_RUNNER_DOWNLOAD_URL"
	EnvVMImageMirror     = "CRSP_VM_IMAGE_MIRROR"
)

// The defaults for github.com.
const (
	DefaultGitHubURL    = "https://github.com"
	DefaultGitHubAPIURL = "https://api.github.com"
	// DefaultVMImageMirror is empty: the VM images are fetched from the hosts
	// of the catalogue (internal/vmcheck/vm-images.json).
	DefaultVMImageMirror = ""

	// DockerHubRegistryURL is where Docker Hub's registry API answers; it is not
	// on docker.io. The pull-through mirror and the engine settings use it for
	// the upstream named docker.io (registry R5.63, R5.65).
	DockerHubRegistryURL = "https://registry-1.docker.io"

	// The download addresses of the tool store (runners R5.112, OPS75). Each is the base the install recipe
	// appends its file name to; a mirror replaces them through toolstore.Sources.
	DefaultRustupDistURL    = "https://static.rust-lang.org/rustup/dist"
	DefaultGoDownloadURL    = "https://go.dev/dl"
	DefaultAdoptiumURL      = "https://api.adoptium.net/v3/binary/version"
	DefaultDotnetInstallURL = "https://dot.net/v1"
	DefaultPnpmReleaseURL   = "https://github.com/pnpm/pnpm/releases/download"
	// DefaultNodeDistURL holds one folder per Node version (v22.1.0/) with the archives and SHASUMS256.txt.
	DefaultNodeDistURL = "https://nodejs.org/dist"

	// runnerReleasePath is where the GitHub Actions runner releases live under
	// the GitHub address.
	runnerReleasePath = "/actions/runner/releases/download"
	// enterpriseAPIPath is GitHub Enterprise Server's REST path under its host.
	enterpriseAPIPath = "/api/v3"

	// ActionsOIDCIssuer is the OIDC issuer of GitHub Actions certificates. It is
	// a fixed identity that signed releases carry, not a host crsp calls, so it
	// never follows CRSP_GITHUB_URL.
	ActionsOIDCIssuer = "https://token.actions.githubusercontent.com"

	// ReleaseSignerSANPrefix is the start of the certificate identity (a regular
	// expression) of the release workflow that signs crsp releases. Releases are
	// built on github.com, so it never follows CRSP_GITHUB_URL.
	ReleaseSignerSANPrefix = "^https://github\\.com/"
)

// Set is the external addresses of one process, read once at start.
type Set struct {
	// GitHubURL is GitHub's web address, no trailing slash.
	GitHubURL string
	// GitHubAPIURL is GitHub's REST address, no trailing slash.
	GitHubAPIURL string
	// RunnerDownloadURL is the base the Actions runner release assets are
	// fetched from: <base>/v<version>/actions-runner-<os>-<arch>-<version>.<ext>.
	RunnerDownloadURL string
	// VMImageMirror is the base of a mirror of the VM images, no trailing
	// slash; empty means the hosts of the catalogue. The mirror holds one
	// folder per distro (<base>/<distro>/) with the files of the upstream
	// directory: the checksum file and the image.
	VMImageMirror string
}

// Defaults are the addresses for github.com.
func Defaults() Set {
	a, _ := Load(func(string) string { return "" })
	return a
}

// Load reads and validates the address variables. Every problem is
// reported together and each message names its variable.
func Load(getenv func(string) string) (Set, error) {
	var errs []error
	read := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return ""
		}
		if err := ValidateBaseURL(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return ""
		}
		return v
	}
	web, api, dl := read(EnvGitHubURL), read(EnvGitHubAPIURL), read(EnvRunnerDownloadURL)
	mirror := read(EnvVMImageMirror)
	a := Set{GitHubURL: web, GitHubAPIURL: api, RunnerDownloadURL: dl, VMImageMirror: mirror}
	switch {
	case web == "":
		a.GitHubURL = DefaultGitHubURL
		if api == "" {
			a.GitHubAPIURL = DefaultGitHubAPIURL
		}
	case api == "" && web != DefaultGitHubURL:
		// A GitHub Enterprise Server host serves its API under /api/v3.
		a.GitHubAPIURL = web + enterpriseAPIPath
	case api == "":
		a.GitHubAPIURL = DefaultGitHubAPIURL
	}
	if dl == "" {
		a.RunnerDownloadURL = a.GitHubURL + runnerReleasePath
	}
	if len(errs) > 0 {
		return Set{}, errors.Join(errs...)
	}
	return a, nil
}

// VMImageURL is the address of a file of a VM image distro: upstream when no
// mirror is set, else <mirror>/<distro>/<last part of upstream>. An upstream
// that ends with a slash (a directory) gives the mirror's folder of the distro.
func (a Set) VMImageURL(distro, upstream string) string {
	if a.VMImageMirror == "" {
		return upstream
	}
	dir := a.VMImageMirror + "/" + url.PathEscape(distro) + "/"
	if strings.HasSuffix(upstream, "/") {
		return dir
	}
	return dir + upstream[strings.LastIndex(upstream, "/")+1:]
}

// VMImageMirrorFrom reads only CRSP_VM_IMAGE_MIRROR: the job process that
// downloads VM images needs no other address. Empty means no mirror.
func VMImageMirrorFrom(getenv func(string) string) (string, error) {
	v := strings.TrimSpace(getenv(EnvVMImageMirror))
	if v == "" {
		return DefaultVMImageMirror, nil
	}
	if err := ValidateBaseURL(v); err != nil {
		return "", fmt.Errorf("%s: %w", EnvVMImageMirror, err)
	}
	return v, nil
}

// ValidateBaseURL checks an address base: absolute, https (http only for
// 127.0.0.1 and localhost), a host, no query or fragment, no trailing slash.
func ValidateBaseURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return fmt.Errorf("%q is not an absolute address (https://host)", v)
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && local:
	default:
		return fmt.Errorf("%q must use https (http only for 127.0.0.1 or localhost)", v)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q must not hold credentials, a query or a fragment", v)
	}
	if strings.HasSuffix(v, "/") {
		return fmt.Errorf("%q must not end with a slash", v)
	}
	return nil
}

// web is the GitHub address; a zero Set means github.com.
func (a Set) web() string {
	if a.GitHubURL == "" {
		return DefaultGitHubURL
	}
	return a.GitHubURL
}

// RepoPage is the GitHub page of a repository or an account ("owner/repo" or "owner").
func (a Set) RepoPage(path string) string { return a.web() + "/" + path }

// JobPage is the GitHub page of one job of a workflow run.
func (a Set) JobPage(repository string, runID, jobID int64) string {
	return fmt.Sprintf("%s/%s/actions/runs/%d/job/%d", a.web(), repository, runID, jobID)
}

// InstallPage is GitHub's page that installs an App on more repositories.
func (a Set) InstallPage(slug string) string {
	return a.web() + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

// RunnerAsset is the download address of an Actions runner release asset.
func (a Set) RunnerAsset(version, osName, arch, ext string) string {
	return fmt.Sprintf("%s/v%s/actions-runner-%s-%s-%s.%s", a.RunnerDownloadURL, version, osName, arch, version, ext)
}

// RegistryAPIURL is the address of an upstream container registry named by its
// host (docker.io, ghcr.io, registry.example.com:5000): Docker Hub's API host
// for Docker Hub, the host itself over HTTPS for every other.
func RegistryAPIURL(upstream string) string {
	switch upstream {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return DockerHubRegistryURL
	}
	return "https://" + upstream
}
