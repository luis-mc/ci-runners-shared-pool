package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/routing"
)

// github-integration R5.106: the routing state of a repository round-trips and
// a save replaces it whole.
func TestIntegrationRoutingStateRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	rs := s.Routing()
	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	st := routing.State{
		Jobs:    []routing.JobState{{Key: "e2e", Var: "CRSP_E2E", Label: "crsp-e2e-7f3a", Class: "large", OS: "linux", Value: `["self-hosted","Linux","crsp-e2e-7f3a"]`}},
		Labels:  []routing.LabelState{{Label: "crsp-e2e-7f3a", State: routing.LabelActive, Since: at, Jobs: []string{"e2e"}}, {Label: "crsp-old-0001", State: routing.LabelRetiring, Since: at}},
		Runners: []routing.RunnerLabel{{Repository: "o/r", RunnerID: "r1", GitHubID: 11, Label: "crsp-old-0001", ScopeOwner: "o", ScopeRepo: "r"}},
		PR:      &routing.PRState{Number: 7, URL: "https://example.test/7", OpenedAt: at},
	}
	if err := rs.Save(ctx, "o/r", st); err != nil {
		t.Fatal(err)
	}
	if err := rs.Save(ctx, "o/other", routing.State{Jobs: []routing.JobState{{Key: "x", Var: "CRSP_X", Label: "crsp-x-0002", Class: "small", OS: "linux", Value: "[]"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := rs.Load(ctx, "o/r")
	if err != nil || len(got.Jobs) != 1 || got.Jobs[0].Var != "CRSP_E2E" || len(got.Labels) != 2 || got.Runners[0].GitHubID != 11 || got.PR == nil || got.PR.Number != 7 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if l := got.Labels[1]; l.State != routing.LabelRetiring || !l.Since.Equal(at) || got.Labels[0].Jobs[0] != "e2e" {
		t.Fatalf("labels %+v", got.Labels)
	}
	if err := rs.Save(ctx, "o/r", routing.State{}); err != nil {
		t.Fatal(err)
	}
	if got, _ = rs.Load(ctx, "o/r"); len(got.Jobs)+len(got.Labels)+len(got.Runners) != 0 || got.PR != nil {
		t.Fatalf("not replaced: %+v", got)
	}
	if other, _ := rs.Load(ctx, "o/other"); len(other.Jobs) != 1 {
		t.Fatalf("another repository must be untouched: %+v", other)
	}
}
