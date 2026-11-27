package jobsdata

import (
	"testing"
	"time"
)

var f0 = time.Date(2026, 11, 21, 6, 40, 0, 0, time.UTC)

func fat(s int) time.Time { return f0.Add(time.Duration(s) * time.Second) }

func TestInferParentPicksTheRunThatFinishedLastBeforeTheChild(t *testing.T) {
	cases := []struct {
		name   string
		child  RunCandidate
		others []RunCandidate
		want   int64
	}{
		{"parent finished seconds before", RunCandidate{RunID: 30, CreatedAt: fat(606)},
			[]RunCandidate{{RunID: 10, CreatedAt: fat(0), CompletedAt: fat(600)}, {RunID: 20, CreatedAt: fat(0), CompletedAt: fat(300)}}, 10},
		{"a run that finished after the child is not the parent", RunCandidate{RunID: 30, CreatedAt: fat(606)},
			[]RunCandidate{{RunID: 10, CompletedAt: fat(600)}, {RunID: 20, CompletedAt: fat(900)}}, 10},
		{"a higher run id is not the parent", RunCandidate{RunID: 30, CreatedAt: fat(606)}, []RunCandidate{{RunID: 40, CompletedAt: fat(600)}}, 0},
		{"child before the parent finished: the single open run", RunCandidate{RunID: 30, CreatedAt: fat(606)}, []RunCandidate{{RunID: 10}}, 10},
		{"child before the parent finished: two open runs are ambiguous", RunCandidate{RunID: 30, CreatedAt: fat(606)}, []RunCandidate{{RunID: 10}, {RunID: 20}}, 0},
		{"no other run", RunCandidate{RunID: 30, CreatedAt: fat(606)}, nil, 0},
	}
	for _, c := range cases {
		got, ok := InferParent(c.child, c.others)
		if got != c.want || ok != (c.want != 0) {
			t.Errorf("%s: got %d %v, want %d", c.name, got, ok, c.want)
		}
	}
}

func TestOverallStateStaysRunningWhileAFollowUpIs(t *testing.T) {
	open := []FollowUp{{State: Done}, {State: Queued}}
	cases := []struct {
		own  State
		fus  []FollowUp
		want Overall
	}{
		{Queued, nil, OverallQueued},
		{Running, open, OverallRunning},
		{Done, open, OverallFollowUpRunning},
		{Done, []FollowUp{{State: Running}}, OverallFollowUpRunning},
		{Done, []FollowUp{{State: Done}}, OverallDone},
		{Done, nil, OverallDone},
	}
	for i, c := range cases {
		if got := OverallOf(c.own, c.fus); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}
