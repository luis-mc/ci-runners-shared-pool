package timeline

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

var day = insights.Window{From: t0, To: t0.Add(2 * time.Hour)}

func TestLanesIdleAndAfterMerge(t *testing.T) {
	jobs := []insights.JobFact{
		// push on default branch holds crsp-1 from 10 to 40 while a PR job waits from 20 to 45
		{ID: 1, Repository: "o/r", JobIdent: "deploy", Event: "push", DefaultBranch: true, Origin: insights.OriginCrsp,
			RunnerName: "crsp-1", MachineID: "m1", CreatedAt: at(9), StartedAt: at(10), CompletedAt: at(40)},
		{ID: 2, Repository: "o/r", JobIdent: "test", Event: "pull_request", Origin: insights.OriginCrsp,
			Labels: []string{"self-hosted", "linux"}, RunnerName: "crsp-2", CreatedAt: at(20), StartedAt: at(45), CompletedAt: at(50)},
		{ID: 3, Repository: "o/x", JobIdent: "lint", Event: "push", Origin: insights.OriginOther,
			RunnerName: "alpha", CreatedAt: at(1), StartedAt: at(2), CompletedAt: at(3)},
	}
	states := fakeStates{[]RunnerIdle{
		{RunnerName: "crsp-3", MachineName: "m2", Labels: []string{"self-hosted", "Linux", "x64"}, Start: at(25), End: at(35)},
		{RunnerName: "crsp-3", MachineName: "m2", Labels: []string{"self-hosted", "Linux", "x64"}, Start: at(90), End: at(100)},
	}}
	lanes, err := NewBuilder(fakeSource{jobs}, states).Timeline(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(lanes) != 4 {
		t.Fatalf("lanes = %d", len(lanes))
	}
	// sorted by origin ("crsp" < "other") then name
	names := []string{lanes[0].RunnerName, lanes[1].RunnerName, lanes[2].RunnerName, lanes[3].RunnerName}
	if names[0] != "crsp-1" || names[1] != "crsp-2" || names[2] != "crsp-3" || names[3] != "alpha" {
		t.Fatalf("order = %v", names)
	}
	if lanes[0].MachineName != "m1" || lanes[3].Origin != insights.OriginOther {
		t.Errorf("lane meta = %+v %+v", lanes[0], lanes[3])
	}
	push := lanes[0].Segments[0]
	if len(push.Markers) != 1 || push.Markers[0] != AfterMergeWork || len(push.WaitingJobIDs) != 1 || push.WaitingJobIDs[0] != 2 {
		t.Errorf("push segment = %+v", push)
	}
	idle := lanes[2].Segments
	if len(idle) != 2 || idle[0].Kind != SegmentIdle || len(idle[0].Markers) != 1 || idle[0].Markers[0] != IdleWhileWaiting {
		t.Fatalf("idle = %+v", idle)
	}
	if len(idle[1].Markers) != 0 {
		t.Errorf("idle with nothing waiting is marked: %+v", idle[1])
	}
	if len(lanes[3].Segments) != 1 || lanes[3].Segments[0].Kind != SegmentJob {
		t.Errorf("other runner = %+v", lanes[3].Segments)
	}
}

func TestIdleNotMarkedWhenLabelsDiffer(t *testing.T) {
	jobs := []insights.JobFact{{ID: 5, Event: "pull_request", Labels: []string{"windows"}, CreatedAt: at(1), StartedAt: at(60), RunnerName: "w", Origin: insights.OriginCrsp, CompletedAt: at(61)}}
	states := fakeStates{[]RunnerIdle{{RunnerName: "l", Labels: []string{"linux"}, Start: at(10), End: at(20)}}}
	lanes, err := NewBuilder(fakeSource{jobs}, states).Timeline(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lanes {
		for _, s := range l.Segments {
			if len(s.Markers) != 0 {
				t.Fatalf("marked: %+v", s)
			}
		}
	}
}

func TestRangeOver24HoursRejected(t *testing.T) {
	b := NewBuilder(fakeSource{}, fakeStates{})
	if _, err := b.Timeline(context.Background(), insights.Window{From: t0, To: t0.Add(25 * time.Hour)}); err == nil {
		t.Fatal("25 h accepted")
	}
	if _, err := b.Timeline(context.Background(), insights.Window{From: t0, To: t0}); err == nil {
		t.Fatal("empty range accepted")
	}
}

// The Installers run starts after CI and waited about 6 minutes for a runner: the wait is a segment on the lane of
// the runner that took the job, marked as a follow-up, so the gap after CI shows on the timeline.
func TestFollowUpRunnerWaitIsASegment(t *testing.T) {
	jobs := []insights.JobFact{
		{ID: 1, RunID: 100, Repository: "o/r", JobIdent: "msi", Event: "workflow_run", Origin: insights.OriginCrsp,
			RunnerName: "crsp-1", CreatedAt: at(1), StartedAt: at(7.5), CompletedAt: at(20)},
		{ID: 2, RunID: 101, Repository: "o/r", JobIdent: "short", Event: "workflow_run", Origin: insights.OriginCrsp,
			RunnerName: "crsp-1", CreatedAt: at(30), StartedAt: at(30.5), CompletedAt: at(31)},
		{ID: 3, RunID: 102, Repository: "o/r", JobIdent: "lint", Event: "push", Origin: insights.OriginCrsp,
			RunnerName: "crsp-1", CreatedAt: at(40), StartedAt: at(50), CompletedAt: at(55)},
	}
	lanes, err := NewBuilder(fakeSource{jobs}, fakeStates{}).Timeline(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	var waits, followUps int
	for _, s := range lanes[0].Segments {
		switch {
		case s.Kind == SegmentWait && s.JobID == 1 && s.RunID == 100 && s.FollowUp && s.Start.Equal(at(1)) && s.End.Equal(at(7.5)):
			waits++
		case s.Kind == SegmentWait:
			t.Errorf("unexpected wait: %+v", s)
		case s.Kind == SegmentJob && s.FollowUp:
			followUps++
		}
	}
	if waits != 1 || followUps != 2 {
		t.Fatalf("waits %d follow-up jobs %d, segments %+v", waits, followUps, lanes[0].Segments)
	}
}
