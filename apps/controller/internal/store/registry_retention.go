package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/retention"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// RegistryRetention is the data part of the registry retention rules (REG6,
// queries/registry_retention.sql); it implements retention.Repo.
type RegistryRetention struct{ s *Store }

var _ retention.Repo = (*RegistryRetention)(nil)

// RegistryRetention returns the retention repository.
func (s *Store) RegistryRetention() *RegistryRetention { return &RegistryRetention{s: s} }

const regTimeFormat = "2006-01-02T15:04:05.000Z"

func regTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(regTimeFormat)
}

func parseRegTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, regTimeFormat} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func regNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return registry.ErrNotFound
	}
	return err
}

func regImage(r sqlcgen.RegistryImage) registry.Image {
	return registry.Image{
		ID: r.ID, Kind: registry.Kind(r.Kind), Name: r.Name, Key: registry.Key(r.ImageTag), Profile: r.ProfileID,
		Arch: r.Arch, State: registry.State(r.State), Manifest: r.ManifestDigest, StackHash: r.StackHash,
		BaseName: r.BaseName, BaseDigest: r.BaseDigest, SourceRepository: r.SourceRepository, Commit: r.CommitSha,
		Branch: r.Branch, PushedBy: r.PushedBy, PushedAt: parseRegTime(r.PushedAt), PromotedAt: parseRegTime(r.PromotedAt),
		Replaces: r.ReplacesID, ReplacedBy: r.ReplacedByID, KeepUntil: parseRegTime(r.KeepUntil),
		BranchGoneAt: parseRegTime(r.BranchGoneAt), SizeBytes: r.SizeBytes, UniqueBytes: r.UniqueBytes,
		LastPullAt: parseRegTime(r.LastPullAt), Scan: registry.ScanState(r.ScanState),
	}
}

// Settings reads the one settings row.
func (r *RegistryRetention) Settings(ctx context.Context) (registry.Settings, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := r.s.q.GetRegistrySettingsRow(ctx)
	if err != nil {
		return registry.Settings{}, fmt.Errorf("read the registry settings: %w", err)
	}
	return registry.Settings{
		Folder: row.Folder, StandardBudget: row.StandardBudget, CustomBudget: row.CustomBudget,
		UnusedDays: int(row.UnusedDays), KeepOldDays: int(row.KeepOldDays), VariantGoneDays: int(row.VariantGoneDays),
		VariantUnused: int(row.VariantUnusedDays), CleanupWeekday: time.Weekday(row.CleanupWeekday), CleanupHour: int(row.CleanupHour),
	}, nil
}

// PutSettings writes the settings row.
func (r *RegistryRetention) PutSettings(ctx context.Context, s registry.Settings, by string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := r.s.q.UpdateRegistrySettingsRow(ctx, sqlcgen.UpdateRegistrySettingsRowParams{
		Folder: s.Folder, StandardBudget: s.StandardBudget, CustomBudget: s.CustomBudget,
		UnusedDays: int64(s.UnusedDays), KeepOldDays: int64(s.KeepOldDays), VariantGoneDays: int64(s.VariantGoneDays),
		VariantUnusedDays: int64(s.VariantUnused), CleanupWeekday: int64(s.CleanupWeekday), CleanupHour: int64(s.CleanupHour),
		UpdatedBy: by,
	})
	if err != nil {
		return fmt.Errorf("save the registry settings: %w", err)
	}
	return nil
}

// Images lists the images whose state is not deleted.
func (r *RegistryRetention) Images(ctx context.Context) ([]registry.Image, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := r.s.q.ListRegistryImages(ctx, sqlcgen.ListRegistryImagesParams{})
	if err != nil {
		return nil, fmt.Errorf("list the registry images: %w", err)
	}
	out := make([]registry.Image, 0, len(rows))
	for _, row := range rows {
		out = append(out, regImage(row))
	}
	return out, nil
}

// Image reads one image, deleted ones too.
func (r *RegistryRetention) Image(ctx context.Context, id string) (registry.Image, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := r.s.q.GetRegistryImage(ctx, id)
	if err != nil {
		return registry.Image{}, regNotFound(err)
	}
	return regImage(row), nil
}

// PointersTo lists the pointers that name an image.
func (r *RegistryRetention) PointersTo(ctx context.Context, imageID string) ([]registry.Pointer, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := r.s.q.ListRegistryPointersTo(ctx, imageID)
	if err != nil {
		return nil, fmt.Errorf("list the pointers of an image: %w", err)
	}
	out := make([]registry.Pointer, 0, len(rows))
	for _, p := range rows {
		out = append(out, registry.Pointer{
			Repository: p.Repository, Profile: p.ProfileID, Arch: p.Arch, ImageID: p.ImageID,
			Previous: p.PreviousImageID, MovedAt: parseRegTime(p.MovedAt), MovedBy: p.MovedBy,
		})
	}
	return out, nil
}

