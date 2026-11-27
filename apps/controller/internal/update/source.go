package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is the repository whose stable GitHub Releases this product updates from.
const Repo = "luis-mc/ci-runners-shared-pool"

// Release is one stable release and its assets (name to download URL).
type Release struct {
	Version string // without the leading v
	Tag     string
	URL     string // the release page
	Assets  map[string]string
}

// Source finds the newest stable release (operations R5.11).
type Source interface {
	Latest(ctx context.Context) (Release, error)
}

// ErrNoRelease says GitHub has no stable release yet.
var ErrNoRelease = errors.New("no stable release found")

// GitHubSource reads GitHub Releases. Base is the API root (default
// https://api.github.com); tests point it at a fake.
type GitHubSource struct {
	Base   string
	Repo   string
	Client *http.Client
	// Version, when set, looks up that release by its tag (a follower pins the
	// controller's version) instead of the latest one.
	Version string
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	HTMLURL    string    `json:"html_url"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (g GitHubSource) releasePath() string {
	if g.Version != "" {
		return "/releases/tags/v" + url.PathEscape(strings.TrimPrefix(g.Version, "v"))
	}
	return "/releases/latest"
}

// Latest asks for /releases/latest, which GitHub answers with the newest
// release that is neither a draft nor a pre-release. The edge pre-release is
// therefore never offered; a draft or pre-release in the answer is refused too.
func (g GitHubSource) Latest(ctx context.Context) (Release, error) {
	base := g.Base
	if base == "" {
		base = addresses.DefaultGitHubAPIURL
	}
	repo := g.Repo
	if repo == "" {
		repo = Repo
	}
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/repos/"+repo+g.releasePath(), nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "crsp-updater")
	resp, err := c.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %d", resp.StatusCode)
	}
	var r ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("GitHub sent an unreadable release: %w", err)
	}
	if r.Draft || r.Prerelease {
		return Release{}, ErrNoRelease
	}
	out := Release{Tag: r.TagName, Version: strings.TrimPrefix(r.TagName, "v"), URL: r.HTMLURL, Assets: map[string]string{}}
	for _, a := range r.Assets {
		out.Assets[a.Name] = a.URL
	}
	if _, ok := ParseVersion(out.Version); !ok {
		return Release{}, ErrNoRelease
	}
	return out, nil
}

// Version is a stable semantic version.
type Version struct{ Major, Minor, Patch int }

// ParseVersion reads "1.2.3" or "v1.2.3". A pre-release or build suffix, an
// edge build ("edge-abc1234") and "dev" are not stable versions.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, true
}

// Newer reports whether latest is a stable version above current. A current
// version that is not stable (a dev or edge build) is never offered an update
// by the automatic path, so a developer's build is not replaced.
func Newer(current, latest string) bool {
	c, ok1 := ParseVersion(current)
	l, ok2 := ParseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	switch {
	case l.Major != c.Major:
		return l.Major > c.Major
	case l.Minor != c.Minor:
		return l.Minor > c.Minor
	}
	return l.Patch > c.Patch
}

// VersionedName puts the release version into a plain asset name, before the
// extension: crsp-windows-amd64.msi and 0.1.15 give crsp-windows-amd64_0.1.15.msi
// (OPS40, DL-96). The version has no leading v.
func VersionedName(plain, version string) string {
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return plain
	}
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		if strings.HasSuffix(plain, ext) {
			return strings.TrimSuffix(plain, ext) + "_" + version + ext
		}
	}
	return plain
}

// PlainName is the reverse of VersionedName: crsp-windows-amd64_0.1.15.msi gives
// crsp-windows-amd64.msi. A name without a version part comes back unchanged.
func PlainName(name string) string {
	if p := plainFromRelease(name); p != "" {
		return p
	}
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		if base, ok := strings.CutSuffix(name, ext); ok {
			if i := strings.LastIndex(base, "_"); i > 0 {
				return base[:i] + ext
			}
			return name
		}
	}
	return name
}

// ReleaseName is the release asset name of the lifecycle standard: Crsp-<version>-<os>-<arch>.<ext>
// (os win, linux or macos; arch x64 or arm64), for example Crsp-0.1.22-win-x64.msi. It takes the
// plain name crsp-<goos>-<goarch>.<ext>; a name of another shape comes back as "".
func ReleaseName(plain, version string) string {
	version = strings.TrimPrefix(version, "v")
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		base, ok := strings.CutSuffix(plain, ext)
		if !ok {
			continue
		}
		parts := strings.Split(base, "-")
		if len(parts) != 3 || parts[0] != "crsp" || version == "" {
			return ""
		}
		osName := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[parts[1]]
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[parts[2]]
		if osName == "" || arch == "" {
			return ""
		}
		return "Crsp-" + version + "-" + osName + "-" + arch + ext
	}
	return ""
}

// ManifestName is the signed manifest of an asset: the asset's name plus .manifest.json.
func ManifestName(asset string) string { return asset + ".manifest.json" }

// assetCandidates lists the names a release may use for a plain name, newest scheme first: the
// lifecycle name (Crsp-<version>-<os>-<arch>.<ext>), the versioned name since OPS40, the plain
// name up to 0.1.14. The old names stay for one transition release.
func assetCandidates(plain, version string) []string {
	out := make([]string, 0, 3)
	if n := ReleaseName(plain, version); n != "" {
		out = append(out, n)
	}
	return append(out, VersionedName(plain, version), plain)
}

// PickAsset chooses the asset of a release for a plain name: the lifecycle name first, then the
// versioned name, then the plain name. When none is there it returns the first candidate, so the
// error names what is expected.
func PickAsset(rel Release, plain string) string {
	c := assetCandidates(plain, rel.Version)
	for _, n := range c {
		if _, ok := rel.Assets[n]; ok {
			return n
		}
	}
	return c[0]
}

// PickFromSums is PickAsset for a SHA256SUMS text.
func PickFromSums(sums []byte, plain, version string) string {
	c := assetCandidates(plain, version)
	for _, n := range c {
		if _, ok := SumFor(sums, n); ok {
			return n
		}
	}
	return c[0]
}

// AssetName is the plain release asset name this OS and architecture installs
// from (the name up to release 0.1.14): the MSI on Windows (task CRSP18),
// crsp-<os>-<arch>.tar.gz elsewhere. A release since OPS40 carries the version
// in the name; PickAsset finds either.
func AssetName(goos, goarch string) string {
	if goos == "windows" {
		return "crsp-windows-" + goarch + ".msi"
	}
	return "crsp-" + goos + "-" + goarch + ".tar.gz"
}

// allowedHost says whether a download URL may be fetched: HTTPS to GitHub's own
// hosts. Tests allow their local server through Options.AllowHost.
func allowedHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	switch u.Hostname() {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "api.github.com":
		return true
	}
	return false
}

// Install kinds: the marker file `install-kind` next to the installed binary
// says how this machine was installed (written by each installer). No file
// means a tarball install, except on Windows, where the MSI is the only one.
const (
	KindMSI     = "msi"
	KindPKG     = "pkg"
	KindDEB     = "deb"
	KindRPM     = "rpm"
	KindTarball = "tarball"
)

// InstallKindFor reads the marker next to exe; goos picks the default.
func InstallKindFor(exe, goos string) string {
	def := KindTarball
	if goos == "windows" {
		def = KindMSI
	}
	// Next to the running binary first, then the share folders the native installers may use (OPS1 documents the exact one).
	var b []byte
	var err error
	for _, dir := range []string{filepath.Dir(exe), "/usr/share/crsp", "/usr/local/share/crsp"} {
		if b, err = os.ReadFile(filepath.Join(dir, "install-kind")); err == nil { //nolint:gosec // fixed names
			break
		}
	}
	if err != nil {
		return def
	}
	switch k := strings.TrimSpace(string(b)); k {
	case KindMSI, KindPKG, KindDEB, KindRPM, KindTarball:
		return k
	}
	return def
}

// AssetFor is the plain release asset name that matches the install kind.
func AssetFor(kind, goos, goarch string) string {
	switch kind {
	case KindMSI:
		return "crsp-windows-" + goarch + ".msi"
	case KindPKG:
		return "crsp-darwin-" + goarch + ".pkg"
	case KindDEB:
		return "crsp-linux-" + goarch + ".deb"
	case KindRPM:
		return "crsp-linux-" + goarch + ".rpm"
	}
	return AssetName(goos, goarch)
}

// plainFromRelease reverses ReleaseName: Crsp-0.1.22-win-x64.msi gives crsp-windows-amd64.msi.
func plainFromRelease(name string) string {
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		base, ok := strings.CutSuffix(name, ext)
		if !ok {
			continue
		}
		parts := strings.Split(base, "-")
		if len(parts) < 4 || parts[0] != "Crsp" {
			return ""
		}
		goos := map[string]string{"win": "windows", "macos": "darwin", "linux": "linux"}[parts[len(parts)-2]]
		arch := map[string]string{"x64": "amd64", "arm64": "arm64"}[parts[len(parts)-1]]
		if goos == "" || arch == "" {
			return ""
		}
		return "crsp-" + goos + "-" + arch + ext
	}
	return ""
}
