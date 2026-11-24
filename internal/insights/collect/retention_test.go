package collect_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/collect"
	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

var day = 24 * time.Hour

// seeded is a store and a vault with files written through the vault and rows as the collector writes them.
type seeded struct {
	t     *testing.T
	db    *store.Store
	vault *collect.FSVault
	now   time.Time
}

func newSeeded(t *testing.T) *seeded {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &seeded{t: t, db: db, vault: &collect.FSVault{Dir: t.TempDir(), Redactor: redact.New()}, now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
}

// log stores a log fetched age ago whose index row says bytes (the cap counts the row's size).
func (s *seeded) log(repo, job int64, age time.Duration, bytes int64, expires string) string {
	s.t.Helper()
	st, err := s.vault.PutLog(context.Background(), collect.Key{RepositoryID: repo, RunID: 1, RunAttempt: 1, JobID: job}, strings.NewReader("line\n"))
	if err != nil {
		s.t.Fatal(err)
	}
	err = s.db.SaveCIJobLog(context.Background(), store.CIJobLog{JobID: job, RepositoryID: repo, RunID: 1, RunAttempt: 1, Path: st.Path, Bytes: bytes,
		State: "stored", FetchedAt: s.now.Add(-age).Format(time.RFC3339), ExpiresAt: expires})
	if err != nil {
		s.t.Fatal(err)
	}
	return st.Path
}

func (s *seeded) report(repo, artifact int64, age time.Duration, bytes int64) string {
	s.t.Helper()
	st, err := s.vault.PutReport(context.Background(), collect.Key{RepositoryID: repo, RunID: 1, RunAttempt: 1, ArtifactID: artifact, Member: "junit.xml"}, strings.NewReader("<testsuite/>"))
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.db.SaveCIReportFile(context.Background(), store.CIReportFile{RepositoryID: repo, RunID: 1, RunAttempt: 1, ArtifactID: artifact, Member: "junit.xml",
		Format: "junit", Path: st.Path, Bytes: bytes, State: "parsed", FetchedAt: s.now.Add(-age).Format(time.RFC3339)})
	if err != nil {
		s.t.Fatal(err)
	}
	return st.Path
}

func (s *seeded) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(s.vault.Dir, filepath.FromSlash(rel)))
	return err == nil
}

// R5.6: files past their days go first, then the oldest files of a repository over its size cap; the rows are
// kept, marked expired; another repository is not touched.
func TestDaysThenSizeCapOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newSeeded(t)
	past := s.now.Add(-time.Hour).Format(time.RFC3339)
	a := s.log(1, 1, 40*day, 400, "") // past the 30 days
	b := s.log(1, 2, 10*day, 400, "") // oldest of what is left: goes for the cap
	c := s.report(1, 3, 5*day, 400)   // next oldest: goes for the cap
	d := s.log(1, 4, 2*day, 400, "")  // stays
	e := s.report(1, 5, 1*day, 400)   // stays
	f := s.log(1, 6, 1*day, 10, past) // its own expiry time is past
	g := s.log(2, 7, 20*day, 400, "") // another repository, under its cap: stays
	ret := &collect.FileRetention{Index: s.db, Vault: s.vault}
	lim := collect.Limits{Keep: true, Days: 30 * day, BytesPerRepository: 1000}
	removed, freed, err := ret.Prune(ctx, s.now, lim)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 || freed != 400+400+400+10 {
		t.Fatalf("removed %d, freed %d", removed, freed)
	}
	for name, p := range map[string]string{"a": a, "b": b, "c": c, "f": f} {
		if s.exists(p) {
			t.Errorf("%s still on disk", name)
		}
	}
	for name, p := range map[string]string{"d": d, "e": e, "g": g} {
		if !s.exists(p) {
			t.Errorf("%s was deleted", name)
		}
	}
	row, ok, _ := s.db.CIJobLogOf(ctx, 1)
	if !ok || row.State != "expired" || row.Path != "" {
		t.Fatalf("row of a = %+v", row)
	}
	rep, ok, _ := s.db.CIReportFileOf(ctx, 1, 3, "junit.xml")
	if !ok || rep.State != "expired" {
		t.Fatalf("row of c = %+v", rep)
	}
	// a second pass finds nothing to do
	if n, _, err := ret.Prune(ctx, s.now, lim); err != nil || n != 0 {
		t.Fatalf("second pass removed %d, %v", n, err)
	}
	// a lowered cap trims the same repository further, oldest first
	lim.BytesPerRepository = 400
	if n, _, err := ret.Prune(ctx, s.now, lim); err != nil || n != 1 || s.exists(d) || !s.exists(e) {
		t.Fatalf("after the lowered cap: removed %d, %v", n, err)
	}
}

// A file that is already gone from the disk does not stop the pass; the row is still marked.
func TestMissingFileDoesNotStopPrune(t *testing.T) {
	s := newSeeded(t)
	p := s.log(1, 1, 40*day, 10, "")
	if err := os.Remove(filepath.Join(s.vault.Dir, filepath.FromSlash(p))); err != nil {
		t.Fatal(err)
	}
	ret := &collect.FileRetention{Index: s.db, Vault: s.vault}
	n, _, err := ret.Prune(context.Background(), s.now, collect.Limits{Days: 30 * day, BytesPerRepository: 1 << 30})
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v", n, err)
	}
}

// R6.2: the defaults, floors and ceilings of the settings are the table of the spec, and the settings reach
// the limits the retention gets.
func TestDefaultsFloorsCeilings(t *testing.T) {
	d := settings.Defaults
	if !d.InsightsKeepCIData || d.InsightsCIDataDays != 30 || d.InsightsCIDataMiBPerRepository != 1024 ||
		d.InsightsTestResultsDays != 90 || d.InsightsResourceSamplesDays != 7 {
		t.Fatalf("defaults = %+v", d)
	}
	want := map[string]settings.Range{
		"insights_ci_data_days":               {Min: 7, Max: 365},
		"insights_ci_data_mib_per_repository": {Min: 64, Max: 20480},
		"insights_test_results_days":          {Min: 14, Max: 400},
		"insights_resource_samples_days":      {Min: 1, Max: 30},
	}
	for k, r := range want {
		if settings.Ranges[k] != r {
			t.Errorf("%s = %v, want %v", k, settings.Ranges[k], r)
		}
	}
	l := settings.CIDataLimits(d)
	if !l.Keep || l.Days != 30*day || l.BytesPerRepository != 1<<30 || l.TestResultsDays != 90*day || l.ResourceSamplesDays != 7*day {
		t.Fatalf("limits = %+v", l)
	}
}
