package toolstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/registry"
)

// Tool is one tool and version of a plan, the shape Linux profiles use.
type Tool = registry.Toolchain

// DefaultUnusedDays is how long a version can stay unused before housekeeping removes it (R5.118). It is a
// setting; this is its default.
const DefaultUnusedDays = 30

const (
	markerDir  = ".installed"
	usageDir   = ".usage"
	stagingDir = ".staging"
)

// Marker is the file written, after the version folder is sealed and in place, to <root>/.installed/. It is
// outside the version folder so that sealing never has to be undone to write or remove it (a sealed folder
// cannot even be renamed on macOS). A folder without a marker is a failed install and is never used.
type Marker struct {
	Manager     string    `json:"manager"`
	InstalledAt time.Time `json:"installed_at"`
	// Unchecked lists the downloads whose project publishes no SHA-256 (R5.113).
	Unchecked []string `json:"unchecked,omitempty"`
}

// Result is what an Installer reports.
type Result struct {
	Manager   string
	Unchecked []string
}

// Installer runs a recipe into the staging folder dest. The real one is ExecInstaller; tests use FakeInstaller.
type Installer interface {
	Install(ctx context.Context, r Recipe, dest string) (Result, error)
}

// Store is the read-only tool store of one machine: Root/<tool>/<version>/, versions side by side.
type Store struct {
	Root string
	GOOS string
	Arch string
	// Sources are the download addresses; the zero value means DefaultSources.
	Sources Sources
	// Seal makes a folder read-only for every job identity; Unseal undoes it for housekeeping. Nil uses the
	// OS default (seal_*.go).
	Seal, Unseal func(dir string) error
	Now          func() time.Time
	// Xcodes finds the Xcodes the owner installed; the zero value looks in /Applications. Xcode is never in
	// Root: the store neither downloads nor removes it.
	Xcodes XcodeFinder

	mu sync.Mutex
}

