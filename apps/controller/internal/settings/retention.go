package settings

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/collect"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// LogsDir is the folder inside the data dir that holds log files. A machine's
// logs live in a sub-folder named after it.
const LogsDir = "logs"

// LogPruner is the log database (internal/logs.Store): it deletes whole days older than cutoff and then,
// per machine, the oldest days until the machine holds at most maxBytesPerMachine (R6.8).
type LogPruner interface {
	Prune(ctx context.Context, cutoff time.Time, maxBytesPerMachine int64) (int64, error)
}

// Retention deletes data past the limits in Settings (observability R5.32,
// R6.8): job history, raw load samples, audit rows and log files. It never
// makes a network call (R6.7).
type Retention struct {
	DB       *store.Store
	Settings *Service
	Audit    *audit.Log
	DataDir  string
	Logger   *slog.Logger
	Now      func() time.Time
	// Interval is the time between runs; one hour when zero (R5.32).
	Interval time.Duration
	// CIData prune the CI data of ci-run-insights (R5.6, R6.2): the stored logs and report files, and
	// whatever else the insights keep with their own days (test results, resource samples). Each gets the
	// limits of the settings; a failure of one does not stop the others.
	CIData []collect.Retention
	// Logs, when set, is the log database (logs.db): lines past the log days and the size per machine are
	// deleted by day (R6.8). A failure joins the other failures and never stops them.
	Logs LogPruner
}

// CIDataLimits are the insights limits in st (ci-run-insights R5.5).
func CIDataLimits(st Settings) collect.Limits {
	day := 24 * time.Hour
	return collect.Limits{
		Keep:                st.InsightsKeepCIData,
		Days:                time.Duration(st.InsightsCIDataDays) * day,
		BytesPerRepository:  int64(st.InsightsCIDataMiBPerRepository) << 20,
		TestResultsDays:     time.Duration(st.InsightsTestResultsDays) * day,
		ResourceSamplesDays: time.Duration(st.InsightsResourceSamplesDays) * day,
	}
}

// RunOnce applies every limit once. It carries on after one step fails and
// returns all failures together.
func (r *Retention) RunOnce(ctx context.Context) error {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	st, err := r.Settings.Get(ctx)
	if err != nil {
		return err
	}
	t := now()
	day := 24 * time.Hour
	var errs []error
	if _, err := r.DB.DeleteJobHistoryBefore(ctx, t.Add(-time.Duration(st.RetentionJobHistoryDays)*day)); err != nil {
		errs = append(errs, err)
	}
	if _, err := r.DB.Jobs().Prune(ctx, t, jobsdata.DefaultRetention); err != nil {
		errs = append(errs, err)
	}
	if _, err := r.DB.TrimJobHistoryToRows(ctx, store.MaxJobHistoryRows); err != nil {
		errs = append(errs, err)
	}
	if err := r.DB.DeleteMachineLoadBefore(ctx, t.Add(-time.Duration(st.RetentionLoadRawHours)*time.Hour)); err != nil {
		errs = append(errs, err)
	}
	if _, err := r.Audit.Prune(ctx, t.Add(-time.Duration(st.RetentionAuditDays)*day)); err != nil {
		errs = append(errs, err)
	}
	if err := PruneLogs(filepath.Join(r.DataDir, LogsDir), t.Add(-time.Duration(st.RetentionLogsDays)*day), int64(st.RetentionLogsMiBPerMachine)<<20); err != nil {
		errs = append(errs, err)
	}
	if r.Logs != nil {
		if _, err := r.Logs.Prune(ctx, t.Add(-time.Duration(st.RetentionLogsDays)*day), int64(st.RetentionLogsMiBPerMachine)<<20); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range r.CIData {
		if _, _, err := p.Prune(ctx, t, CIDataLimits(st)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run applies the limits every hour until ctx ends.
func (r *Retention) Run(ctx context.Context) {
	every := r.Interval
	if every <= 0 {
		every = time.Hour
	}
	log := r.Logger
	if log == nil {
		log = slog.Default()
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Error("retention", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

type logFile struct {
	path string
	size int64
	mod  time.Time
}

// PruneLogs deletes log files older than cutoff under dir, then, for each
// sub-folder (one per machine), the oldest files until it holds at most
// maxBytes. A missing folder is not an error.
func PruneLogs(dir string, cutoff time.Time, maxBytes int64) error {
	groups := map[string][]logFile{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				return fmt.Errorf("delete %s: %w", path, rerr)
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		group := ""
		if first := filepath.Dir(rel); first != "." {
			group = first
			for filepath.Dir(group) != "." {
				group = filepath.Dir(group)
			}
		}
		groups[group] = append(groups[group], logFile{path: path, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	if err != nil {
		return err
	}
	var errs []error
	for group, files := range groups {
		if group == "" {
			continue // files in the top folder follow the age limit only
		}
		var total int64
		for _, f := range files {
			total += f.size
		}
		sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
		for _, f := range files {
			if total <= maxBytes {
				break
			}
			if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			total -= f.size
		}
	}
	return errors.Join(errs...)
}
