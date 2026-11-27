package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
)

type crspNames map[string]bool

func (c crspNames) IsCrsp(_ context.Context, n string) (bool, error) { return c[n], nil }

type fakeSink struct {
	origins map[int64]string
	groups  map[int64]string
	labels  map[int64][]string
	source  map[int64]string
}

func (f *fakeSink) SetRunnerOrigin(_ context.Context, id int64, o reconcile.Origin, g string, l []string) error {
	f.origins[id], f.groups[id], f.labels[id] = string(o), g, l
	return nil
}
func (f *fakeSink) SetStepsSource(_ context.Context, id int64, s string) error {
	f.source[id] = s
	return nil
}

func originRecorder(st *fakeStore, sink *fakeSink) *Recorder {
	return New(Config{Writer: st, Reader: st, Origins: sink, Classify: reconcile.NewClassifier(crspNames{"crsp-1": true}),
		Now: func() time.Time { return t0 },
		Repos: func(_ context.Context, name string) (int64, bool) {
			return 7, name == "luis-mc/app"
		}})
}

func TestOnRunnerRecordsOrigin(t *testing.T) { // R5.39
	st := newStore()
	sink := &fakeSink{origins: map[int64]string{}, groups: map[int64]string{}, labels: map[int64][]string{}, source: map[int64]string{}}
	r := originRecorder(st, sink)
	ctx := context.Background()
	for _, id := range []int64{1, 2, 3, 4} {
		if err := r.OnJob(ctx, ghJob(id, "in_progress")); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.OnRunner(ctx, 1, "crsp-1", "Default", []string{"self-hosted", "crsp"}))
	must(r.OnRunner(ctx, 2, "old-pool-3", "Default", []string{"self-hosted"}))
	must(r.OnRunner(ctx, 3, "GitHub Actions 9", "GitHub Actions", []string{"ubuntu-latest"}))
	must(r.OnRunner(ctx, 4, "", "", nil)) // queued: nothing to say yet
	must(r.OnRunner(ctx, 99, "crsp-1", "Default", nil))
	want := map[int64]string{1: "crsp", 2: "other", 3: "hosted"}
	for id, w := range want {
		if sink.origins[id] != w {
			t.Errorf("job %d origin %q, want %q", id, sink.origins[id], w)
		}
	}
	if _, ok := sink.origins[4]; ok {
		t.Error("a job with no runner got an origin")
	}
	if _, ok := sink.origins[99]; ok {
		t.Error("an unknown job got an origin")
	}
	if sink.groups[2] != "Default" || len(sink.labels[1]) != 2 {
		t.Errorf("group %q labels %v", sink.groups[2], sink.labels[1])
	}
}

func TestOnStepsMarksSourceEvents(t *testing.T) { // R5.40
	st := newStore()
	sink := &fakeSink{origins: map[int64]string{}, groups: map[int64]string{}, labels: map[int64][]string{}, source: map[int64]string{}}
	r := originRecorder(st, sink)
	ctx := context.Background()
	if err := r.OnJob(ctx, ghJob(1, "in_progress")); err != nil {
		t.Fatal(err)
	}
	if err := r.OnSteps(ctx, 1, []github.JobStep{{Number: 1, Name: "build", Status: "in_progress"}}); err != nil {
		t.Fatal(err)
	}
	if sink.source[1] != "events" {
		t.Fatalf("steps source %q, want events", sink.source[1])
	}
}
