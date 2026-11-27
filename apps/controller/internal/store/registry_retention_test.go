package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/retention"
)

type sqlDisk struct{}

func (sqlDisk) Free(string) (int64, error) { return 1_000_000_000_000, nil }

type sqlRegistry struct{ deleted []string }

func (r *sqlRegistry) SetReadOnly(bool) {}
func (r *sqlRegistry) DeleteManifest(_ context.Context, name, digest string) error {
	r.deleted = append(r.deleted, name+"@"+digest)
	return nil
}
func (r *sqlRegistry) GC(context.Context, bool) (int64, error) { return 123, nil }

type sqlAudit struct{ actions []string }

func (a *sqlAudit) Record(_ context.Context, _ retention.Actor, action, _, _ string, _ map[string]any) error {
	a.actions = append(a.actions, action)
	return nil
}

func regExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func regImg(t *testing.T, s *Store, id, kind, name, state, extra string) {
	t.Helper()
	regExec(t, s, `INSERT INTO registry_images (id, kind, name, image_tag, profile_id, arch, state, manifest_digest, stack_hash, base_digest,
		pushed_at, promoted_at, keep_until, branch_gone_at, last_pull_at) VALUES (?, ?, ?, ?, 'node', 'amd64', ?, ?, 'h', 'sha256:0',
		'2026-11-01T00:00:00.000Z', '2026-11-01T00:00:00.000Z', ?, '', '')`, id, kind, name, (id + "000000000000")[:12], state, "sha256:"+id, extra)
}