// RunningJobs names the running jobs and the pulls in progress that use an image.
func (r *RegistryRetention) RunningJobs(ctx context.Context, imageID string) ([]string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	jobs, err := r.s.q.ListRunningJobsOfRegistryImage(ctx, imageID)
	if err != nil {
		return nil, fmt.Errorf("list the running jobs of an image: %w", err)
	}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		text := j.Repository + " " + j.Workflow + " " + j.Name
		if j.StartedAt != "" {
			if t := parseRegTime(j.StartedAt); !t.IsZero() {
				text += " (started " + t.UTC().Format("15:04") + " UTC)"
			}
		}
		out = append(out, text)
	}
	machines, err := r.s.q.ListInFlightPullMachinesOfRegistryImage(ctx, imageID)
	if err != nil {
		return nil, fmt.Errorf("list the pulls of an image: %w", err)
	}
	for _, m := range machines {
		out = append(out, "a pull in progress on machine "+m)
	}
	return out, nil
}

// LiveLeases counts the tokens that have not ended and name the image.
func (r *RegistryRetention) LiveLeases(ctx context.Context, now time.Time, name string) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := r.s.q.CountLiveRegistryTokenLeasesNaming(ctx, sqlcgen.CountLiveRegistryTokenLeasesNamingParams{ExpiresAt: regTime(now), Name: name})
	if err != nil {
		return 0, fmt.Errorf("count the live registry tokens: %w", err)
	}
	return int(n), nil
}

// LayerKnown says whether the layer accounting already counts the digest.
func (r *RegistryRetention) LayerKnown(ctx context.Context, digest string) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := r.s.q.RegistryLayerKnown(ctx, digest)
	if err != nil {
		return false, fmt.Errorf("look up a layer: %w", err)
	}
	return n > 0, nil
}

// MarkDeleted marks an image deleted.
func (r *RegistryRetention) MarkDeleted(ctx context.Context, id string, at time.Time, by string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := r.s.q.MarkRegistryImageDeleted(ctx, sqlcgen.MarkRegistryImageDeletedParams{DeletedAt: regTime(at), DeletedBy: by, ID: id})
	if err != nil {
		return fmt.Errorf("mark an image deleted: %w", err)
	}
	if n == 0 {
		return registry.ErrNotFound
	}
	return nil
}

// OldStaged lists staged and refused images pushed before the cutoff.
func (r *RegistryRetention) OldStaged(ctx context.Context, before time.Time) ([]retention.StagedImage, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := r.s.q.ListOldStagedRegistryImages(ctx, regTime(before))
	if err != nil {
		return nil, fmt.Errorf("list old staged images: %w", err)
	}
	out := make([]retention.StagedImage, 0, len(rows))
	for _, row := range rows {
		out = append(out, retention.StagedImage{Image: regImage(row), StagedName: row.StagedName})
	}
	return out, nil
}

// Recommendations lists the open recommendations, owner's first.
func (r *RegistryRetention) Recommendations(ctx context.Context) ([]registry.Recommendation, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := r.s.q.ListOpenRegistryRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the registry recommendations: %w", err)
	}
	out := make([]registry.Recommendation, 0, len(rows))
	for _, x := range rows {
		out = append(out, registry.Recommendation{
			ID: x.ID, Rule: registry.Rule(x.Rule), ImageID: x.ImageID, OtherID: x.OtherImageID, Kind: registry.Kind(x.Kind),
			FreesBytes: x.FreesBytes, Decider: registry.Decider(x.Decider), DueAt: parseRegTime(x.DueAt), CreatedAt: parseRegTime(x.CreatedAt),
		})
	}
	return out, nil
}

// Upsert opens or refreshes the recommendation of (rule, image, kind).
func (r *RegistryRetention) Upsert(ctx context.Context, x registry.Recommendation) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := r.s.q.UpsertRegistryRecommendation(ctx, sqlcgen.UpsertRegistryRecommendationParams{
		ID: x.ID, Rule: string(x.Rule), ImageID: x.ImageID, OtherImageID: x.OtherID, Kind: string(x.Kind),
		FreesBytes: x.FreesBytes, Decider: string(x.Decider), DueAt: regTime(x.DueAt),
	})
	if err != nil {
		return fmt.Errorf("save a registry recommendation: %w", err)
	}
	return nil
}

