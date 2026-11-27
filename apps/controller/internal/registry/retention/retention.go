// Package retention is REG6: budgets, settings, recommendations, safe delete and
// the weekly cleanup of the pool registry (docs/features/registry R5.41 to R5.52).
//
// It reaches data through Repo, the registry through Registry and the disk
// through registry.DiskInfo, so every rule is tested against fakes.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Errors of this package, beside the ones of package registry.
var (
	// ErrNeedsConfirmation answers a delete without confirm: the caller shows the
	// InUse that came with it and asks again with confirm (R5.46, UX-62).
	ErrNeedsConfirmation = errors.New("retention: the delete needs a yes")
	// ErrCleanupRunning answers a cleanup that starts while one runs.
	ErrCleanupRunning = errors.New("retention: a cleanup is running")
)

// Why a recommendation was closed.
const (
	WhyActedOn = "acted"
	WhyGone    = "gone"
)

// Audit actions (R5.51).
const (
	ActionDelete       = "registry.image.delete"
	ActionReadOnly     = "registry.read_only"
	ActionCleanup      = "registry.cleanup"
	ActionSettings     = "registry.settings.update"
	ActionBudgets      = "registry.budgets.update"
	defaultDiskFloor   = 5_000_000_000
	stagedKeepDuration = 24 * time.Hour
)

// Actor says who made a change; the audit adapter maps it to the audit log.
type Actor struct {
	Login  string
	Source string // web, system
}

// Sources of an Actor.
const (
	SourceWeb    = "web"
	SourceSystem = "system"
)

// CrspActor is the actor of what crsp does on its own.
var CrspActor = Actor{Login: "crsp", Source: SourceSystem}

// Auditor writes the audit entries of R5.51. A failed write fails the change.
type Auditor interface {
	Record(ctx context.Context, actor Actor, action, targetType, targetID string, details map[string]any) error
}

// StagedImage is a staged or refused image with the name it sits under in the
// incoming area, where its manifest is.
type StagedImage struct {
	registry.Image
	StagedName string
}

// Cleanup is one row of registry_cleanups.
type Cleanup struct {
	ID         int64
	Trigger    string // weekly or now
	State      string // running, done or failed
	FreedBytes int64
	Error      string
	StartedBy  string
	StartedAt  time.Time
	FinishedAt time.Time
}

// KindUse is what the layers charged to a kind take.
type KindUse struct {
	UsedBytes   int64
	SharedBytes int64
}

// Repo is the data the retention rules need. internal/store implements it
// (RegistryRetention in registry_retention.go).
type Repo interface {
	Settings(ctx context.Context) (registry.Settings, error)
	PutSettings(ctx context.Context, s registry.Settings, by string) error
	// Images lists every image whose state is not deleted.
	Images(ctx context.Context) ([]registry.Image, error)
	Image(ctx context.Context, id string) (registry.Image, error)
	PointersTo(ctx context.Context, imageID string) ([]registry.Pointer, error)
	// RunningJobs names the running jobs and pulls in progress that use the image.
	RunningJobs(ctx context.Context, imageID string) ([]string, error)
	// LiveLeases counts tokens that have not ended and name the image.
	LiveLeases(ctx context.Context, now time.Time, name string) (int, error)
	MarkDeleted(ctx context.Context, id string, at time.Time, by string) error
	OldStaged(ctx context.Context, before time.Time) ([]StagedImage, error)

	Recommendations(ctx context.Context) ([]registry.Recommendation, error)
	Upsert(ctx context.Context, r registry.Recommendation) error
	Close(ctx context.Context, id, why string) error

	DanglingLayers(ctx context.Context) ([]registry.Layer, error)
	DeleteLayer(ctx context.Context, digest string) error
	// Use gives the bytes charged to each kind and the part shared with the other.
	Use(ctx context.Context) (map[registry.Kind]KindUse, error)

	SetReadOnly(ctx context.Context, on bool) error
	StartCleanup(ctx context.Context, trigger, by string) (int64, error)
	FinishCleanup(ctx context.Context, id int64, state string, freed int64, errText string) error
	Cleanup(ctx context.Context, id int64) (Cleanup, error)
	LastCleanup(ctx context.Context) (Cleanup, error) // registry.ErrNotFound when none
}

