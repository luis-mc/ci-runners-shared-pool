package waits

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

func TestCrspSplit(t *testing.T) {
	s := NewSplitter().Split(crspJob(1, "k", 1, 3, 2, 5))
	if s.Wait != 3*time.Minute || s.Hold != 2*time.Minute || s.Run != 5*time.Minute || s.GitHubWait != time.Minute {
		t.Fatalf("split = %+v", s)
	}
	if !s.WaitKnown || !s.HoldKnown || !s.RunKnown {
		t.Fatalf("known = %+v", s)
	}
}

func TestOtherRunnerNoHold(t *testing.T) {
	j := insights.JobFact{Origin: insights.OriginOther, CreatedAt: at(0), StartedAt: at(4), CompletedAt: at(6)}
	s := NewSplitter().Split(j)
	if s.Wait != 4*time.Minute || s.HoldKnown || s.Hold != 0 || s.Run != 2*time.Minute {
		t.Fatalf("split = %+v", s)
	}
}

func TestUnknownNotZero(t *testing.T) {
	j := crspJob(1, "k", 1, 3, 2, 5)
	j.StartedAt, j.CompletedAt = time.Time{}, time.Time{}
	s := NewSplitter().Split(j)
	if s.HoldKnown || s.RunKnown || !s.WaitKnown {
		t.Fatalf("known = %+v", s)
	}
	j = crspJob(1, "k", 1, 3, 2, 5)
	j.SlotAt = at(-1) // before created: never negative
	if s := NewSplitter().Split(j); s.WaitKnown || s.Wait != 0 {
		t.Fatalf("negative wait = %+v", s)
	}
}

func TestGroupByRepositoryKeyDay(t *testing.T) {
	a := crspJob(1, "k1", 0, 1, 1, 1)
	b := crspJob(2, "k1", 0, 3, 1, 1)
	c := crspJob(3, "k2", 0, 5, 1, 1)
	c.Repository = "o/other"
	c.CreatedAt = c.CreatedAt.AddDate(0, 0, 1)
	jobs := []insights.JobFact{a, b, c}
	cases := []struct {
		g    Group
		want []string
		jobs []int
	}{
		{ByRepository, []string{"o/other", "o/r"}, []int{1, 2}},
		{ByJobKey, []string{"k1", "k2"}, []int{2, 1}},
		{ByDay, []string{"2026-11-10", "2026-11-11"}, []int{2, 1}},
	}
	for _, tc := range cases {
		rows, err := Aggregate(context.Background(), jobs, tc.g, 0, nil)
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s: %v %v", tc.g, rows, err)
		}
		for i, r := range rows {
			if r.Key != tc.want[i] {
				t.Errorf("%s key %d = %q", tc.g, i, r.Key)
			}
		}
	}
	rows, _ := Aggregate(context.Background(), jobs, ByJobKey, 0, nil)
	if rows[0].Jobs != 2 || rows[0].Wait.P50 != time.Minute || rows[0].Wait.P95 != 3*time.Minute || rows[0].Wait.Total != 4*time.Minute {
		t.Fatalf("k1 = %+v", rows[0])
	}
}

func TestReaderWindowAndDays(t *testing.T) {
	r := NewReader(fakeSource{jobs: []insights.JobFact{crspJob(1, "k", 0, 1, 1, 1)}}, nil, func() time.Time { return t0 })
	rows, err := r.SplitBy(context.Background(), 7, ByJobKey, 0, 2*time.Minute)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	if _, err := r.SplitBy(context.Background(), 5, ByJobKey, 0, 0); err == nil {
		t.Fatal("days 5 accepted")
	}
}

// LOOK66: a group per runner label (the crsp- one first) and the slowest 10% (p90, nearest rank).
func TestGroupByLabelAndP90(t *testing.T) {
	var jobs []insights.JobFact
	for i := 1; i <= 10; i++ {
		j := crspJob(int64(i), "k", 0, float64(i), 1, 1)
		j.Labels = []string{"self-hosted", "crsp-build-c4a7"}
		jobs = append(jobs, j)
	}
	other := crspJob(11, "k", 0, 1, 1, 1)
	other.Labels = []string{"self-hosted", "linux"}
	none := crspJob(12, "k", 0, 1, 1, 1)
	jobs = append(jobs, other, none)
	rows, err := Aggregate(context.Background(), jobs, ByLabel, 0, nil)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %v %v", rows, err)
	}
	got := map[string]Row{}
	for _, r := range rows {
		got[r.Key] = r
	}
	b := got["crsp-build-c4a7"]
	if b.Jobs != 10 || b.Wait.P50 != 5*time.Minute || b.Wait.P90 != 9*time.Minute || b.Wait.P95 != 10*time.Minute {
		t.Fatalf("crsp-build = %+v", b)
	}
	if got["self-hosted, linux"].Jobs != 1 || got["no label"].Jobs != 1 {
		t.Fatalf("others = %+v", got)
	}
}