// The retention rules run end to end on SQLite: queries, the in-use refusal naming the job, and the audit entry.
func TestRegistryRetentionOnSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reg, aud := &sqlRegistry{}, &sqlAudit{}
	data, _ := filepath.Abs("/data") // a full path on Windows too
	svc := retention.New(retention.Options{Repo: s.RegistryRetention(), Registry: reg, Disk: sqlDisk{}, Audit: aud, DataDir: data, Now: func() time.Time { return now }})

	// Defaults (R5.41, R5.42).
	st, err := svc.Settings(ctx)
	if err != nil || st.StandardBudget != 40_000_000_000 || st.CustomBudget != 30_000_000_000 || st.UnusedDays != 30 ||
		st.KeepOldDays != 7 || st.Folder != filepath.Join(data, "registry") || st.CleanupHour != 3 || st.CleanupWeekday != time.Sunday {
		t.Fatalf("%+v %v", st, err)
	}

	regImg(t, s, "img_a", "standard", "crsp/std/node", "superseded", "2026-11-18T00:00:00.000Z") // keep_until passed
	regImg(t, s, "img_b", "custom", "crsp/custom/o-r/node", "current", "")
	regImg(t, s, "img_c", "standard", "crsp/std/live", "current", "")
	regExec(t, s, `INSERT INTO registry_layers (digest, size_bytes, charged_to) VALUES ('sha256:l1', 100, 'standard'), ('sha256:l2', 50, 'custom'), ('sha256:l3', 7, 'standard')`)
	regExec(t, s, `INSERT INTO registry_image_layers (image_id, layer_digest) VALUES ('img_c', 'sha256:l1'), ('img_b', 'sha256:l1'), ('img_b', 'sha256:l2')`)

	b, err := svc.BudgetReport(ctx)
	if err != nil || b.Standard.UsedBytes != 107 || b.Custom.UsedBytes != 50 || b.Standard.SharedBytes != 100 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := svc.PutBudgets(ctx, 10_000_000_000, 5_000_000_000, "luis"); err != nil {
		t.Fatal(err)
	}
	var big *retention.BudgetTooBig
	if _, err := svc.PutBudgets(ctx, 400_000_000_000, 400_000_000_000, "luis"); !errors.As(err, &big) || big.MaxTotalBytes != 500_000_000_157 {
		t.Fatalf("budget cap: %v", err)
	}

	// The in-use refusal names the job (R5.46).
	regExec(t, s, `INSERT INTO jobs (id, repository_id, repository, run_id, run_attempt, workflow, name, state, started_at) VALUES (42, 1, 'o/app', 9, 1, 'ci.yml', 'test', 'running', '2026-11-19T11:05:00.000Z')`)
	regExec(t, s, `INSERT INTO registry_pulls (pull_id, machine_id, image_id, image_tag, source, state, job_id) VALUES ('p1', 'm1', 'img_c', 'tag', 'pulled', 'done', '42')`)
	u, err := svc.Delete(ctx, "img_c", true, "luis")
	if !errors.Is(err, registry.ErrInUse) || len(u.RunningJobs) != 1 || u.RunningJobs[0] != "o/app ci.yml test (started 11:05 UTC)" {
		t.Fatalf("in-use refusal: %v %+v", err, u)
	}
	regExec(t, s, `UPDATE jobs SET state = 'done' WHERE id = 42`)

	// A live token that names the image holds it back; an ended one does not.
	regExec(t, s, `INSERT INTO registry_token_leases (jti, machine_id, names, expires_at) VALUES ('j1', 'm1', '["crsp/std/live"]', '2026-11-19T12:05:00.000Z')`)
	if _, err := svc.Delete(ctx, "img_c", true, "luis"); !errors.Is(err, registry.ErrInUse) {
		t.Fatalf("token: %v", err)
	}
	regExec(t, s, `UPDATE registry_token_leases SET expires_at = '2026-11-19T11:59:00.000Z'`)
	if _, err := svc.Delete(ctx, "img_c", false, "luis"); !errors.Is(err, retention.ErrNeedsConfirmation) {
		t.Fatalf("no confirm: %v", err)
	}

	// Evaluate: the superseded image past keep_until goes alone; the audit entry is written.
	if err := svc.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RegistryRetention().Image(ctx, "img_a"); got.State != registry.StateDeleted {
		t.Fatalf("img_a is %s", got.State)
	}
	if len(reg.deleted) != 1 || reg.deleted[0] != "crsp/std/node@sha256:img_a" {
		t.Fatalf("%v", reg.deleted)
	}
	// Owner rules wait: img_b and img_c are old enough to be unused (no pull ever, promoted 18 days ago: not yet).
	recs, err := svc.Recommendations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Decider == registry.DecidedByCrsp && r.Rule != registry.RuleDangling {
			t.Fatalf("an unexpected crsp recommendation %+v", r)
		}
	}
	// l3 is held by no image: dangling, and it is crsp's.
	var dangling bool
	for _, r := range recs {
		dangling = dangling || (r.Rule == registry.RuleDangling && r.FreesBytes == 7 && r.Decider == registry.DecidedByCrsp)
	}
	if !dangling {
		t.Fatalf("no dangling recommendation: %+v", recs)
	}

	// Delete with a yes, then the cleanup records bytes freed and forgets the dangling layer.
	if _, err := svc.Delete(ctx, "img_c", true, "luis"); err != nil {
		t.Fatal(err)
	}
	freed, err := svc.Cleanup(ctx, retention.TriggerNow)
	if err != nil || freed != 123 {
		t.Fatalf("%d %v", freed, err)
	}
	last, err := svc.LastCleanup(ctx)
	if err != nil || last.State != "done" || last.FreedBytes != 123 || last.Trigger != "now" {
		t.Fatalf("%+v %v", last, err)
	}
	var left int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM registry_layers WHERE digest = 'sha256:l3'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the swept layer row stays: %d %v", left, err)
	}
	var ro int
	if err := s.db.QueryRowContext(ctx, `SELECT read_only FROM registry_settings`).Scan(&ro); err != nil || ro != 0 {
		t.Fatalf("read-only flag %d %v", ro, err)
	}
	want := map[string]bool{retention.ActionBudgets: true, retention.ActionDelete: true, retention.ActionReadOnly: true, retention.ActionCleanup: true}
	for _, a := range aud.actions {
		delete(want, a)
	}
	if len(want) != 0 {
		t.Fatalf("missing audit actions %v in %v", want, aud.actions)
	}
	if _, err := s.RegistryRetention().LastCleanup(ctx); err != nil {
		t.Fatal(err)
	}
}

// Recommendations survive a second pass without a duplicate, and close when the condition ends (R5.45).
func TestRegistryRecommendationsOnSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	svc := retention.New(retention.Options{Repo: s.RegistryRetention(), Registry: &sqlRegistry{}, Disk: sqlDisk{}, DataDir: "/d", Now: func() time.Time { return now }})
	regImg(t, s, "img_u", "custom", "crsp/custom/o-r/node", "current", "") // promoted 2026-11-01, never pulled: unused 59 days
	for i := 0; i < 2; i++ {
		if err := svc.Evaluate(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := svc.Recommendations(ctx)
	if len(recs) != 1 || recs[0].Rule != registry.RuleUnused || recs[0].Decider != registry.DecidedByOwner || !recs[0].DueAt.IsZero() {
		t.Fatalf("%+v", recs)
	}
	regExec(t, s, `UPDATE registry_images SET last_pull_at = '2026-12-29T00:00:00.000Z'`)
	if err := svc.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	if recs, _ = svc.Recommendations(ctx); len(recs) != 0 {
		t.Fatalf("stays after the condition ended: %+v", recs)
	}
	var state, why string
	if err := s.db.QueryRowContext(ctx, `SELECT state, closed_why FROM registry_recommendations`).Scan(&state, &why); err != nil || state != "gone" {
		t.Fatalf("%s %s %v", state, why, err)
	}
}
