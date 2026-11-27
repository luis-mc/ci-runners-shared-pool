package timeline

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

var weekWin = insights.Window{From: t0.AddDate(0, 0, -7), To: t0}

func waitJob(id int64, startMin, waitMin float64, labels ...string) insights.JobFact {
	return insights.JobFact{ID: id, Labels: labels, CreatedAt: at(startMin), StartedAt: at(startMin + waitMin), CompletedAt: at(startMin + waitMin + 1)}
}

func TestIdleNextToQueueFinding(t *testing.T) {
	linux := []string{"self-hosted", "linux"}
	jobs := []insights.JobFact{
		waitJob(1, -100, 10, "self-hosted", "windows"),
		waitJob(2, -90, 10, "self-hosted", "windows"),
	}
	states := fakeStates{[]RunnerIdle{
		{RunnerName: "l1", MachineName: "m1", Labels: linux, Start: at(-105), End: at(-80)},
		{RunnerName: "l2", MachineName: "m1", Labels: linux, Start: at(-105), End: at(-80)}, // same span: merged, not doubled
	}}
	a := &IdleAnalyser{Jobs: fakeSource{jobs}, States: states}
	got, err := a.Analyse(context.Background(), weekWin)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != insights.KindIdleNextToQueue {
		t.Fatalf("findings = %+v", got)
	}
	if got[0].Subject != "linux,self-hosted > self-hosted,windows" || got[0].MinutesSavedWeek != 20 || got[0].Action != insights.ActionMachineSlots {
		t.Errorf("finding = %+v", got[0])
	}
}

func TestIdleBelowThresholdsNoFinding(t *testing.T) {
	linux := []string{"linux"}
	jobs := []insights.JobFact{waitJob(1, -100, 10, "windows"), waitJob(2, -50, 30, "linux")}
	// idle under 2 min is ignored; a waiting job the idle runner could take is not "next to" it;
	// 10 min of waiting is under the 15 min a week.
	cases := []struct {
		name string
		idle []RunnerIdle
	}{
		{"short idle", []RunnerIdle{{Labels: linux, Start: at(-100), End: at(-99)}}},
		{"takeable", []RunnerIdle{{Labels: linux, Start: at(-45), End: at(-30)}}},
		{"under 15 min", []RunnerIdle{{Labels: linux, Start: at(-100), End: at(-90)}}},
	}
	for _, tc := range cases {
		a := &IdleAnalyser{Jobs: fakeSource{jobs}, States: fakeStates{tc.idle}}
		got, err := a.Analyse(context.Background(), weekWin)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %v %v", tc.name, got, err)
		}
	}
}

func TestIdleNoMachineNoAction(t *testing.T) {
	jobs := []insights.JobFact{waitJob(1, -100, 20, "windows")}
	states := fakeStates{[]RunnerIdle{{Labels: []string{"linux"}, Start: at(-100), End: at(-70)}}}
	got, _ := (&IdleAnalyser{Jobs: fakeSource{jobs}, States: states}).Analyse(context.Background(), weekWin)
	if len(got) != 1 || got[0].Action != insights.ActionNone {
		t.Fatalf("%+v", got)
	}
	if time.Duration(got[0].MinutesSavedWeek)*time.Minute != 20*time.Minute {
		t.Errorf("minutes = %d", got[0].MinutesSavedWeek)
	}
}
