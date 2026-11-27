package waits

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

func TestHoldOverCapWithReason(t *testing.T) {
	jobs := []insights.JobFact{
		crspJob(1, "k", 0, 1, 6, 1), crspJob(2, "k", 0, 1, 6, 1), // over the cap, 12 min
		crspJob(3, "k", 0, 1, 1, 1), // under the cap
	}
	a := &Analyser{Jobs: fakeSource{jobs}, Explain: fakeExplain{r: HoldImagePull}}
	got, err := a.Analyse(context.Background(), weekWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != insights.KindWaitHold || got[0].Subject != "k" {
		t.Fatalf("findings = %+v", got)
	}
	if got[0].MinutesSavedWeek != 8 { // (6-2)*2
		t.Errorf("minutes = %d", got[0].MinutesSavedWeek)
	}
	var ev struct {
		Over    int            `json:"holds_over_cap"`
		Reasons map[string]int `json:"reasons"`
	}
	if err := json.Unmarshal(got[0].Evidence, &ev); err != nil || ev.Over != 2 || ev.Reasons["image_pull"] != 2 {
		t.Fatalf("evidence = %s %v", got[0].Evidence, err)
	}
	rows, _ := Aggregate(context.Background(), jobs, ByJobKey, DefaultHoldCap, fakeExplain{r: HoldLoad})
	if rows[0].HoldsOverCap != 2 || rows[0].HoldReasons[HoldLoad] != 2 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestHoldUnderWeeklyTotalNoFinding(t *testing.T) {
	a := &Analyser{Jobs: fakeSource{[]insights.JobFact{crspJob(1, "k", 0, 1, 6, 1)}}}
	got, err := a.Analyse(context.Background(), weekWindow)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestHoldExplainerErrorIsUnknownAndCapSetting(t *testing.T) {
	jobs := []insights.JobFact{crspJob(1, "k", 0, 1, 6, 1), crspJob(2, "k", 0, 1, 6, 1)}
	rows, err := Aggregate(context.Background(), jobs, ByJobKey, DefaultHoldCap, fakeExplain{err: errors.New("no log")})
	if err != nil || rows[0].HoldReasons[HoldUnknown] != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	a := &Analyser{Jobs: fakeSource{jobs}, HoldCap: func(context.Context) time.Duration { return 7 * time.Minute }}
	if got, _ := a.Analyse(context.Background(), weekWindow); len(got) != 0 {
		t.Fatalf("cap 7 min should clear the holds: %v", got)
	}
	a.HoldCap = func(context.Context) time.Duration { return time.Second } // out of range: default
	if got, _ := a.Analyse(context.Background(), weekWindow); len(got) != 1 {
		t.Fatalf("default cap: %v", got)
	}
}
