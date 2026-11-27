package runners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Extra packages on a remote machine (runners R5.86). The agent installs the
// named OS packages into the runner environment (the container image layer,
// the VM base disk or the persistent runner prefix) and never on the host OS.
// The environment is reached only through PackageExec, a hook the backend
// supplies, so this file touches no backend flag, mount or disk manifest.

// MaxPackageName is the longest package name the agent accepts.
const MaxPackageName = 128

// PackageManager is the package tool of the runner environment.
type PackageManager string

// Package managers.
const (
	PkgApt    PackageManager = "apt"
	PkgDnf    PackageManager = "dnf"
	PkgBrew   PackageManager = "brew"
	PkgWinget PackageManager = "winget"
)

// ErrPackageName is returned for a name the strict pattern refuses.
var ErrPackageName = errors.New("not a package name")

// ValidatePackageNames checks each name against the strict pattern of R5.51
// (letters, digits and + - . only, not starting with -) and a length limit.
// The first bad name refuses the whole list.
func ValidatePackageNames(names []string) error {
	for _, n := range names {
		if len(n) > MaxPackageName || !packageName.MatchString(n) {
			return fmt.Errorf("%w: %q", ErrPackageName, n)
		}
	}
	return nil
}

// PackageSetHash is the cache key of a package set: the manager, the target
// environment and the sorted names, in any order and without repeats.
func PackageSetHash(m PackageManager, env string, names []string) string {
	h := sha256.New()
	for _, part := range append([]string{string(m), env}, NormalizeLayers(names)...) {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// InstallArgv is the command that installs names with m inside the runner
// environment. Names are passed after "--" where the tool accepts it; winget
// has no list form, so it gets one command per name (see InstallCommands).
func InstallArgv(m PackageManager, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, errors.New("no packages")
	}
	if err := ValidatePackageNames(names); err != nil {
		return nil, err
	}
	names = NormalizeLayers(names)
	switch m {
	case PkgApt:
		return append([]string{"apt-get", "install", "-y", "--no-install-recommends", "--"}, names...), nil
	case PkgDnf:
		return append([]string{"dnf", "install", "-y", "--"}, names...), nil
	case PkgBrew:
		return append([]string{"brew", "install"}, names...), nil
	case PkgWinget:
		if len(names) != 1 {
			return nil, errors.New("winget installs one id per command")
		}
		return []string{"winget", "install", "--id", names[0], "-e", "--silent", "--accept-package-agreements", "--accept-source-agreements"}, nil
	}
	return nil, fmt.Errorf("unknown package manager %q", m)
}

// InstallCommands is the list of commands for a set: one for apt, dnf and
// brew, one per name for winget.
func InstallCommands(m PackageManager, names []string) ([][]string, error) {
	if m != PkgWinget {
		a, err := InstallArgv(m, names)
		if err != nil {
			return nil, err
		}
		return [][]string{a}, nil
	}
	var out [][]string
	for _, n := range NormalizeLayers(names) {
		a, err := InstallArgv(m, []string{n})
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// PackageExec runs one install command inside the runner environment (never
// on the host OS) and returns its output.
type PackageExec func(ctx context.Context, argv []string) (string, error)

// PackageFailure is one package set that did not install; the machine report
// carries it to the doctor.
type PackageFailure struct {
	SetHash string
	Names   []string
	Error   string
	At      time.Time
}

// PackageInstaller installs a package set once per set hash. A done set
// leaves a marker file in Dir; the next call with the same set is a cache hit
// and runs nothing. A failure is kept for the doctor until the set installs.
type PackageInstaller struct {
	// Dir holds the markers. It must be set.
	Dir string
	// Exec reaches the runner environment. It must be set.
	Exec PackageExec
	// Redact cleans command output before it is kept; nil keeps it as is.
	Redact func(string) string
	// Now stamps failures; nil is time.Now.
	Now func() time.Time

	mu       sync.Mutex
	failures map[string]PackageFailure
}

// Ensure installs names with m into the environment env unless the same set
// is already done. hit is true for a cache hit.
func (p *PackageInstaller) Ensure(ctx context.Context, m PackageManager, env string, names []string) (hit bool, err error) {
	names = NormalizeLayers(names)
	if len(names) == 0 {
		return true, nil
	}
	cmds, err := InstallCommands(m, names)
	if err != nil {
		return false, err
	}
	if p.Dir == "" || p.Exec == nil {
		return false, errors.New("package installer is not set up")
	}
	key := PackageSetHash(m, env, names)
	marker := filepath.Join(p.Dir, key+".done")
	if _, serr := os.Stat(marker); serr == nil {
		p.clear(key)
		return true, nil
	} else if !errors.Is(serr, os.ErrNotExist) {
		return false, fmt.Errorf("package cache: %w", serr)
	}
	for _, argv := range cmds {
		out, rerr := p.Exec(ctx, argv)
		if rerr != nil {
			return false, p.fail(key, names, argv, out, rerr)
		}
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return false, fmt.Errorf("package cache: %w", err)
	}
	if err := os.WriteFile(marker, []byte(strings.Join(names, "\n")+"\n"), 0o644); err != nil {
		return false, fmt.Errorf("package cache: %w", err)
	}
	p.clear(key)
	return false, nil
}

func (p *PackageInstaller) fail(key string, names, argv []string, out string, cause error) error {
	msg := strings.TrimSpace(out)
	if len(msg) > 400 {
		msg = msg[len(msg)-400:]
	}
	if p.Redact != nil {
		msg = p.Redact(msg)
	}
	text := fmt.Sprintf("%s failed: %v", argv[0], cause)
	if msg != "" {
		text += ": " + msg
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	p.mu.Lock()
	if p.failures == nil {
		p.failures = map[string]PackageFailure{}
	}
	p.failures[key] = PackageFailure{SetHash: key, Names: names, Error: text, At: now()}
	p.mu.Unlock()
	return errors.New(text)
}

func (p *PackageInstaller) clear(key string) {
	p.mu.Lock()
	delete(p.failures, key)
	p.mu.Unlock()
}

// Failures lists the sets that failed and have not installed since, oldest
// hash first.
func (p *PackageInstaller) Failures() []PackageFailure {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PackageFailure, 0, len(p.failures))
	for _, f := range p.failures {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SetHash < out[j].SetHash })
	return out
}