// Close ends a recommendation: done when it was acted on, gone when its condition ended.
func (r *RegistryRetention) Close(ctx context.Context, id, why string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	state := "gone"
	if why == retention.WhyActedOn {
		state = "done"
	}
	if _, err := r.s.q.CloseRegistryRecommendation(ctx, sqlcgen.CloseRegistryRecommendationParams{State: state, ClosedWhy: why, ID: id}); err != nil {
		return fmt.Errorf("close a registry recommendation: %w", err)
	}
	return nil
}

// DanglingLayers lists the layers no live image holds.
func (r *RegistryRetention) DanglingLayers(ctx context.Context) ([]registry.Layer, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := r.s.q.ListDanglingRegistryLayers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dangling layers: %w", err)
	}
	out := make([]registry.Layer, 0, len(rows))
	for _, l := range rows {
		out = append(out, registry.Layer{Digest: l.Digest, SizeBytes: l.SizeBytes, MediaType: l.MediaType, ChargedTo: registry.Kind(l.ChargedTo)})
	}
	return out, nil
}

// DeleteLayer forgets a layer after the sweep removed its blob.
func (r *RegistryRetention) DeleteLayer(ctx context.Context, digest string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := r.s.q.DeleteRegistryLayer(ctx, digest); err != nil {
		return fmt.Errorf("forget a layer: %w", err)
	}
	return nil
}

// Use gives the bytes charged to each kind and the part shared with the other.
func (r *RegistryRetention) Use(ctx context.Context) (map[registry.Kind]retention.KindUse, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	out := map[registry.Kind]retention.KindUse{}
	sums, err := r.s.q.SumRegistryLayersByKind(ctx)
	if err != nil {
		return nil, fmt.Errorf("sum the layers: %w", err)
	}
	for _, x := range sums {
		u := out[registry.Kind(x.ChargedTo)]
		u.UsedBytes = int64(x.Bytes.Float64)
		out[registry.Kind(x.ChargedTo)] = u
	}
	shared, err := r.s.q.SumRegistrySharedLayers(ctx)
	if err != nil {
		return nil, fmt.Errorf("sum the shared layers: %w", err)
	}
	for _, x := range shared {
		u := out[registry.Kind(x.ChargedTo)]
		u.SharedBytes = x.Bytes
		out[registry.Kind(x.ChargedTo)] = u
	}
	return out, nil
}

// SetReadOnly records the read-only window of the cleanup.
func (r *RegistryRetention) SetReadOnly(ctx context.Context, on bool) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	v := int64(0)
	if on {
		v = 1
	}
	if err := r.s.q.SetRegistryReadOnly(ctx, v); err != nil {
		return fmt.Errorf("set the registry read-only flag: %w", err)
	}
	return nil
}

// StartCleanup records a cleanup that begins.
func (r *RegistryRetention) StartCleanup(ctx context.Context, trigger, by string) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	id, err := r.s.q.InsertRegistryCleanup(ctx, sqlcgen.InsertRegistryCleanupParams{Trigger: trigger, StartedBy: by})
	if err != nil {
		return 0, fmt.Errorf("record a registry cleanup: %w", err)
	}
	return id, nil
}

// FinishCleanup records how a cleanup ended.
func (r *RegistryRetention) FinishCleanup(ctx context.Context, id int64, state string, freed int64, errText string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := r.s.q.FinishRegistryCleanup(ctx, sqlcgen.FinishRegistryCleanupParams{State: state, FreedBytes: freed, Error: errText, ID: id}); err != nil {
		return fmt.Errorf("finish a registry cleanup: %w", err)
	}
	return nil
}

func regCleanup(c sqlcgen.RegistryCleanup) retention.Cleanup {
	return retention.Cleanup{
		ID: c.ID, Trigger: c.Trigger, State: c.State, FreedBytes: c.FreedBytes, Error: c.Error, StartedBy: c.StartedBy,
		StartedAt: parseRegTime(c.StartedAt), FinishedAt: parseRegTime(c.FinishedAt),
	}
}

// Cleanup reads one cleanup.
func (r *RegistryRetention) Cleanup(ctx context.Context, id int64) (retention.Cleanup, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	c, err := r.s.q.GetRegistryCleanup(ctx, id)
	if err != nil {
		return retention.Cleanup{}, regNotFound(err)
	}
	return regCleanup(c), nil
}

// LastCleanup reads the latest cleanup, or registry.ErrNotFound.
func (r *RegistryRetention) LastCleanup(ctx context.Context) (retention.Cleanup, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	c, err := r.s.q.LastRegistryCleanup(ctx)
	if err != nil {
		return retention.Cleanup{}, regNotFound(err)
	}
	return regCleanup(c), nil
}
