package retention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Cleanup triggers.
const (
	TriggerWeekly = "weekly"
	TriggerNow    = "now"
)

// catchUp is how long after its time a missed weekly cleanup still runs.
const catchUp = 24 * time.Hour

// Cleanup runs the cleanup in the foreground and returns the bytes freed.
func (s *Service) Cleanup(ctx context.Context, trigger string) (int64, error) {
	id, err := s.begin(ctx, trigger, CrspActor.Login)
	if err != nil {
		return 0, err
	}
	return s.run(ctx, id, trigger, CrspActor)
}

// StartCleanup begins a cleanup and runs it in the background (Clean up now).
// It returns ErrCleanupRunning when one runs.
func (s *Service) StartCleanup(ctx context.Context, trigger, by string) (Cleanup, error) {
	id, err := s.begin(ctx, trigger, by)
	if err != nil {
		return Cleanup{}, err
	}
	row, err := s.o.Repo.Cleanup(ctx, id)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return Cleanup{}, err
	}
	actor := Actor{Login: by, Source: SourceWeb}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if _, err := s.run(context.WithoutCancel(ctx), id, trigger, actor); err != nil {
			s.o.Logger.Warn("registry cleanup failed", "err", err)
		}
	}()
	return row, nil
}

func (s *Service) begin(ctx context.Context, trigger, by string) (int64, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, ErrCleanupRunning
	}
	s.running = true
	s.mu.Unlock()
	id, err := s.o.Repo.StartCleanup(ctx, trigger, by)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return 0, err
	}
	return id, nil
}

// run is the cleanup: crsp's own removals, a short read-only window (pushes and
// promotions wait, pulls keep working), a dry run, mark and sweep, back to
// read-write, bytes freed recorded (R5.48).
func (s *Service) run(ctx context.Context, id int64, trigger string, actor Actor) (freed int64, err error) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	readOnly := false
	defer func() {
		if readOnly {
			s.o.Registry.SetReadOnly(false)
			if e := s.o.Repo.SetReadOnly(context.WithoutCancel(ctx), false); e != nil && err == nil {
				err = e
			}
		}
		state, text := "done", ""
		if err != nil {
			state, text = "failed", err.Error()
		}
		if e := s.o.Repo.FinishCleanup(context.WithoutCancel(ctx), id, state, freed, text); e != nil && err == nil {
			err = e
		}
		if err == nil {
			err = s.record(context.WithoutCancel(ctx), actor, ActionCleanup, "registry", fmt.Sprint(id), map[string]any{
				"summary": fmt.Sprintf("registry cleanup freed %d bytes", freed), "trigger": trigger, "freed_bytes": freed})
		}
	}()

	now := s.o.Now()
	if e := s.Evaluate(ctx, now); e != nil {
		s.o.Logger.Warn("registry cleanup: recommendations not refreshed", "err", e)
	}

	// The read-only window.
	if err = s.o.Repo.SetReadOnly(ctx, true); err != nil {
		return 0, err
	}
	s.o.Registry.SetReadOnly(true)
	readOnly = true
	if err = s.record(ctx, actor, ActionReadOnly, "registry", fmt.Sprint(id), map[string]any{
		"summary": "registry set read-only for the cleanup", "trigger": trigger}); err != nil {
		return 0, err
	}

	// Staged and refused images older than 1 day (R5.52).
	old, e := s.o.Repo.OldStaged(ctx, now.Add(-stagedKeepDuration))
	if e != nil {
		return 0, e
	}
	for _, img := range old {
		name := img.StagedName
		if name == "" {
			continue
		}
		if e := s.o.Registry.DeleteManifest(ctx, name, img.Manifest); e != nil && !errors.Is(e, registry.ErrNotFound) {
			s.o.Logger.Warn("registry cleanup: could not remove a staged image", "image", img.ID, "err", e)
			continue
		}
		if e := s.o.Repo.MarkDeleted(ctx, img.ID, now, CrspActor.Login); e != nil {
			return 0, e
		}
	}

	// Dry run first: a failing mark stops before anything is swept.
	if _, err = s.o.Registry.GC(ctx, true); err != nil {
		return 0, fmt.Errorf("garbage collection dry run: %w", err)
	}
	if freed, err = s.o.Registry.GC(ctx, false); err != nil {
		return 0, fmt.Errorf("garbage collection: %w", err)
	}
	dangling, e := s.o.Repo.DanglingLayers(ctx)
	if e != nil {
		return freed, e
	}
	for _, l := range dangling {
		if e := s.o.Repo.DeleteLayer(ctx, l.Digest); e != nil {
			return freed, e
		}
	}
	return freed, nil
}

// Tick is what the controller calls every few minutes: it refreshes the
// recommendations and, when the weekly time has come and no cleanup ran since,
// runs the weekly cleanup (R5.42, R5.48).
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	if err := s.Evaluate(ctx, now); err != nil {
		return err
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	due := prevCleanup(now, st)
	if now.Sub(due) >= catchUp {
		return nil
	}
	last, err := s.o.Repo.LastCleanup(ctx)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		return err
	}
	if err == nil && !last.StartedAt.Before(due) {
		return nil
	}
	_, err = s.Cleanup(ctx, TriggerWeekly)
	if errors.Is(err, ErrCleanupRunning) {
		return nil
	}
	return err
}

// LastCleanup is the latest cleanup, or registry.ErrNotFound.
func (s *Service) LastCleanup(ctx context.Context) (Cleanup, error) { return s.o.Repo.LastCleanup(ctx) }
