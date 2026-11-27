package runs

import (
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func at(min float64) time.Time { return t0.Add(time.Duration(min * float64(time.Minute))) }

func job(id int64, node string, created, queued, start, end float64) insights.JobFact {
	return insights.JobFact{ID: id, RunID: 1, RunAttempt: 1, NodeID: node, JobName: node, JobIdent: "r/w/" + node,
		Result: "success", CreatedAt: at(created), QueuedAt: at(queued), StartedAt: at(start), CompletedAt: at(end)}
}

func TestTopThreeSavings(t *testing.T) {
	// lint 0-2; build (needs lint) queued at 2, waited 6 min for a slot, ran 8-18 (median 4);
	// test (needs build) started 20 (2 min after build ended), ran 20-25. A side job ends early.
	g := Graph{
		Jobs: []insights.JobFact{
			job(1, "lint", 0, 0, 0, 2),
			job(2, "build", 0, 2, 8, 18),
			job(3, "test", 0, 18, 20, 25),
			job(4, "side", 0, 0, 0, 3),
		},
		Needs:   map[string][]string{"build": {"lint"}, "test": {"build"}},
		Medians: map[string]time.Duration{"r/w/build": 4 * time.Minute},
	}
	got := NewSavings().Top(g)
	if len(got) != 3 {
		t.Fatalf("want 3 savings, got %d: %+v", len(got), got)
	}
	want := []struct {
		job    string
		change Change
		sooner time.Duration
	}{
		{"build", StartAfterNeeds, 6 * time.Minute},
		{"build", NoWait, 6 * time.Minute},
		{"build", MedianRun, 6 * time.Minute},
	}
	for i, w := range want {
		if got[i].Job != w.job || got[i].Sooner != w.sooner {
			t.Errorf("saving %d = %+v, want %v", i, got[i], w)
		}
	}
	// ties break by job, then change name: median_run, no_wait, start_after_needs.
	if got[0].Change != MedianRun || got[1].Change != NoWait || got[2].Change != StartAfterNeeds {
		t.Errorf("tie order = %v %v %v", got[0].Change, got[1].Change, got[2].Change)
	}
	if !strings.HasPrefix(got[1].Words, "The run would end 6 min sooner if build had not waited") {
		t.Errorf("words = %q", got[1].Words)
	}
}

func TestSavingsStayOnCriticalPath(t *testing.T) {
	// the long job decides the end; shaving the short one saves nothing.
	g := Graph{Jobs: []insights.JobFact{job(1, "short", 0, 0, 5, 8), job(2, "long", 0, 0, 0, 30)}}
	if got := NewSavings().Top(g); len(got) != 0 {
		t.Errorf("want no savings, got %+v", got)
	}
}

func TestSavingsUnknownTimes(t *testing.T) {
	j := insights.JobFact{ID: 1, NodeID: "a"}
	if got := NewSavings().Top(Graph{Jobs: []insights.JobFact{j}}); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
}

func TestSavingsCycleDoesNotHang(t *testing.T) {
	g := Graph{Jobs: []insights.JobFact{job(1, "a", 0, 0, 1, 5), job(2, "b", 0, 0, 6, 9)},
		Needs: map[string][]string{"a": {"b"}, "b": {"a"}}}
	_ = NewSavings().Top(g)
}
