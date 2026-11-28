package api

import (
	"io"
	"log/slog"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func nofitServer(recs ...runners.Record) *server {
	f := &fakeRunners{recs: map[string]runners.Record{}}
	for _, r := range recs {
		f.recs[r.ID] = r
	}
	return &server{opts: Options{Runners: f, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
}

func queuedWith(id int64, labels ...string) gen.Job {
	free := gen.WaitReasonFreeSlot
	return gen.Job{Id: id, State: gen.JobStateQueued, Labels: labels, WaitReason: &free}
}

// CRSP391: a job asking for a label no runner carries is "No runner fits", online or offline runner alike.
func TestMarkNoRunnerWhenNoRunnerCarriesTheLabel(t *testing.T) {
	s := nofitServer(runners.Record{ID: "r1", OS: "linux", Arch: "x64", Labels: []string{"pool-other"}, State: "offline"})
	q := []gen.Job{queuedWith(1, "self-hosted", "linux", "pool-main"), queuedWith(2, "self-hosted", "Linux", "POOL-OTHER")}
	w := overview.Waiting(q)
	o := gen.Overview{Queued: &q, Waiting: &w}
	s.markNoRunner(t.Context(), &o)
	if r := q[0].WaitReason; r == nil || *r != gen.WaitReasonNoRunnerWithLabels {
		t.Fatalf("job 1 reason %v", r)
	}
	if q[0].WaitText == nil || *q[0].WaitText != "pool-main" {
		t.Fatalf("job 1 names the missing label, got %v", q[0].WaitText)
	}
	if *q[1].WaitReason != gen.WaitReasonFreeSlot {
		t.Fatalf("job 2 has a runner with its labels (case does not count), reason %v", *q[1].WaitReason)
	}
	if w[0].Reason != gen.WaitReasonNoRunnerWithLabels || w[1].Reason != gen.WaitReasonFreeSlot {
		t.Fatalf("waiting list %+v", w)
	}
}

func TestMarkNoRunnerKeepsReservedAndLabelless(t *testing.T) {
	s := nofitServer()
	res := gen.WaitReasonReserved
	q := []gen.Job{{Id: 1, State: gen.JobStateQueued, Labels: []string{"x"}, WaitReason: &res}, queuedWith(2)}
	o := gen.Overview{Queued: &q}
	s.markNoRunner(t.Context(), &o)
	if *q[0].WaitReason != gen.WaitReasonReserved || *q[1].WaitReason != gen.WaitReasonFreeSlot {
		t.Fatal("a reserved job or a job with no labels was re-marked")
	}
}

func TestUnplaceableLabelsNeverCarriedTogether(t *testing.T) {
	rs := []runners.Record{{OS: "linux", Labels: []string{"a"}}, {OS: "linux", Labels: []string{"b"}}}
	if got := unplaceable([]string{"a", "b"}, rs); len(got) != 2 {
		t.Fatalf("each label has a runner but none has both: %v", got)
	}
	if got := unplaceable([]string{"a", "c"}, rs); len(got) != 1 || got[0] != "c" {
		t.Fatalf("got %v", got)
	}
}
