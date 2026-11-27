package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/waits"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

type fakeEngine struct{ ran chan struct{} }

func (e fakeEngine) RunOnce(context.Context, time.Time) error { return nil }
func (e fakeEngine) Run(ctx context.Context) error {
	close(e.ran)
	<-ctx.Done()
	return ctx.Err()
}

// The wiring mounts the area on the API options and starts the engine; no deps starts nothing.
func TestStartInsights(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var opts api.Options
	startInsights(context.Background(), logger, &opts, insightsDeps{})
	if opts.Insights != nil || opts.JobRunners != nil {
		t.Fatalf("empty deps mounted something: %+v", opts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := fakeEngine{ran: make(chan struct{})}
	area := &api.InsightsArea{}
	startInsights(ctx, logger, &opts, insightsDeps{Area: area, Engine: eng})
	<-eng.ran
	if opts.Insights != area {
		t.Error("area not mounted")
	}
}

type countTrigger struct{ n int }

func (c *countTrigger) Trigger() { c.n++ }

// D18: a finished CI run reaches the engine through the bridge; before the engine exists it is dropped.
func TestBridgeForwardsFinishedRuns(t *testing.T) {
	var b triggerBridge
	b.Trigger() // no engine yet: nothing happens
	c := &countTrigger{}
	b.set(c)
	b.Trigger()
	b.Trigger()
	if c.n != 2 {
		t.Fatalf("triggers = %d", c.n)
	}
}

// D18, D19: Settings give the period; an unknown value is every run.
func TestPeriodOfSettings(t *testing.T) {
	got := periodOf(settings.Settings{InsightsUpdatePeriod: "every_nth_run", InsightsUpdateEveryNthRuns: 7})
	if got.Mode != insights.ModeEveryNthRun || got.N != 7 {
		t.Errorf("period = %+v", got)
	}
	if got := periodOf(settings.Settings{InsightsUpdatePeriod: "weekly"}); got.Mode != insights.ModeEveryRun {
		t.Errorf("unknown period = %+v", got)
	}
	failing := periodFrom(func(context.Context) (settings.Settings, error) { return settings.Settings{}, io.EOF })
	if failing().Mode != insights.ModeEveryRun {
		t.Error("an unreadable setting keeps every run")
	}
}

// D18: the tray links to the newest weekly digest file; none written means no link.
func TestLatestDigest(t *testing.T) {
	dir := t.TempDir()
	if got := latestDigest(dir); got != "" {
		t.Fatalf("no digest yet = %q", got)
	}
	d := filepath.Join(dir, "ci-data", "digests")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"2026-W46.md", "2026-W47.md", "note.txt"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := latestDigest(dir); filepath.Base(got) != "2026-W47.md" {
		t.Fatalf("latest = %q", got)
	}
}

type memFindings struct{ synced [][]insights.Kind }

func (m *memFindings) Sync(_ context.Context, kinds []insights.Kind, _ []insights.Finding, _ time.Time) (insights.SyncResult, error) {
	m.synced = append(m.synced, kinds)
	return insights.SyncResult{}, nil
}
func (m *memFindings) List(context.Context, insights.Filter) ([]insights.StoredFinding, int, string, error) {
	return nil, 0, "", nil
}
func (m *memFindings) Get(context.Context, int64) (insights.StoredFinding, error) {
	return insights.StoredFinding{}, nil
}
func (m *memFindings) Summary(context.Context) (insights.Summary, error) {
	return insights.Summary{}, nil
}

// D18, D19: the controller's engine measures each pass and syncs the cost warning next to the analysers.
func TestNewInsightsEngineMeasuresEachPass(t *testing.T) {
	fs := &memFindings{}
	get := func(context.Context) (settings.Settings, error) { return settings.Defaults, nil }
	eng := newInsightsEngine(nil, fs, nil, get, t.TempDir(), filepath.Join(t.TempDir(), "crsp.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := eng.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(fs.synced) != 1 || len(fs.synced[0]) != 1 || fs.synced[0][0] != insights.KindCost {
		t.Fatalf("synced = %v, want one sync of the cost kind", fs.synced)
	}
}

// LOOK66: the controller mounts the waits reader over the store, so getWaitSplit answers from the jobs it keeps.
func TestInsightsAreaReadsWaitsFromTheStore(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	area := newInsightsArea(db)
	if area.Waits == nil || area.Resources == nil {
		t.Fatalf("area = %+v", area)
	}
	rows, err := area.Waits.SplitBy(ctx, 7, waits.ByLabel, 0, 2*time.Minute)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty store: %v %v", rows, err)
	}
}