// Registry is the part of registry.Registry retention uses.
type Registry interface {
	SetReadOnly(on bool)
	DeleteManifest(ctx context.Context, name, digest string) error
	GC(ctx context.Context, dryRun bool) (freedBytes int64, err error)
}

// Profiles says whether a profile still exists (IMG3); optional.
type Profiles interface {
	Exists(ctx context.Context, profile string) (bool, error)
}

// Options builds a Service.
type Options struct {
	Repo     Repo
	Registry Registry
	Disk     registry.DiskInfo
	Audit    Auditor
	Profiles Profiles // nil: the profile_removed rule is not evaluated
	Logger   *slog.Logger
	// DataDir gives the default folder <DataDir>/registry.
	DataDir string
	// ActiveFolder is the folder the registry runs on now; nil means the saved folder.
	ActiveFolder func() string
	// DiskFloor: below this many free bytes an over_budget recommendation opens
	// whatever the budgets say. Zero means 5 GB.
	DiskFloor int64
	Now       func() time.Time
}

// Service implements registry.Retention.
type Service struct {
	o  Options
	mu sync.Mutex // guards running
	// running is set from the start of a cleanup until it ends.
	running bool
	wg      sync.WaitGroup
}

var _ registry.Retention = (*Service)(nil)

// New returns the service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.DiskFloor == 0 {
		o.DiskFloor = defaultDiskFloor
	}
	return &Service{o: o}
}

// Wait blocks until the cleanups started in the background have ended.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) record(ctx context.Context, actor Actor, action, targetType, id string, details map[string]any) error {
	if s.o.Audit == nil {
		return nil
	}
	return s.o.Audit.Record(ctx, actor, action, targetType, id, details)
}

// ---------------------------------------------------------------- settings

// DefaultFolder is the registry folder when none is set.
func (s *Service) DefaultFolder() string { return filepath.Join(s.o.DataDir, "registry") }

// Settings returns the saved settings with the folder filled in.
func (s *Service) Settings(ctx context.Context) (registry.Settings, error) {
	st, err := s.o.Repo.Settings(ctx)
	if err != nil {
		return st, err
	}
	if st.Folder == "" {
		st.Folder = s.DefaultFolder()
	}
	return st, nil
}

// PendingFolder is the saved folder when the registry does not run on it yet
// (the blobs are not moved), else empty.
func (s *Service) PendingFolder(st registry.Settings) string {
	active := st.Folder
	if s.o.ActiveFolder != nil {
		active = s.o.ActiveFolder()
	}
	if active != "" && filepath.Clean(active) != filepath.Clean(st.Folder) {
		return st.Folder
	}
	return ""
}

// ValidationError is a setting the owner must change; the text is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// PutSettings validates and saves everything but the budgets (R5.42).
func (s *Service) PutSettings(ctx context.Context, in registry.Settings, by string) (registry.Settings, error) {
	cur, err := s.Settings(ctx)
	if err != nil {
		return cur, err
	}
	switch {
	case in.UnusedDays < 1 || in.UnusedDays > 3650:
		return cur, &ValidationError{"unused days must be between 1 and 3650"}
	case in.KeepOldDays < 0 || in.KeepOldDays > 365:
		return cur, &ValidationError{"keep-old days must be between 0 and 365"}
	case in.VariantGoneDays < 0 || in.VariantGoneDays > 365:
		return cur, &ValidationError{"variant days after the branch is gone must be between 0 and 365"}
	case in.VariantUnused < 1 || in.VariantUnused > 365:
		return cur, &ValidationError{"variant unused days must be between 1 and 365"}
	case in.CleanupWeekday < time.Sunday || in.CleanupWeekday > time.Saturday:
		return cur, &ValidationError{"cleanup day must be 0 (Sunday) to 6 (Saturday)"}
	case in.CleanupHour < 0 || in.CleanupHour > 23:
		return cur, &ValidationError{"cleanup hour must be between 0 and 23"}
	}
	folder := in.Folder
	if folder == "" {
		folder = s.DefaultFolder()
	}
	if !filepath.IsAbs(folder) {
		return cur, &ValidationError{"the folder must be a full path"}
	}
	in.StandardBudget, in.CustomBudget = cur.StandardBudget, cur.CustomBudget
	stored := in
	stored.Folder = filepath.Clean(folder)
	if stored.Folder == filepath.Clean(s.DefaultFolder()) {
		stored.Folder = ""
	}
	if err := s.o.Repo.PutSettings(ctx, stored, by); err != nil {
		return cur, err
	}
	if err := s.record(ctx, Actor{Login: by, Source: SourceWeb}, ActionSettings, "registry", "settings",
		map[string]any{"summary": "changed the registry settings", "folder": folder}); err != nil {
		return cur, err
	}
	return s.Settings(ctx)
}

