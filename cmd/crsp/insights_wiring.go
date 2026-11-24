package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/digest"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/waits"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// insightsDeps is what the CI run insights routes and the engine run on (docs/features/ci-run-insights R5.42).
// Each analyser family builds its own service over the store; the join of the wave fills the fields. A nil
// Area field answers 501 on its routes; a nil Engine starts no pass.
type insightsDeps struct {
	Area *api.InsightsArea
	// Runners says who ran a job (R5.39 to R5.41); nil leaves those job fields absent.
	Runners api.JobRunnerReader
	// Engine runs the analysers: after each CI run and on a timer, as the update period says (R5.44, R6.3).
	Engine insights.Engine
	// Digest writes the weekly digest draft on Monday 08:00 Europe/Lisbon (R5.38); nil writes none.
	Digest digest.Writer
}

// newInsightsArea is the insights area over the store: the resources and peaks of a job, and the waits read from
// the job facts the controller keeps (LOOK66, ci-run-insights R5.46). Fields still nil answer 501 on their routes.
func newInsightsArea(db *store.Store) *api.InsightsArea {
	return &api.InsightsArea{
		Resources: db.Resources(), Peaks: db.Resources(),
		Waits: waits.NewReader(db.InsightJobSource(), nil, time.Now),
	}
}

// insightsBridge carries "a CI run finished" from the job ingest to the engine. The ingest is built before the
// engine, so it holds the bridge and the bridge holds the engine once it exists. It never blocks.
var insightsBridge triggerBridge

type triggerBridge struct {
	t atomic.Pointer[insights.Triggerer]
}

func (b *triggerBridge) set(t insights.Triggerer) { b.t.Store(&t) }

// Trigger forwards to the engine; before the engine exists it does nothing.
func (b *triggerBridge) Trigger() {
	if t := b.t.Load(); t != nil {
		(*t).Trigger()
	}
}

// periodOf turns the settings into the engine's update period (D18, D19).
func periodOf(s settings.Settings) insights.Period {
	return insights.Period{Mode: insights.Mode(s.InsightsUpdatePeriod), N: s.InsightsUpdateEveryNthRuns}.Norm()
}

// periodFrom reads the update period at every decision, so a change in Settings takes effect at once; an
// unreadable setting keeps the default of every finished CI run.
func periodFrom(get func(context.Context) (settings.Settings, error)) func() insights.Period {
	return func() insights.Period {
		s, err := get(context.Background())
		if err != nil {
			return insights.Period{Mode: insights.ModeEveryRun}
		}
		return periodOf(s)
	}
}

// latestDigest is the newest weekly digest file under the data dir, "" when none was written (R5.38). The
// tray menu links to it.
func latestDigest(dataDir string) string {
	files, _ := filepath.Glob(filepath.Join(dataDir, "ci-data", "digests", "*.md"))
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files) // <YYYY>-W<ww>.md sorts by week
	last := files[len(files)-1]
	if strings.HasSuffix(last, ".md") {
		if _, err := os.Stat(last); err == nil {
			return last
		}
	}
	return ""
}

// startInsights mounts the insights services on the API options and starts the engine until ctx ends. A
// failed pass never stops the controller (R6.8): the engine logs it and the next pass runs.
func startInsights(ctx context.Context, logger *slog.Logger, opts *api.Options, d insightsDeps) {
	opts.Insights = d.Area
	opts.JobRunners = d.Runners
	if t, ok := d.Engine.(insights.Triggerer); ok {
		insightsBridge.set(t)
	}
	if d.Digest != nil {
		go digest.Run(ctx, d.Digest, insights.SystemClock{}, digest.Location(), func(err error) {
			logger.Warn("insights: weekly digest not written", "err", err)
		})
	}
	if d.Engine == nil {
		return
	}
	go func() {
		if err := d.Engine.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("insights engine stopped", "err", err)
		}
	}()
}

// newInsightsEngine builds the engine the way the controller runs it: the update period read from Settings at
// every decision, and every pass measured (average CPU, peak memory, store size on disk: the CI data folder and
// the database file) so that a pass over 5% CPU, 300 MB or 1 GB shows the cost warning (D18, D19).
func newInsightsEngine(analysers []insights.Analyser, fs insights.FindingStore, runs insights.RunLog,
	get func(context.Context) (settings.Settings, error), dataDir, dbFile string, logger *slog.Logger) insights.Engine {
	size := func() int64 {
		return insights.DirSize(filepath.Join(dataDir, "ci-data")) + insights.FileSize(dbFile) + insights.FileSize(dbFile+"-wal")
	}
	return insights.NewEngine(analysers, fs, runs, insights.WithLogger(logger),
		insights.WithPeriod(periodFrom(get)), insights.WithMeter(insights.ProcessMeter{StoreSize: size}))
}
