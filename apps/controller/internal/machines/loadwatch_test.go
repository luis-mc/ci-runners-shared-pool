package machines

import (
	"testing"
	"time"
)

// R5.77, R5.78: the warning rises at once with a job running, clears only after
// LoadClearAfter steady under budget, and a new spike restarts that period.
func TestLoadWatchRaisesAndClearsWithoutFlapping(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	steps := []struct {
		name    string
		sec     int
		over    bool
		running int
		want    bool
	}{
		{"over with no job running: no warning", 0, true, 0, false},
		{"over with a job running: warning", 5, true, 1, true},
		{"back under: still up", 20, false, 1, true},
		{"spike again: still up, period restarts", 40, true, 1, true},
		{"under again", 50, false, 1, true},
		{"under 59 s: still up", 109, false, 1, true},
		{"under 60 s: cleared", 110, false, 1, false},
		{"over but no job: stays clear", 120, true, 0, false},
	}
	var w watchState
	for _, s := range steps {
		if got := w.observe(s.over, s.running, at(s.sec)); got != s.want {
			t.Fatalf("%s: warning = %v, want %v", s.name, got, s.want)
		}
	}
}

func TestOverBudgetIsStrictAndIgnoresUnknown(t *testing.T) {
	set := Settings{BudgetMemoryPct: 85, BudgetCPUPct: 60}
	cases := []struct {
		l    Load
		want bool
	}{
		{Load{MemoryPct: 96, MemoryKnown: true}, true},
		{Load{MemoryPct: 85, MemoryKnown: true, CPUPct: 60, CPUKnown: true}, false},
		{Load{CPUPct: 61, CPUKnown: true}, true},
		{Load{MemoryPct: 99}, false},
	}
	for i, c := range cases {
		if got := OverBudget(c.l, set); got != c.want {
			t.Fatalf("case %d: %v", i, got)
		}
	}
}