// ---------------------------------------------------------------- delete

// Image reads one image (deleted ones too), or registry.ErrNotFound.
func (s *Service) Image(ctx context.Context, id string) (registry.Image, error) {
	return s.o.Repo.Image(ctx, id)
}

// CheckDelete says what uses an image (R5.46, R5.47).
func (s *Service) CheckDelete(ctx context.Context, imageID string) (registry.InUse, error) {
	img, err := s.o.Repo.Image(ctx, imageID)
	if err != nil {
		return registry.InUse{}, err
	}
	return s.inUse(ctx, img)
}

func (s *Service) inUse(ctx context.Context, img registry.Image) (registry.InUse, error) {
	now := s.o.Now()
	var u registry.InUse
	var err error
	if u.RunningJobs, err = s.o.Repo.RunningJobs(ctx, img.ID); err != nil {
		return u, err
	}
	if u.Pointers, err = s.o.Repo.PointersTo(ctx, img.ID); err != nil {
		return u, err
	}
	u.InRollback = !img.KeepUntil.IsZero() && img.KeepUntil.After(now)
	n, err := s.o.Repo.LiveLeases(ctx, now, img.Name)
	if err != nil {
		return u, err
	}
	u.PendingPulls = n
	return u, nil
}

// Delete removes the manifest of an image; its blobs wait for the cleanup
// (R5.46). Without confirm it answers ErrNeedsConfirmation with what uses the
// image. An image a running job uses, or one a live token names, is refused
// even with confirm (ErrInUse). A pointer or the rollback days need the yes.
func (s *Service) Delete(ctx context.Context, imageID string, confirm bool, by string) (registry.InUse, error) {
	img, err := s.o.Repo.Image(ctx, imageID)
	if err != nil {
		return registry.InUse{}, err
	}
	if img.State == registry.StateDeleted {
		return registry.InUse{}, registry.ErrNotFound
	}
	u, err := s.inUse(ctx, img)
	if err != nil {
		return u, err
	}
	if !confirm {
		return u, ErrNeedsConfirmation
	}
	if len(u.RunningJobs) > 0 || u.PendingPulls > 0 {
		return u, registry.ErrInUse
	}
	actor := Actor{Login: by, Source: SourceWeb}
	if by == "" || by == CrspActor.Login {
		actor = CrspActor
	}
	if err := s.removeImage(ctx, img, actor, u); err != nil {
		return u, err
	}
	return u, nil
}

// removeImage deletes the manifest, marks the row deleted, closes what was
// recommended about it and writes the audit entry (R5.51).
func (s *Service) removeImage(ctx context.Context, img registry.Image, actor Actor, u registry.InUse) error {
	name := img.Name
	if err := s.o.Registry.DeleteManifest(ctx, name, img.Manifest); err != nil && !errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("delete the manifest of %s: %w", img.ID, err)
	}
	if err := s.o.Repo.MarkDeleted(ctx, img.ID, s.o.Now(), actor.Login); err != nil {
		return err
	}
	if err := s.closeFor(ctx, img.ID, WhyActedOn); err != nil {
		return err
	}
	return s.record(ctx, actor, ActionDelete, "registry_image", img.ID, map[string]any{
		"summary": fmt.Sprintf("deleted image %s:%s", img.Name, img.Key),
		"name":    img.Name, "key": string(img.Key), "state": string(img.State), "pointers": len(u.Pointers),
	})
}

func (s *Service) closeFor(ctx context.Context, imageID, why string) error {
	recs, err := s.o.Repo.Recommendations(ctx)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.ImageID == imageID {
			if err := s.o.Repo.Close(ctx, r.ID, why); err != nil {
				return err
			}
		}
	}
	return nil
}
