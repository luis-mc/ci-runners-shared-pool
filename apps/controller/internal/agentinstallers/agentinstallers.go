// Package agentinstallers holds the agent installers of the release the
// controller was built from (operations R5.128 to R5.133, CRSP304). Only the
// Controller installer is user-facing; an agent installer is a release asset
// that the controller fetches, checks against SHA256SUMS, keeps in its data
// folder and hands to a machine: pushed over SSH without the owner seeing it,
// or by a command with a LAN link. Once cached, an installer is served with no
// network; a file can also be imported by hand (an offline controller).
package agentinstallers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// PathPrefix is where the controller serves the installers (same listener as the console).
const PathPrefix = "/agent-installers/"

// SumsName is the checksum file of a release.
const SumsName = "SHA256SUMS"

// ErrUnavailable says there is no release to fetch from and nothing cached.
var ErrUnavailable = errors.New("this controller has no agent installers to hand out")

// ErrNotListed says the name is not an agent installer of the release.
var ErrNotListed = errors.New("no such agent installer")

// ErrMismatch says a file does not have the SHA-256 SHA256SUMS lists.
var ErrMismatch = errors.New("the file does not match SHA256SUMS")

// ErrSumsKnown says the release's own SHA256SUMS is already known, so a carried-over copy is not taken.
var ErrSumsKnown = errors.New("this controller already has the checksums of its release")

// nameRe reads both name patterns of an agent installer (CRSP347, CRSP348):
// the archive (zip, tar.gz) and the native package (msi, pkg, deb).
var nameRe = regexp.MustCompile(`^CrspAgent-([0-9A-Za-z][0-9A-Za-z._+~-]*)-(win|linux|macos)-(x64|arm64)\.(zip|tar\.gz|msi|pkg|deb)$`)

// formats lists the file types an agent installer has on each system.
var formats = map[string][]string{
	"windows": {"zip", "msi"},
	"linux":   {"tar.gz", "deb"},
	"macos":   {"tar.gz", "pkg"},
}

// Info is what an agent installer name says.
type Info struct {
	Version, OS, Arch string // OS: windows, linux, macos. Arch: amd64, arm64.
	// Format is the file type: zip, tar.gz, msi, pkg or deb.
	Format string
}

// Name is CrspAgent-<version>-<os>-<arch>.<ext>: zip for Windows, tar.gz for the rest.
func Name(version, goos, goarch string) (string, error) {
	osName, ext := "", "tar.gz"
	switch goos {
	case "windows":
		osName, ext = "win", "zip"
	case "linux":
		osName = "linux"
	case "macos", "darwin":
		osName = "macos"
	default:
		return "", fmt.Errorf("agentinstallers: unknown os %q", goos)
	}
	arch := ""
	switch goarch {
	case "amd64", "x64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("agentinstallers: unknown arch %q", goarch)
	}
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return "", errors.New("agentinstallers: no version")
	}
	return "CrspAgent-" + version + "-" + osName + "-" + arch + "." + ext, nil
}

// ParseName reads an installer name; the second result is false for any other name.
func ParseName(name string) (Info, bool) {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return Info{}, false
	}
	in := Info{Version: m[1], OS: map[string]string{"win": "windows", "linux": "linux", "macos": "macos"}[m[2]], Arch: "amd64", Format: m[4]}
	if m[3] == "arm64" {
		in.Arch = "arm64"
	}
	for _, f := range formats[in.OS] {
		if f == in.Format {
			return in, true
		}
	}
	return Info{}, false
}

// Installer is one agent installer of the release.
type Installer struct {
	Name, SHA256, OS, Arch, Format string
	// Cached says the file is in the data folder and has the listed SHA-256.
	Cached bool
}

// Store serves the agent installers of one release.
type Store struct {
	// Version is the release to serve (the controller's own).
	Version string
	// Source finds the release; nil means offline (cache and imports only).
	Source update.Source
	// Dir is the cache folder (the data folder's agent-installers/).
	Dir string
	// Fetch and AllowURL are seams for tests; the defaults are the updater's.
	Fetch    func(ctx context.Context, url, dst string) error
	AllowURL func(string) bool

	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	release  update.Release
	haveRel  bool
	verified map[string]bool
	// sumsMem is the SHA256SUMS read from the release, kept for the life of the
	// process; netFailed is when the network last failed, so an offline
	// controller does not wait on it for every call (RetryAfter).
	sumsMem   []byte
	netFailed time.Time
}