// NewStore returns a store at root for goos and arch with the OS default seal.
func NewStore(root, goos, arch string) *Store {
	return &Store{Root: root, GOOS: goos, Arch: arch, Seal: sealDir, Unseal: unsealDir, Now: time.Now}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Dir is the folder of one version.
func (s *Store) Dir(t Tool) string { return filepath.Join(s.Root, t.Tool, t.Version) }

// Has says whether the version is installed and complete.
func (s *Store) Has(t Tool) bool {
	if !ValidVersion(t.Version) || CanonTool(t.Tool) != t.Tool {
		return false
	}
	if t.Tool == ToolXcode {
		_, err := s.Xcodes.Find(context.Background(), t.Version)
		return err == nil
	}
	_, err := os.Stat(s.markerPath(t))
	return err == nil
}

func (s *Store) markerPath(t Tool) string {
	return filepath.Join(s.Root, markerDir, t.Tool+"@"+t.Version)
}

func (s *Store) sources() Sources {
	if s.Sources == (Sources{}) {
		return DefaultSources()
	}
	return s.Sources
}

// Missing returns the tools of the plan that are not in the store (step 2 of the job flow).
func (s *Store) Missing(p Plan) Plan {
	var out Plan
	for _, t := range p {
		if !s.Has(t) {
			out = append(out, t)
		}
	}
	return out
}

// ErrNeedsInstall is returned by Resolve when the plan is not fully in the store.
var ErrNeedsInstall = errors.New("toolstore: the store lacks a tool of the plan")

// EnsureInstalled installs what the plan lacks, once (steps 2 and 3). A version that is already there is
// skipped; two callers asking for the same version install it once because the lock is held across the check
// and the install. Each version is installed into a staging folder, marked, sealed and moved into place, so
// a failed install leaves nothing a job could use. The error names every tool that failed; the others are
// still installed. Xcode is never installed: a plan that names one the Mac lacks fails with the words for
// the owner (ErrXcodeMissing).
func (s *Store) EnsureInstalled(ctx context.Context, in Installer, p Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var failed []string
	for _, t := range p {
		if s.Has(t) {
			continue
		}
		if t.Tool == ToolXcode {
			// The owner installs Xcode; the job fails with the reason, and the doctor says which one.
			_, err := s.Xcodes.Find(ctx, t.Version)
			failed = append(failed, fmt.Sprintf("%s %s: %v", t.Tool, t.Version, err))
			continue
		}
		if err := s.install(ctx, in, t); err != nil {
			failed = append(failed, fmt.Sprintf("%s %s: %v", t.Tool, t.Version, err))
		}
	}
	if len(failed) > 0 {
		return errors.New("toolstore: install failed: " + strings.Join(failed, "; "))
	}
	return nil
}

func (s *Store) install(ctx context.Context, in Installer, t Tool) error {
	if CanonTool(t.Tool) != t.Tool || !ValidVersion(t.Version) {
		return fmt.Errorf("not a valid tool and version")
	}
	stage := filepath.Join(s.Root, stagingDir, t.Tool+"-"+t.Version)
	_ = s.unsealAll(stage)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	rec, err := RecipeFor(s.GOOS, s.Arch, s.sources(), t, stage)
	if err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	res, err := in.Install(ctx, rec, stage)
	if err != nil {
		_ = s.unsealAll(stage)
		_ = os.RemoveAll(stage)
		return err
	}
	if s.Seal != nil {
		if err := s.Seal(stage); err != nil {
			return fmt.Errorf("sealing: %w", err)
		}
	}
	dir := s.Dir(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// Renaming a directory needs write access to the directory itself on macOS: open the top folder for the
	// move and close it again. Everything below it stays sealed.
	setDirMode(stage, 0o755)
	if err := os.Rename(stage, dir); err != nil {
		return err
	}
	setDirMode(dir, 0o555)
	// The marker comes last: only now does Has say yes.
	if err := os.MkdirAll(filepath.Dir(s.markerPath(t)), 0o755); err != nil {
		return err
	}
	mk, _ := json.Marshal(Marker{Manager: res.Manager, InstalledAt: s.now().UTC(), Unchecked: res.Unchecked})
	if err := os.WriteFile(s.markerPath(t), mk, 0o644); err != nil {
		return err
	}
	return s.Touch(t)
}

func (s *Store) unsealAll(dir string) error {
	if s.Unseal == nil {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	return s.Unseal(dir)
}

// Touch records that a job used the version now. The usage file is outside the sealed folders, so a sealed
// store never needs a write.
func (s *Store) Touch(t Tool) error {
	if err := os.MkdirAll(filepath.Join(s.Root, usageDir), 0o755); err != nil {
		return err
	}
	b, _ := s.now().UTC().MarshalText()
	return os.WriteFile(filepath.Join(s.Root, usageDir, t.Tool+"@"+t.Version), b, 0o644)
}

// Installed is one complete version of the store and when it was last used.
type Installed struct {
	Tool     Tool
	LastUsed time.Time
	Marker   Marker
}

// List returns the complete versions, sorted by tool then version.
func (s *Store) List() ([]Installed, error) {
	tools, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, td := range tools {
		if !td.IsDir() || strings.HasPrefix(td.Name(), ".") {
			continue
		}
		vs, err := os.ReadDir(filepath.Join(s.Root, td.Name()))
		if err != nil {
			return nil, err
		}
		for _, vd := range vs {
			t := Tool{Tool: td.Name(), Version: vd.Name()}
			if !s.Has(t) {
				continue
			}
			it := Installed{Tool: t}
			if b, err := os.ReadFile(s.markerPath(t)); err == nil {
				_ = json.Unmarshal(b, &it.Marker)
			}
			it.LastUsed = it.Marker.InstalledAt
			if b, err := os.ReadFile(filepath.Join(s.Root, usageDir, t.Tool+"@"+t.Version)); err == nil {
				var at time.Time
				if at.UnmarshalText(b) == nil && at.After(it.LastUsed) {
					it.LastUsed = at
				}
			}
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tool.Tool != out[j].Tool.Tool {
			return out[i].Tool.Tool < out[j].Tool.Tool
		}
		return out[i].Tool.Version < out[j].Tool.Version
	})
	return out, nil
}

// PruneOptions are the housekeeping settings (R5.118).
type PruneOptions struct {
	// MaxUnused is how long a version can stay unused; 0 means DefaultUnusedDays days.
	MaxUnused time.Duration
	// KeepAtLeast is the number of newest-used versions of a tool that stay even when unused.
	KeepAtLeast map[string]int
	// InUse are versions a running job or a planned job needs; they are never removed.
	InUse Plan
}

// Prune removes the versions unused for longer than the setting and returns them. A version in InUse stays.
func (s *Store) Prune(opt PruneOptions) ([]Tool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := opt.MaxUnused
	if max <= 0 {
		max = DefaultUnusedDays * 24 * time.Hour
	}
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	busy := map[Tool]bool{}
	for _, t := range opt.InUse {
		busy[t] = true
	}
	byTool := map[string][]Installed{}
	for _, it := range all {
		byTool[it.Tool.Tool] = append(byTool[it.Tool.Tool], it)
	}
	var removed []Tool
	for tool, vs := range byTool {
		sort.Slice(vs, func(i, j int) bool {
			if !vs[i].LastUsed.Equal(vs[j].LastUsed) {
				return vs[i].LastUsed.After(vs[j].LastUsed)
			}
			return vs[i].Tool.Version > vs[j].Tool.Version
		})
		keep := opt.KeepAtLeast[tool]
		for i, it := range vs {
			stale := s.now().Sub(it.LastUsed) > max
			if !stale || busy[it.Tool] || i < keep {
				continue
			}
			if err := s.remove(it.Tool); err != nil {
				return removed, err
			}
			removed = append(removed, it.Tool)
		}
	}
	sort.Slice(removed, func(i, j int) bool {
		if removed[i].Tool != removed[j].Tool {
			return removed[i].Tool < removed[j].Tool
		}
		return removed[i].Version < removed[j].Version
	})
	return removed, nil
}

func (s *Store) remove(t Tool) error {
	dir := s.Dir(t)
	// Take the marker away first, so a crash halfway leaves a folder no job treats as installed.
	if err := os.Remove(s.markerPath(t)); err != nil {
		return err
	}
	if s.Unseal != nil {
		if err := s.Unseal(dir); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(s.Root, usageDir, t.Tool+"@"+t.Version))
	return os.RemoveAll(dir)
}

func sortStrings(s []string) { sort.Strings(s) }

// setDirMode sets the mode bits of one folder on Unix; on Windows the ACL seal decides, and the read-only
// attribute of a folder means nothing there.
func setDirMode(dir string, m os.FileMode) {
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, m)
	}
}
