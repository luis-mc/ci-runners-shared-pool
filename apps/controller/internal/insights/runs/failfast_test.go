package runs

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

func failedRun(branch string) []insights.JobFact {
	lint := runJob(1, 1, "ci.yml", branch, "a", "push", 0, 0, 2, "failure")
	long := runJob(2, 1, "ci.yml", branch, "a", "push", 0, 0, 20, "cancelled") // ran 18 min past the failure
	late := runJob(3, 1, "ci.yml", branch, "a", "push", 0, 5, 9, "success")    // started after: 4 min
	done := runJob(4, 1, "ci.yml", branch, "a", "push", 0, 0, 1, "success")    // ended before the failure
	return []insights.JobFact{lint, long, late, done}
}

func TestMinutesAfterFirstFailure(t *testing.T) {
	if got := FailFastMinutes(failedRun("feat")); got != 22 {
		t.Errorf("got %v, want 22 (18 + 4)", got)
	}
	ok := []insights.JobFact{runJob(1, 1, "ci.yml", "feat", "a", "push", 0, 0, 5, "success")}
	if got := FailFastMinutes(ok); got != 0 {
		t.Errorf("green run = %v", got)
	}
}

func TestFailFastNeverAdvisedForRelease(t *testing.T) {
	w := insights.Window{From: t0, To: t0.AddDate(0, 0, 7)}
	fs, err := Analyser{Jobs: fakeJobs(failedRun("RELEASE-48"))}.Analyse(context.Background(), w)
	if err != nil || len(fs) != 0 {
		t.Fatalf("release run: %v %+v", err, fs)
	}
	fs, _ = Analyser{Jobs: fakeJobs(failedRun("feat"))}.Analyse(context.Background(), w)
	if len(fs) != 1 || fs[0].Kind != insights.KindRunFailFast || fs[0].MinutesSavedWeek != 22 {
		t.Fatalf("other run: %+v", fs)
	}
	if _, ok := failFastWaste(runInfo{First: failedRun("release/x")[0], Jobs: failedRun("release/x")}, defaultRelease); ok {
		t.Error("release waste reported")
	}
}