// RetryAfter is how long the network is left alone after a failure.
const RetryAfter = time.Minute

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// offline says the network failed a moment ago.
func (s *Store) offline() bool {
	return !s.netFailed.IsZero() && s.now().Sub(s.netFailed) < RetryAfter
}

func (s *Store) fetch() func(context.Context, string, string) error {
	if s.Fetch != nil {
		return s.Fetch
	}
	return update.Fetch
}

func (s *Store) allow(u string) bool {
	if s.AllowURL != nil {
		return s.AllowURL(u)
	}
	return update.AllowedURL(u)
}

func (s *Store) dir() string { return filepath.Join(s.Dir, strings.TrimPrefix(s.Version, "v")) }

// findRelease finds the release once; a failure is not kept, so the next call tries again.
func (s *Store) findRelease(ctx context.Context) (update.Release, bool) {
	if s.haveRel {
		return s.release, true
	}
	if s.Source == nil || s.offline() {
		return update.Release{}, false
	}
	rel, err := s.Source.Latest(ctx)
	if err != nil {
		s.netFailed = s.now()
		return update.Release{}, false
	}
	s.netFailed = time.Time{}
	s.release, s.haveRel = rel, true
	return rel, true
}

// sums returns the checksum text: fetched and cached when the release is
// reachable, else the cached copy. Callers hold mu.
func (s *Store) sums(ctx context.Context) ([]byte, error) {
	if s.Version == "" || s.Dir == "" {
		return nil, ErrUnavailable
	}
	if s.sumsMem != nil {
		return s.sumsMem, nil
	}
	path := filepath.Join(s.dir(), SumsName)
	if rel, ok := s.findRelease(ctx); ok {
		if u, ok := rel.Assets[SumsName]; ok && s.allow(u) {
			if err := os.MkdirAll(s.dir(), 0o750); err != nil {
				return nil, err
			}
			part := path + ".part"
			if err := s.fetch()(ctx, u, part); err == nil {
				if b, err := os.ReadFile(part); err == nil && len(listed(b)) > 0 { //nolint:gosec // our cache folder
					if err := os.Rename(part, path); err == nil {
						s.sumsMem = b
						return b, nil
					}
				}
			}
			_ = os.Remove(part)
		}
	}
	b, err := os.ReadFile(path) //nolint:gosec // our cache folder
	if err != nil {
		return nil, fmt.Errorf("%w: no SHA256SUMS for %s, here or on the release", ErrUnavailable, s.Version)
	}
	if len(listed(b)) == 0 {
		return nil, fmt.Errorf("%w: the SHA256SUMS of %s lists no agent installer", ErrUnavailable, s.Version)
	}
	return b, nil
}

// sumLine is one line of SHA256SUMS.
type sumLine struct{ Name, SHA256 string }

var sumRe = regexp.MustCompile(`^([0-9a-f]{64})[ ]+\*?(\S+)$`)

// listed keeps the lines of SHA256SUMS that name an agent installer.
func listed(sums []byte) []sumLine {
	var out []sumLine
	for _, line := range strings.Split(string(sums), "\n") {
		m := sumRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if _, ok := ParseName(m[2]); ok {
			out = append(out, sumLine{Name: m[2], SHA256: m[1]})
		}
	}
	return out
}

// NativeFormat is the native package format of an OS: msi, pkg or deb.
var NativeFormat = map[string]string{"win": "msi", "macos": "pkg", "linux": "deb"}

// Coverage says which of the six targets (win, linux, macos by x64, arm64) a SHA256SUMS lists an agent archive and
// a native package for. A target is "<os>-<arch>" in the release names, e.g. "win-x64".
type Coverage struct {
	MissingArchives, MissingNative []string
}

// CoverageOf reads the SHA256SUMS text of a release.
func CoverageOf(sums []byte) Coverage {
	haveArchive, haveNative := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(string(sums), "\n") {
		m := sumRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if in, ok := ParseName(m[2]); ok {
			os := map[string]string{"windows": "win", "linux": "linux", "macos": "macos"}[in.OS]
			arch := "x64"
			if in.Arch == "arm64" {
				arch = "arm64"
			}
			if in.Format == NativeFormat[os] {
				haveNative[os+"-"+arch+"."+in.Format] = true
			} else {
				haveArchive[os+"-"+arch] = true
			}
		}
	}
	var c Coverage
	for _, os := range []string{"win", "linux", "macos"} {
		for _, arch := range []string{"x64", "arm64"} {
			t := os + "-" + arch
			if !haveArchive[t] {
				c.MissingArchives = append(c.MissingArchives, t)
			}
			if !haveNative[t+"."+NativeFormat[os]] {
				c.MissingNative = append(c.MissingNative, t+"."+NativeFormat[os])
			}
		}
	}
	return c
}

