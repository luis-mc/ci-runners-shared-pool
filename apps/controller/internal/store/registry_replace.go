package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file is the store side of replace plans (registry R5.54 to R5.58,
// queries/registry_replace.sql).

func formatPlanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeFormat)
}

func planFromRow(r sqlcgen.RegistryReplacePlan) (registry.ReplacePlan, error) {
	p := registry.ReplacePlan{
		ID: r.ID, NewImageID: r.NewImageID, OldImageID: r.OldImageID,
		KeepDays: int(r.KeepDays), KeepUntil: ParseTime(r.KeepUntil),
		State: registry.PlanState(r.State), StepsDone: int(r.StepsDone), StepsTotal: int(r.StepsTotal),
		Error: r.Error, CreatedBy: r.CreatedBy, CreatedAt: ParseTime(r.CreatedAt),
		StartedAt: ParseTime(r.StartedAt), FinishedAt: ParseTime(r.FinishedAt),
	}
	if r.KeepUntil == "" {
		p.KeepUntil = time.Time{}
	}
	if err := json.Unmarshal([]byte(r.Repositories), &p.Repositories); err != nil {
		return p, fmt.Errorf("plan %s repositories: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.Machines), &p.Machines); err != nil {
		return p, fmt.Errorf("plan %s machines: %w", r.ID, err)
	}
	return p, nil
}

// InsertReplacePlan stores a new plan (draft).
func (s *Store) InsertReplacePlan(ctx context.Context, p registry.ReplacePlan) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	repos, err := json.Marshal(nonNil(p.Repositories))
	if err != nil {
		return err
	}
	machines, err := json.Marshal(nonNil(p.Machines))
	if err != nil {
		return err
	}
	return s.q.InsertRegistryReplacePlan(ctx, sqlcgen.InsertRegistryReplacePlanParams{
		ID: p.ID, NewImageID: p.NewImageID, OldImageID: p.OldImageID, KeepDays: int64(p.KeepDays),
		Repositories: string(repos), Machines: string(machines), StepsTotal: int64(p.StepsTotal), CreatedBy: p.CreatedBy,
	})
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// GetReplacePlan reads one plan, or registry.ErrNotFound.
func (s *Store) GetReplacePlan(ctx context.Context, id string) (registry.ReplacePlan, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetRegistryReplacePlan(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.ReplacePlan{}, registry.ErrNotFound
	}
	if err != nil {
		return registry.ReplacePlan{}, err
	}
	return planFromRow(r)
}

// SaveReplacePlan writes the moving part of a plan: state, progress, error and times.
func (s *Store) SaveReplacePlan(ctx context.Context, p registry.ReplacePlan) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.SetRegistryReplacePlanState(ctx, sqlcgen.SetRegistryReplacePlanStateParams{
		State: string(p.State), StepsDone: int64(p.StepsDone), Error: p.Error,
		StartedAt: formatPlanTime(p.StartedAt), FinishedAt: formatPlanTime(p.FinishedAt),
		KeepUntil: formatPlanTime(p.KeepUntil), ID: p.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return registry.ErrNotFound
	}
	return nil
}

// MarkReplacePlanRolledBack sets a done plan to rolled_back; false when it was not done.
func (s *Store) MarkReplacePlanRolledBack(ctx context.Context, id, by string, at time.Time) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.SetRegistryReplacePlanRolledBack(ctx, sqlcgen.SetRegistryReplacePlanRolledBackParams{
		RolledBackBy: by, RolledBackAt: formatPlanTime(at), ID: id,
	})
	return n > 0, err
}

// ActiveReplacePlanForImage finds the draft or running plan that names the image.
func (s *Store) ActiveReplacePlanForImage(ctx context.Context, imageID string) (registry.ReplacePlan, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetActiveRegistryReplacePlanForImage(ctx, imageID)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.ReplacePlan{}, false, nil
	}
	if err != nil {
		return registry.ReplacePlan{}, false, err
	}
	p, err := planFromRow(r)
	return p, err == nil, err
}

// ReplacePlansByState lists plans in one state, oldest first.
func (s *Store) ReplacePlansByState(ctx context.Context, state registry.PlanState) ([]registry.ReplacePlan, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListRegistryReplacePlansByState(ctx, string(state))
	if err != nil {
		return nil, err
	}
	out := make([]registry.ReplacePlan, 0, len(rows))
	for _, r := range rows {
		p, err := planFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// SupersedeRegistryImage marks oldID superseded by newID, kept until keepUntil.
// An image that is not current or variant is left as it is (false).
func (s *Store) SupersedeRegistryImage(ctx context.Context, oldID, newID string, keepUntil time.Time) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.SupersedeRegistryImage(ctx, sqlcgen.SupersedeRegistryImageParams{
		ReplacedByID: newID, KeepUntil: formatPlanTime(keepUntil), ID: oldID,
	})
	return n > 0, err
}

// RestoreRegistryImage makes a superseded image current again (rollback).
func (s *Store) RestoreRegistryImage(ctx context.Context, id string) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.RestoreRegistryImage(ctx, id)
	return n > 0, err
}
