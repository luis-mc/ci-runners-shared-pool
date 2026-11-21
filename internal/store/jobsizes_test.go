package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// scheduling R5.79 to R5.87: the scan rows, the overrides and the measured
// sizes round-trip, and the scheduler's settings carry them in precedence order.
func TestIntegrationJobSizesFeedSettings(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	js := s.JobSizes()
	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	rows := []jobsize.ScanRow{
		{Repository: "o/r", Job: "e2e", Weight: jobsize.Weight{CPU: 2, Memory: 2}, CPU: 2, GiB: 2, Class: jobsize.Normal, Confidence: jobsize.ConfHigh,
			Tests: 40, Reasons: []string{"playwright: 40 tests"}, Services: []string{"kafka"}, Commit: strings.Repeat("a", 40), ScannedAt: at, ResetNote: "x"},
		{Repository: "o/r", Job: "unit[1]", Weight: jobsize.Weight{CPU: 1, Memory: 1}, CPU: 1, GiB: 0.5, Class: jobsize.Light, Confidence: jobsize.ConfLow,
			Commit: strings.Repeat("a", 40), ScannedAt: at},
	}
	file := []jobsize.Rule{{Repository: "o/r", Pattern: "integration-*", Class: jobsize.Heavy, Origin: jobsize.OriginFile}}
	if err := js.ReplaceScan(ctx, "o/r", rows, file, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := js.SetConsoleRule(ctx, "o/r", "e2e", jobsize.Heavy); err != nil {
		t.Fatal(err)
	}
	got, err := js.ScanRows(ctx, "o/r")
	if err != nil || len(got) != 2 || got[0].Job != "e2e" || got[0].Services[0] != "kafka" || got[0].ResetNote != "x" || !got[0].ScannedAt.Equal(at) {
		t.Fatalf("rows = %+v err = %v", got, err)
	}
	set, err := settingsOf(t, s, "m1")
	if err != nil {
		t.Fatal(err)
	}
	// Console rule first, then the file rule, then one entry per job.
	if len(set.Sizes) != 4 || set.Sizes[0].Pattern != "e2e" || set.Sizes[0].Source != "override" || set.Sizes[1].Pattern != "integration-*" {
		t.Fatalf("sizes = %+v", set.Sizes)
	}
	if w, class := set.WeightOf(slots.JobNames{Repository: "o/r", Job: "e2e"}); w.CPU != 2 || class != "heavy" {
		t.Fatalf("e2e = %v %s: the console override must win", w, class)
	}
	if w, _ := set.WeightOf(slots.JobNames{Repository: "o/r", Job: "unit[1]"}); w.CPU != 1 {
		t.Fatalf("a job key with glob characters must match only itself: %v", w)
	}
	if w, class := set.WeightOf(slots.JobNames{Repository: "o/r", Job: "unit1"}); class != "default" || w != slots.DefaultWeight {
		t.Fatalf("unit1 = %v %s, want the default", w, class)
	}
	// A measured size after 5 runs beats the scan; the console override still wins.
	if err := js.SetMeasured(ctx, jobsize.Measured{Repository: "o/r", Job: "unit[1]", Weight: jobsize.Weight{CPU: 1, Memory: 2}, Runs: 6}); err != nil {
		t.Fatal(err)
	}
	set, _ = settingsOf(t, s, "m1")
	if w, _ := set.WeightOf(slots.JobNames{Repository: "o/r", Job: "unit[1]"}); w.Memory != 2 {
		t.Fatalf("measured = %v", w)
	}
	// A rescan that resets the job drops its measured size.
	if err := js.ReplaceScan(ctx, "o/r", rows[1:], nil, "bad file", []string{"unit[1]"}); err != nil {
		t.Fatal(err)
	}
	ms, _ := js.Measured(ctx, "o/r")
	ferr, _ := js.FileError(ctx, "o/r")
	rules, _ := js.Rules(ctx, "o/r")
	if len(ms) != 0 || ferr != "bad file" || len(rules) != 1 || rules[0].Origin != jobsize.OriginConsole {
		t.Fatalf("after rescan: measured=%v ferr=%q rules=%v", ms, ferr, rules)
	}
	if err := js.SetConsoleRule(ctx, "o/r", "e2e", ""); err != nil {
		t.Fatal(err)
	}
	if rules, _ := js.Rules(ctx, "o/r"); len(rules) != 0 {
		t.Fatalf("rules = %v", rules)
	}
}

func settingsOf(t *testing.T, s *Store, machine string) (slots.Settings, error) {
	t.Helper()
	var out slots.Settings
	err := s.Atomically(context.Background(), func(tx slots.Tx) error {
		var err error
		out, err = tx.Settings(machine)
		return err
	})
	return out, err
}