// Sums returns the SHA256SUMS text and the version.
func (s *Store) Sums(ctx context.Context) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return nil, "", err
	}
	return b, strings.TrimPrefix(s.Version, "v"), nil
}

// List names every agent installer of the release and says which are cached.
func (s *Store) List(ctx context.Context) ([]Installer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return nil, err
	}
	var out []Installer
	for _, p := range listed(b) {
		in, _ := ParseName(p.Name)
		got, err := update.FileSHA256(filepath.Join(s.dir(), p.Name))
		out = append(out, Installer{Name: p.Name, SHA256: p.SHA256, OS: in.OS, Arch: in.Arch, Format: in.Format, Cached: err == nil && got == p.SHA256})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) want(sums []byte, name string) (string, bool) {
	for _, p := range listed(sums) {
		if p.Name == name {
			return p.SHA256, true
		}
	}
	return "", false
}

// Open returns an installer, downloaded and checked against SHA256SUMS on
// first use. A cached file is served without network. A name that SHA256SUMS
// does not list as an agent installer is refused.
func (s *Store) Open(ctx context.Context, name string) (*os.File, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return nil, 0, err
	}
	want, ok := s.want(b, name)
	if !ok {
		return nil, 0, ErrNotListed
	}
	if err := s.ensure(ctx, name, want); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(filepath.Join(s.dir(), name)) //nolint:gosec // our cache folder, name checked against SHA256SUMS
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// ensure makes the file present with the listed SHA-256. Callers hold mu.
func (s *Store) ensure(ctx context.Context, name, want string) error {
	path := filepath.Join(s.dir(), name)
	if s.verified == nil {
		s.verified = map[string]bool{}
	}
	if s.verified[name] {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		s.verified[name] = false
	}
	if got, err := update.FileSHA256(path); err == nil && got == want {
		s.verified[name] = true
		return nil
	}
	rel, ok := s.findRelease(ctx)
	if !ok {
		return fmt.Errorf("%w: %s is not cached and the release cannot be reached; import it by hand", ErrUnavailable, name)
	}
	u, ok := rel.Assets[name]
	if !ok || !s.allow(u) {
		return fmt.Errorf("%w: the release has no %s", ErrNotListed, name)
	}
	if err := os.MkdirAll(s.dir(), 0o750); err != nil {
		return err
	}
	part := path + ".part"
	if err := s.fetch()(ctx, u, part); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	got, err := update.FileSHA256(part)
	if err != nil || got != want {
		_ = os.Remove(part)
		return fmt.Errorf("%w: %s", ErrMismatch, name)
	}
	if err := os.Rename(part, path); err != nil {
		return err
	}
	s.verified[name] = true
	return nil
}

// Prefetch downloads every listed installer once, so the controller can hand
// them out with no network later. It returns the names now cached and the
// first error.
func (s *Store) Prefetch(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return nil, err
	}
	var done []string
	var first error
	for _, p := range listed(b) {
		if err := s.ensure(ctx, p.Name, p.SHA256); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		done = append(done, p.Name)
	}
	return done, first
}

