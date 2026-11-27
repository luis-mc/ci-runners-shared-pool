package waits

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

func TestShortJobsWaitingLong(t *testing.T) {
	var jobs []insights.JobFact
	for i := int64(0); i < 3; i++ {
		jobs = append(jobs, crspJob(i, "prune", 0, 4, 0.1, 0.5))
		jobs = append(jobs, crspJob(10+i, "build", 0, 4, 0.1, 9))    // long run: no finding
		jobs = append(jobs, crspJob(20+i, "fast", 0, 0.5, 0.1, 0.5)) // short wait: no finding
	}
	a := &Analyser{Jobs: fakeSource{jobs}, Room: fakeRoom(true)}
	got, err := a.Analyse(context.Background(), weekWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != insights.KindHousekeepingLane || got[0].Subject != "prune" {
		t.Fatalf("findings = %+v", got)
	}
	if got[0].Action != insights.ActionLightLane || got[0].MinutesSavedWeek != 12 {
		t.Errorf("action %q minutes %d", got[0].Action, got[0].MinutesSavedWeek)
	}
	a.Room = fakeRoom(false)
	got, _ = a.Analyse(context.Background(), weekWindow)
	if len(got) != 1 || got[0].Action != insights.ActionNone {
		t.Fatalf("no room: %+v", got)
	}
}

func TestHousekeepingNeedsEnoughJobs(t *testing.T) {
	a := &Analyser{Jobs: fakeSource{[]insights.JobFact{crspJob(1, "prune", 0, 4, 0.1, 0.5)}}}
	if got, _ := a.Analyse(context.Background(), weekWindow); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
