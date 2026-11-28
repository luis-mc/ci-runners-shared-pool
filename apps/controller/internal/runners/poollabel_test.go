package runners

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

type fakeLabeler struct {
	added   []string
	removed []string
	err     error
}

func (f *fakeLabeler) AddRunnerLabels(_ context.Context, _ int64, _ github.RunnerScope, id int64, labels []string) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, labels...)
	return nil
}

func (f *fakeLabeler) RemoveRunnerLabel(_ context.Context, _ int64, _ github.RunnerScope, id int64, label string) error {
	if f.err != nil {
		return f.err
	}
	f.removed = append(f.removed, label)
	return nil
}

func TestPoolLabelMoveKeepsRecordAndGitHubInStep(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	setGH(r, rec.ID, 99)
	_ = r.store.SetRunnerLabels(ctx, rec.ID, []string{"self-hosted", "linux", "pool-a"})
	lab := &fakeLabeler{}
	r.svc.Labeler = lab

	hs, err := r.svc.LabelHolders(ctx, "pool-a")
	if err != nil || len(hs) != 1 || !hs[0].Registered || hs[0].Ephemeral {
		t.Fatalf("%+v %v", hs, err)
	}
	if err := r.svc.AddPoolLabel(ctx, rec.ID, "pool-b", "luis"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.AddPoolLabel(ctx, rec.ID, "pool-b", "luis"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if !hasLabel(got.Labels, "pool-a") || !hasLabel(got.Labels, "pool-b") || len(got.Labels) != 4 {
		t.Fatalf("both labels while the move runs, once each: %v", got.Labels)
	}
	if err := r.svc.RemovePoolLabel(ctx, rec.ID, "pool-a", "luis"); err != nil {
		t.Fatal(err)
	}
	got, _ = r.store.GetRunner(ctx, rec.ID)
	if hasLabel(got.Labels, "pool-a") || !hasLabel(got.Labels, "pool-b") {
		t.Fatalf("labels %v", got.Labels)
	}
	if len(lab.removed) != 1 || lab.removed[0] != "pool-a" {
		t.Fatalf("removed %v", lab.removed)
	}
}

func TestPoolLabelNeedsGitHubForARegisteredRunnerAndSkipsOthers(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	setGH(r, rec.ID, 99)
	if err := r.svc.AddPoolLabel(ctx, rec.ID, "pool-b", "luis"); err == nil {
		t.Fatal("no GitHub label API: a registered runner cannot be relabelled")
	}
	r.svc.Labeler = &fakeLabeler{err: errors.New("down")}
	if err := r.svc.AddPoolLabel(ctx, rec.ID, "pool-b", "luis"); err == nil {
		t.Fatal("a GitHub failure fails the step")
	}
	if got, _ := r.store.GetRunner(ctx, rec.ID); hasLabel(got.Labels, "pool-b") {
		t.Fatal("the record does not claim a label GitHub refused")
	}
	// A record with no registration (a pool runner) is changed in the record only.
	setGH(r, rec.ID, 0)
	if err := r.svc.AddPoolLabel(ctx, rec.ID, "pool-b", "luis"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.AddPoolLabel(ctx, "gone", "pool-b", "luis"); err != nil {
		t.Fatalf("a runner that is gone is nothing to relabel: %v", err)
	}
}

func TestPoolNamesServeTheOldNameWhileARenameRuns(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.svc.PoolNames = func() []string { return []string{"pool-b", "pool-a"} }
	if got := r.svc.poolName(); got != "pool-b" {
		t.Fatal(got)
	}
	if got := r.svc.poolFor([]string{"self-hosted", "pool-a"}); got != "pool-a" {
		t.Fatalf("a job with the old label keeps it: %s", got)
	}
	if got := r.svc.poolFor([]string{"self-hosted"}); got != "pool-b" {
		t.Fatal(got)
	}
}

func setGH(r *rig, id string, gh int64) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	rec := r.store.runners[id]
	rec.GitHubRunnerID = gh
	r.store.runners[id] = rec
}