// ImportSums stores a SHA256SUMS file the owner carried over (an offline
// controller): it must list at least one agent installer of this version.
func (s *Store) ImportSums(r io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return err
	}
	if len(b) == 1<<20 {
		return fmt.Errorf("%w: SHA256SUMS is too large", ErrNotListed)
	}
	ok := false
	for _, p := range listed(b) {
		if in, _ := ParseName(p.Name); strings.TrimPrefix(s.Version, "v") == in.Version {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("%w: SHA256SUMS lists no agent installer of %s", ErrNotListed, s.Version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sumsMem != nil {
		return ErrSumsKnown
	}
	if err := os.MkdirAll(s.dir(), 0o750); err != nil {
		return err
	}
	s.sumsMem = nil
	return os.WriteFile(filepath.Join(s.dir(), SumsName), b, 0o640) //nolint:gosec // our cache folder
}

// Import stores an installer the owner carried over. The name must be listed
// in SHA256SUMS (fetched or imported first) and the bytes must match its
// SHA-256; nothing else is ever kept.
func (s *Store) Import(ctx context.Context, name string, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return err
	}
	want, ok := s.want(b, name)
	if !ok {
		return ErrNotListed
	}
	if err := os.MkdirAll(s.dir(), 0o750); err != nil {
		return err
	}
	part := filepath.Join(s.dir(), name+".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640) //nolint:gosec // our cache folder
	if err != nil {
		return err
	}
	_, cerr := io.Copy(f, r)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(part)
		return cerr
	}
	if got, err := update.FileSHA256(part); err != nil || got != want {
		_ = os.Remove(part)
		return fmt.Errorf("%w: %s", ErrMismatch, name)
	}
	if err := os.Rename(part, filepath.Join(s.dir(), name)); err != nil {
		return err
	}
	if s.verified == nil {
		s.verified = map[string]bool{}
	}
	s.verified[name] = true
	return nil
}

// State is what the console and the CLI show before an import.
type State struct {
	Version string
	// HaveSums says SHA256SUMS of the release is here (fetched or imported).
	HaveSums bool
	// FromRelease says SHA256SUMS was read from the release itself, so an imported copy is not needed.
	FromRelease bool
	Installers  []Installer
}

// State reports the version, whether SHA256SUMS is here, and every listed installer.
// A controller without SHA256SUMS is not an error: it is the first step of an import.
func (s *Store) State(ctx context.Context) (State, error) {
	if s.Version == "" || s.Dir == "" {
		return State{}, ErrUnavailable
	}
	st := State{Version: strings.TrimPrefix(s.Version, "v")}
	list, err := s.List(ctx)
	switch {
	case err == nil:
		st.HaveSums, st.Installers = true, list
	case errors.Is(err, ErrUnavailable):
	default:
		return State{}, err
	}
	s.mu.Lock()
	st.FromRelease = s.sumsMem != nil
	s.mu.Unlock()
	return st, nil
}

// Ref says where one installer is and the SHA-256 it must have.
type Ref struct {
	Name, URL, SHA256 string
	// SelfSigned says the address is the controller's own; the SHA-256, taken
	// in the owner's session, decides what is installed.
	SelfSigned bool
}

// For finds the installer of an OS ("windows", "linux", "macos") and CPU
// ("amd64", "arm64"). console is the controller's console address; with one,
// the ref points at the LAN link, without one at the release asset.
func (s *Store) For(ctx context.Context, goos, goarch, console string) (Ref, error) {
	name, err := Name(s.Version, goos, goarch)
	if err != nil {
		return Ref{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.sums(ctx)
	if err != nil {
		return Ref{}, err
	}
	want, ok := s.want(b, name)
	if !ok {
		return Ref{}, fmt.Errorf("%w: %s", ErrNotListed, name)
	}
	if console != "" {
		return Ref{Name: name, URL: LANLink(console, name), SHA256: want, SelfSigned: true}, nil
	}
	rel, ok := s.findRelease(ctx)
	u := ""
	if ok {
		u = rel.Assets[name]
	}
	if u == "" || !s.allow(u) {
		return Ref{}, fmt.Errorf("%w: no address for %s", ErrUnavailable, name)
	}
	return Ref{Name: name, URL: u, SHA256: want}, nil
}

// LANLink is the address of an installer on the controller's console.
func LANLink(console, name string) string {
	return strings.TrimRight(console, "/") + PathPrefix + name
}

// ManualCommand is the command a person pastes on a machine that cannot be
// pushed to: it downloads the installer from the LAN link, checks its SHA-256
// and unpacks it. The join command (machines R5.3) follows it. Windows gets
// PowerShell, Linux and macOS a shell line.
func ManualCommand(r Ref, goos string) string {
	if goos == "windows" {
		skip := ""
		if r.SelfSigned {
			skip = "[Net.ServicePointManager]::ServerCertificateValidationCallback={$true}; "
		}
		return fmt.Sprintf("%s$f=Join-Path $env:TEMP '%s'; Invoke-WebRequest -UseBasicParsing -Uri '%s' -OutFile $f; "+
			"if ((Get-FileHash -Algorithm SHA256 $f).Hash.ToLower() -ne '%s') { throw 'checksum differs' }; "+
			"Expand-Archive -Force $f \"$env:TEMP\\crsp-agent-setup\"; & \"$env:TEMP\\crsp-agent-setup\\crsp-agent\\install.ps1\"",
			skip, r.Name, r.URL, r.SHA256)
	}
	k := ""
	if r.SelfSigned {
		k = "k"
	}
	return fmt.Sprintf("curl -fsSL%s -o %s '%s' && echo '%s  %s' | shasum -a 256 -c - && tar xzf %s && sudo sh crsp-agent/install.sh",
		k, r.Name, r.URL, r.SHA256, r.Name, r.Name)
}
