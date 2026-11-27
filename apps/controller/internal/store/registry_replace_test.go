package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

func TestRegistryReplacePlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, id := range []string{"img_old", "img_new"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO registry_images (id, kind, name, image_tag, profile_id, arch, state, manifest_digest, stack_hash, base_digest)
			VALUES (?, 'custom', 'crsp/custom/acme-app/node-web', ?, 'node-web', 'amd64', 'current', 'sha256:x', 'h', 'sha256:b')`, id, id[len(id)-3:]+"000000000"); err != nil {
			t.Fatal(err)
		}
	}
	p := registry.ReplacePlan{ID: "rpl_1", NewImageID: "img_new", OldImageID: "img_old", KeepDays: 7,
		Repositories: []string{"acme/app"}, Machines: nil, StepsTotal: 1, CreatedBy: "owner"}
	if err := s.InsertReplacePlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetReplacePlan(ctx, "rpl_1")
	if err != nil || got.State != registry.PlanDraft || got.Repositories[0] != "acme/app" || got.Machines == nil || got.CreatedAt.IsZero() {
		t.Fatalf("draft: %+v %v", got, err)
	}
	if _, err := s.GetReplacePlan(ctx, "none"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, ok, _ := s.ActiveReplacePlanForImage(ctx, "img_old"); !ok {
		t.Fatal("draft plan not active")
	}

	// A new controller reads the running state (R5.58).
	until := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	got.State, got.StepsDone, got.StartedAt, got.KeepUntil = registry.PlanRunning, 1, until.Add(-7*24*time.Hour), until
	if err := s.SaveReplacePlan(ctx, got); err != nil {
		t.Fatal(err)
	}
	running, err := s.ReplacePlansByState(ctx, registry.PlanRunning)
	if err != nil || len(running) != 1 || running[0].StepsDone != 1 || !running[0].KeepUntil.Equal(until) {
		t.Fatalf("running: %+v %v", running, err)
	}

	if ok, err := s.SupersedeRegistryImage(ctx, "img_old", "img_new", until); err != nil || !ok {
		t.Fatalf("supersede: %v %v", ok, err)
	}
	var state, by, keep string
	if err := s.db.QueryRowContext(ctx, `SELECT state, replaced_by_id, keep_until FROM registry_images WHERE id='img_old'`).Scan(&state, &by, &keep); err != nil {
		t.Fatal(err)
	}
	if state != "superseded" || by != "img_new" || ParseTime(keep) != until {
		t.Fatalf("image: %s %s %s", state, by, keep)
	}
	if ok, _ := s.SupersedeRegistryImage(ctx, "img_old", "img_new", until); ok {
		t.Fatal("superseded twice")
	}

	// Rolled back only from done.
	if ok, _ := s.MarkReplacePlanRolledBack(ctx, "rpl_1", "owner", until); ok {
		t.Fatal("rolled back a running plan")
	}
	got.State = registry.PlanDone
	if err := s.SaveReplacePlan(ctx, got); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkReplacePlanRolledBack(ctx, "rpl_1", "owner", until); err != nil || !ok {
		t.Fatalf("rollback mark: %v %v", ok, err)
	}
	if ok, err := s.RestoreRegistryImage(ctx, "img_old"); err != nil || !ok {
		t.Fatalf("restore: %v %v", ok, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT state, replaced_by_id, keep_until FROM registry_images WHERE id='img_old'`).Scan(&state, &by, &keep); err != nil || state != "current" || by != "" || keep != "" {
		t.Fatalf("restored: %s %s %s %v", state, by, keep, err)
	}
	if _, ok, _ := s.ActiveReplacePlanForImage(ctx, "img_old"); ok {
		t.Fatal("rolled back plan still active")
	}
}
