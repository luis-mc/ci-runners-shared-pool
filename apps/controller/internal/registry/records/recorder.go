package records

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

func isFinished(state string) bool { return state == "done" || state == "failed" }

// findImage resolves the promoted image a pull or event names: by manifest digest first, then by key and arch,
// then by key alone. It returns false when the registry has no such image.
func (s *Service) findImage(ctx context.Context, manifest string, k registry.Key, arch string) (sqlcgen.RegistryImage, bool) {
	if manifest != "" {
		if r, err := s.q.GetRegistryImageByManifest(ctx, manifest); err == nil {
			return r, true
		}
	}
	if k.Valid() && arch != "" {
		if r, err := s.q.GetPromotedRegistryImageByKey(ctx, sqlcgen.GetPromotedRegistryImageByKeyParams{
			ImageTag: k.String(), Arch: registry.OCIArch(arch)}); err == nil {
			return r, true
		}
	}
	if k.Valid() {
		if r, err := s.q.GetRegistryImageByTag(ctx, k.String()); err == nil {
			return r, true
		}
	}
	return sqlcgen.RegistryImage{}, false
}

// RecordPull keeps a PullReport (R5.30): one registry_pulls row per pull id, so a resent or later report of the same
// pull updates the row. When the pull is done and came from the registry it adds one to the day's total of the
// image, once: a resent "done" report is not counted twice. Time saved is what the agent says, or else the typical
// build time of the profile and arch minus the pull time.
func (s *Service) RecordPull(ctx context.Context, r registry.PullRecord) error {
	if r.PullID == "" || r.MachineID == "" {
		return fmt.Errorf("%w: a pull needs its id and its machine", errBadRecord)
	}
	switch r.State {
	case "pulling", "waiting_controller", "done", "failed":
	default:
		return fmt.Errorf("%w: pull state %q", errBadRecord, r.State)
	}
	switch r.Source {
	case registry.SourceLocal, registry.SourcePulled, registry.SourceBuilt:
	default:
		return fmt.Errorf("%w: pull source %q", errBadRecord, r.Source)
	}
	at := r.At
	if at.IsZero() {
		at = s.now()
	}
	prev, err := s.q.GetRegistryPullByID(ctx, r.PullID)
	existed := err == nil
	if err != nil && !noRows(err) {
		return err
	}
	// A late progress report never reopens a pull that has ended.
	if existed && isFinished(prev.State) && !isFinished(r.State) {
		return nil
	}
	img, known := s.findImage(ctx, r.Manifest, r.Key, r.Arch)
	imageID, manifest := "", r.Manifest
	if known {
		imageID = img.ID
		if manifest == "" {
			manifest = img.ManifestDigest
		}
	}
	started := stamp(at)
	if existed && prev.StartedAt != "" {
		started = prev.StartedAt
	}
	finished := ""
	if isFinished(r.State) {
		finished = stamp(at)
	}
	saved := r.SavedSeconds
	if saved == 0 && known && r.State == "done" && r.Source == registry.SourcePulled {
		saved = s.savedSeconds(ctx, img, r.Seconds)
	}
	if err := s.q.UpsertRegistryPull(ctx, sqlcgen.UpsertRegistryPullParams{
		PullID: r.PullID, MachineID: r.MachineID, ImageID: imageID, ImageTag: r.Key.String(), ManifestDigest: manifest,
		Source: string(r.Source), State: r.State, BytesDone: r.BytesDone, BytesTotal: r.BytesTotal, Seconds: r.Seconds,
		SavedSeconds: saved, Repository: r.Repository, JobID: r.JobID, Error: r.Error, StartedAt: started, FinishedAt: finished,
	}); err != nil {
		return err
	}
	if r.State == "done" && known && r.Source != registry.SourceBuilt {
		if err := s.q.SetRegistryImageLastPull(ctx, sqlcgen.SetRegistryImageLastPullParams{
			LastPullAt: stamp(at), ID: imageID, LastPullAt_2: stamp(at)}); err != nil {
			return err
		}
	}
	if r.State == "done" && known && r.Source == registry.SourcePulled && (!existed || prev.State != "done") {
		if err := s.q.AddRegistryPullDaily(ctx, sqlcgen.AddRegistryPullDailyParams{
			ImageID: imageID, Day: s.day(at), BuildsSaved: 1, SavedSeconds: saved}); err != nil {
			return err
		}
	}
	s.pruneSometimes(ctx)
	return nil
}

// savedSeconds is the typical build time of the profile and arch minus the pull time, never below zero. With no
// build history it is zero.
func (s *Service) savedSeconds(ctx context.Context, img sqlcgen.RegistryImage, pullSeconds float64) float64 {
	v, err := s.q.TypicalImageBuildSeconds(ctx, sqlcgen.TypicalImageBuildSecondsParams{ProfileID: img.ProfileID, Arch: img.Arch})
	if err != nil {
		return 0
	}
	typical := asFloat(v)
	if typical <= pullSeconds {
		return 0
	}
	return typical - pullSeconds
}

