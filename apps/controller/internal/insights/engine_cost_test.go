package insights

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// D18: when a pass is due, by the period, a finished CI run or the timer.
func TestPeriodDue(t *testing.T) {
	cases := []struct {
		name      string
		p         Period
		finished  int
		since     time.Duration
		fromTimer bool
		want      bool
	}{
		{"every run, a run finished", Period{Mode: ModeEveryRun}, 1, time.Second, false, true},
		{"every run, timer too early", Period{Mode: ModeEveryRun}, 0, 5 * time.Minute, true, false},
		{"every run, timer after 15 min", Period{Mode: ModeEveryRun}, 0, Interval, true, true},
		{"unknown mode is every run", Period{Mode: "x"}, 1, 0, false, true},
		{"every 3rd run, second run", Period{Mode: ModeEveryNthRun, N: 3}, 2, time.Minute, false, false},
		{"every 3rd run, third run", Period{Mode: ModeEveryNthRun, N: 3}, 3, time.Minute, false, true},
		{"every Nth, timer under an hour", Period{Mode: ModeEveryNthRun, N: 3}, 1, 30 * time.Minute, true, false},
		{"every Nth, timer after an hour", Period{Mode: ModeEveryNthRun, N: 3}, 1, time.Hour, true, true},
		{"N below 2 becomes 2", Period{Mode: ModeEveryNthRun, N: 0}, 1, 0, false, false},
		{"hourly, a run finished", Period{Mode: ModeHourly}, 99, 2 * time.Hour, false, false},
		{"hourly, timer under an hour", Period{Mode: ModeHourly}, 0, 45 * time.Minute, true, false},
		{"hourly, timer after an hour", Period{Mode: ModeHourly}, 0, time.Hour, true, true},
	}
	for _, c := range cases {
		if got := c.p.Due(c.finished, c.since, c.fromTimer, false); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
	if !(Period{Mode: ModeHourly}).Due(0, 0, false, true) {
		t.Error("the first pass always runs")
	}
}

// startEngine runs the engine on a fake clock whose timer never fires; passes reports each pass.
func startEngine(t *testing.T, p Period) (Triggerer, <-chan struct{}, func()) {
	t.Helper()
	passes := make(chan struct{}, 100)
	a := &signalAnalyser{passes: passes}
	c := &fakeClock{now: t0, after: make(chan time.Time)}
	eng := NewEngine([]Analyser{a}, newMemStore(), nil, quiet(), WithClock(c), WithPeriod(func() Period { return p }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = eng.Run(ctx); close(done) }()
	select {
	case <-passes:
	case <-time.After(5 * time.Second):
		t.Fatal("no first pass")
	}
	return eng.(Triggerer), passes, func() { cancel(); <-done }
}

type signalAnalyser struct{ passes chan struct{} }

func (signalAnalyser) Name() string  { return "signal" }
func (signalAnalyser) Kinds() []Kind { return []Kind{KindWaitHold} }
func (a signalAnalyser) Analyse(context.Context, Window) ([]Finding, error) {
	a.passes <- struct{}{}
	return nil, nil
}

func expectPass(t *testing.T, passes <-chan struct{}, want bool, what string) {
	t.Helper()
	select {
	case <-passes:
		if !want {
			t.Fatalf("%s: a pass ran", what)
		}
	case <-time.After(100 * time.Millisecond):
		if want {
			t.Fatalf("%s: no pass", what)
		}
	}
}

// D18: insights are re-analysed after each CI run, not only on a timer.
func TestFinishedRunStartsAPass(t *testing.T) {
	trig, passes, stop := startEngine(t, Period{Mode: ModeEveryRun})
	defer stop()
	trig.Trigger()
	expectPass(t, passes, true, "after a finished run")
	trig.Trigger()
	expectPass(t, passes, true, "after the next finished run")
}

// D19: the slower period "after every Nth run" waits for N finished runs.
func TestEveryNthRunWaitsForN(t *testing.T) {
	trig, passes, stop := startEngine(t, Period{Mode: ModeEveryNthRun, N: 3})
	defer stop()
	trig.Trigger()
	trig.Trigger()
	expectPass(t, passes, false, "two runs of three")
	trig.Trigger()
	expectPass(t, passes, true, "the third run")
}

// D19: the hourly period ignores finished runs.
func TestHourlyIgnoresFinishedRuns(t *testing.T) {
	trig, passes, stop := startEngine(t, Period{Mode: ModeHourly})
	defer stop()
	for i := 0; i < 5; i++ {
		trig.Trigger()
	}
	expectPass(t, passes, false, "hourly mode and five finished runs")
}

type fakeMeter struct{ u atomic.Pointer[Usage] }

func (m *fakeMeter) Begin() func() Usage { return func() Usage { return *m.u.Load() } }

// D18, D19: over 5% CPU, 300 MB RAM or 1 GB disk, a warning finding recommends a slower period; it resolves
// when the next pass is back under.
func TestCostWarningOpensAndResolves(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		word string
		over bool
	}{
		{"under everything", Usage{CPUPercent: 4.9, PeakRAMBytes: 299e6, StoreBytes: 999e6}, "", false},
		{"cpu", Usage{CPUPercent: 5.1}, "CPU", true},
		{"ram", Usage{PeakRAMBytes: 301e6}, "memory", true},
		{"disk", Usage{StoreBytes: 1001e6}, "disk", true},
	}
	for _, c := range cases {
		st := newMemStore()
		m := &fakeMeter{}
		m.u.Store(&c.u)
		eng := NewEngine(nil, st, nil, quiet(), WithMeter(m))
		if err := eng.RunOnce(context.Background(), t0); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		f, open := st.open[string(KindCost)+"|analysis"]
		if open != c.over {
			t.Fatalf("%s: finding open = %v, want %v", c.name, open, c.over)
		}
		if !c.over {
			continue
		}
		if f.Scope != ScopePool || f.MinutesSavedWeek != 0 || !strings.Contains(f.Title, c.word) ||
			!strings.Contains(f.Fix, "5th CI run") || !strings.Contains(f.Fix, "once an hour") {
			t.Errorf("%s: finding = %+v", c.name, f)
		}
		under := Usage{}
		m.u.Store(&under)
		_ = eng.RunOnce(context.Background(), t0.Add(time.Hour))
		if _, still := st.open[string(KindCost)+"|analysis"]; still {
			t.Errorf("%s: back under the limits, the warning must resolve", c.name)
		}
	}
}

// D19: the advice follows the period already chosen.
func TestCostAdviceFollowsThePeriod(t *testing.T) {
	over := Usage{CPUPercent: 9}
	nth, _ := CostFinding(over, Period{Mode: ModeEveryNthRun, N: 5})
	if strings.Contains(nth.Fix, "5th") || !strings.Contains(nth.Fix, "once an hour") {
		t.Errorf("every Nth: %q", nth.Fix)
	}
	hourly, _ := CostFinding(over, Period{Mode: ModeHourly})
	if strings.Contains(hourly.Fix, "Choose a slower") {
		t.Errorf("hourly has nothing slower to offer: %q", hourly.Fix)
	}
}

// D18: the process meter reports a size on disk and a peak memory, and the 1 GB limit is checked on it.
func TestProcessMeterMeasuresAPass(t *testing.T) {
	dir := t.TempDir()
	m := ProcessMeter{StoreSize: func() int64 { return DirSize(dir) }}
	end := m.Begin()
	u := end()
	if u.PeakRAMBytes <= 0 || u.CPUPercent < 0 || u.StoreBytes != 0 {
		t.Errorf("usage = %+v", u)
	}
	if DirSize(dir+"/missing") != 0 {
		t.Error("a missing folder is 0 bytes")
	}
}
