package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/actions"
)

func TestInsightPlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ps := s.InsightPlans()
	if _, err := ps.Get(ctx, "nope"); !errors.Is(err, actions.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	p := actions.Plan{ID: "plan_1", FindingID: 3, Action: "machine_slots", State: actions.Draft, CreatedBy: "alice", CreatedAt: at,
		Changes: []actions.Change{{Target: "m1", TargetName: "M1", Field: "job_slots", From: 4, To: 2, Words: "w", JobsWaitedFor: []string{"j"}}}}
	if err := ps.Insert(ctx, p); err != nil {
		t.Fatal(err)
	}
	g, err := ps.Get(ctx, "plan_1")
	if err != nil || g.State != actions.Draft || g.Results[0].State != "pending" || g.Changes[0].JobsWaitedFor[0] != "j" || !g.CreatedAt.Equal(at) {
		t.Fatalf("draft: %+v %v", g, err)
	}
	g.State, g.StartedAt, g.FinishedAt = actions.Done, at, at.Add(time.Minute)
	g.Results = []actions.ChangeResult{{Target: "m1", Field: "job_slots", State: "done"}}
	if err := ps.Update(ctx, g); err != nil {
		t.Fatal(err)
	}
	h, _ := ps.Get(ctx, "plan_1")
	if h.State != actions.Done || h.Results[0].State != "done" || !h.FinishedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("done: %+v", h)
	}
}