// RecordPush keeps a PushReport: one registry_pushes row per push id. A resent "pushed" report does not undo the
// outcome the promotion wrote (promoted or refused, SetPushOutcome).
func (s *Service) RecordPush(ctx context.Context, r registry.PushRecord) error {
	if r.PushID == "" || r.MachineID == "" {
		return fmt.Errorf("%w: a push needs its id and its machine", errBadRecord)
	}
	switch r.State {
	case "pushing", "waiting_controller", "pushed", "failed":
	default:
		return fmt.Errorf("%w: push state %q", errBadRecord, r.State)
	}
	at := r.At
	if at.IsZero() {
		at = s.now()
	}
	prev, err := s.q.GetRegistryPushByID(ctx, r.PushID)
	existed := err == nil
	if err != nil && !noRows(err) {
		return err
	}
	state, imageID, started, finished := r.State, "", stamp(at), ""
	if existed {
		imageID = prev.ImageID
		if prev.StartedAt != "" {
			started = prev.StartedAt
		}
		if prev.State == "promoted" || prev.State == "refused" {
			state = prev.State
		}
		finished = prev.FinishedAt
	}
	if r.State == "pushed" || r.State == "failed" {
		finished = stamp(at)
	}
	return s.q.UpsertRegistryPush(ctx, sqlcgen.UpsertRegistryPushParams{
		PushID: r.PushID, MachineID: r.MachineID, ImageTag: r.Key.String(), StagedName: r.Name, TargetName: r.Target,
		ManifestDigest: r.Manifest, State: state, ImageID: imageID, Seconds: r.Seconds, Error: r.Error,
		StartedAt: started, FinishedAt: finished})
}

// SetPushOutcome is how the promotion tells the records what became of a pushed image: promoted with the image id,
// or refused with the reason. It does nothing for an unknown push.
func (s *Service) SetPushOutcome(ctx context.Context, pushID string, promoted bool, imageID, why string) error {
	prev, err := s.q.GetRegistryPushByID(ctx, pushID)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	state := "refused"
	if promoted {
		state = "promoted"
	}
	return s.q.UpsertRegistryPush(ctx, sqlcgen.UpsertRegistryPushParams{
		PushID: prev.PushID, MachineID: prev.MachineID, ImageTag: prev.ImageTag, StagedName: prev.StagedName,
		TargetName: prev.TargetName, ManifestDigest: prev.ManifestDigest, State: state, ImageID: imageID,
		Seconds: prev.Seconds, Error: why, StartedAt: prev.StartedAt, FinishedAt: stamp(s.now())})
}

// RecordEvent keeps a registry event. A pull of a promoted manifest moves the image's last pull time (it is what
// "unused for N days" reads). It adds no daily count: the PullReport of the agent is the one count of a pull, so a
// pull is never counted twice. Pushes are the business of PushReport and the promotion.
func (s *Service) RecordEvent(ctx context.Context, ev registry.Event) error {
	if ev.Action != "pull" || !strings.HasPrefix(ev.Digest, "sha256:") {
		return nil
	}
	img, ok := s.findImage(ctx, ev.Digest, "", "")
	if !ok {
		return nil
	}
	at := ev.At
	if at.IsZero() {
		at = s.now()
	}
	return s.q.SetRegistryImageLastPull(ctx, sqlcgen.SetRegistryImageLastPullParams{
		LastPullAt: stamp(at), ID: img.ID, LastPullAt_2: stamp(at)})
}

// Consume reads events until ctx ends or the channel closes.
func (s *Service) Consume(ctx context.Context, events <-chan registry.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := s.RecordEvent(ctx, ev); err != nil {
				s.cfg.Logger.Warn("registry: could not keep an event", "err", err)
			}
		}
	}
}

// Prune removes pull rows older than 30 days and daily totals older than 180 days (R5.30).
func (s *Service) Prune(ctx context.Context) error {
	now := s.now()
	if _, err := s.q.DeleteOldRegistryPulls(ctx, stamp(now.Add(-PullRetention))); err != nil {
		return err
	}
	_, err := s.q.DeleteOldRegistryPullDaily(ctx, s.day(now.Add(-DailyRetention)))
	return err
}

func (s *Service) pruneSometimes(ctx context.Context) {
	now := s.now().Unix()
	last := s.lastPrune.Load()
	if now-last < int64(pruneEvery/time.Second) || !s.lastPrune.CompareAndSwap(last, now) {
		return
	}
	if err := s.Prune(ctx); err != nil {
		s.cfg.Logger.Warn("registry: could not prune old records", "err", err)
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
