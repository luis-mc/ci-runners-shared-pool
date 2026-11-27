package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

func setup(t *testing.T) (*Service, *audit.Log) {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db), audit.New(db)
}

func ip(v int) *int { return &v }

var owner = audit.Actor{Login: "octo", ID: "1", Source: audit.SourceWeb}

func TestDefaultsAreTheOwnerDecisions(t *testing.T) {
	s, _ := setup(t)
	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionJobHistoryDays != 90 || got.RetentionLogsDays != 14 || got.RetentionLogsMiBPerMachine != 512 ||
		got.RetentionAuditDays != 400 || got.BackupTimeLocal != "03:00" || got.BackupKeepDays != 14 {
		t.Fatalf("%+v", got)
	}
}

func TestUpdateSavesAndAudits(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	tm := "04:30"
	got, err := s.Update(ctx, owner, Patch{RetentionAuditDays: ip(500), BackupTimeLocal: &tm})
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionAuditDays != 500 || got.BackupTimeLocal != "04:30" || got.RetentionLogsDays != 14 {
		t.Fatalf("%+v", got)
	}
	again, _ := s.Get(ctx)
	if again != got {
		t.Fatalf("not persisted: %+v vs %+v", again, got)
	}
	p, _ := l.List(ctx, audit.Filter{})
	if len(p.Items) != 1 || p.Items[0].Action != "settings.update" || p.Items[0].Actor != "octo" ||
		!strings.Contains(p.Items[0].Summary, "400 -> 500") || !strings.Contains(p.Items[0].Summary, "03:00 -> 04:30") {
		t.Fatalf("%+v", p.Items)
	}
	// No change, no audit row.
	if _, err := s.Update(ctx, owner, Patch{RetentionAuditDays: ip(500)}); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.List(ctx, audit.Filter{}); len(p.Items) != 1 {
		t.Fatalf("a no-op wrote a row: %d", len(p.Items))
	}
}

func TestOutOfRangeRefusedWithRange(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	bad := "25:00"
	_, err := s.Update(ctx, owner, Patch{RetentionLogsDays: ip(0), RetentionAuditDays: ip(30), BackupKeepDays: ip(14), BackupTimeLocal: &bad})
	var re *RangeError
	if !errors.As(err, &re) || len(re.Problems) != 3 {
		t.Fatalf("want 3 problems, got %v", err)
	}
	for _, want := range []string{"retention_logs_days must be between 1 and 90", "retention_audit_days must be between 90 and 1825", "HH:MM"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	got, _ := s.Get(ctx)
	if got != Defaults {
		t.Fatalf("a refused change was applied: %+v", got)
	}
	p, _ := l.List(ctx, audit.Filter{})
	if len(p.Items) != 1 || p.Items[0].Result != "refused" {
		t.Fatalf("want one refused row: %+v", p.Items)
	}
}

func TestEveryRangeMatchesFloorAndCeiling(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	for key, r := range Ranges {
		for _, v := range []int{r.Min, r.Max} {
			p := patchFor(key, v)
			if _, err := s.Update(ctx, owner, p); err != nil {
				t.Errorf("%s=%d at the limit: %v", key, v, err)
			}
		}
		for _, v := range []int{r.Min - 1, r.Max + 1} {
			if _, err := s.Update(ctx, owner, patchFor(key, v)); err == nil {
				t.Errorf("%s=%d outside the range was accepted", key, v)
			}
		}
	}
}

func patchFor(key string, v int) Patch {
	var p Patch
	switch key {
	case "retention_job_history_days":
		p.RetentionJobHistoryDays = &v
	case "retention_logs_days":
		p.RetentionLogsDays = &v
	case "retention_logs_mib_per_machine":
		p.RetentionLogsMiBPerMachine = &v
	case "retention_audit_days":
		p.RetentionAuditDays = &v
	case "retention_load_raw_hours":
		p.RetentionLoadRawHours = &v
	case "backup_keep_days":
		p.BackupKeepDays = &v
	case "max_event_streams":
		p.MaxEventStreams = &v
	case "fallback_grace_minutes":
		p.FallbackGraceMinutes = &v
	case "insights_ci_data_days":
		p.InsightsCIDataDays = &v
	case "insights_ci_data_mib_per_repository":
		p.InsightsCIDataMiBPerRepository = &v
	case "insights_test_results_days":
		p.InsightsTestResultsDays = &v
	case "insights_resource_samples_days":
		p.InsightsResourceSamplesDays = &v
	case "insights_hold_cap_seconds":
		p.InsightsHoldCapSeconds = &v
	case "insights_update_every_n_runs":
		p.InsightsUpdateEveryNthRuns = &v
	}
	return p
}

// CRSP19: auto-update is on by default, the owner can turn it off and on, and
// the choice survives a read.
func TestAutoUpdateDefaultsOnAndToggles(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	got, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoUpdate || !Defaults.AutoUpdate {
		t.Fatalf("auto_update must default to on: %+v", got)
	}
	off, on := false, true
	if got, err = s.Update(ctx, owner, Patch{AutoUpdate: &off}); err != nil || got.AutoUpdate {
		t.Fatalf("off: %+v %v", got, err)
	}
	if again, _ := s.Get(ctx); again.AutoUpdate {
		t.Fatal("off did not persist")
	}
	if got, err = s.Update(ctx, owner, Patch{AutoUpdate: &on}); err != nil || !got.AutoUpdate {
		t.Fatalf("on: %+v %v", got, err)
	}
	p, _ := l.List(ctx, audit.Filter{})
	if len(p.Items) != 2 || !strings.Contains(p.Items[0].Summary, "auto_update") {
		t.Fatalf("each change must write one audit row naming auto_update: %+v", p.Items)
	}
}

// CIX12 (R5.5, R6.2): the CI data keys, their defaults, floors and ceilings, and the audit row.
func TestInsightsRanges(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	got, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.InsightsKeepCIData || got.InsightsCIDataDays != 30 || got.InsightsCIDataMiBPerRepository != 1024 ||
		got.InsightsTestResultsDays != 90 || got.InsightsResourceSamplesDays != 7 || got.InsightsHoldCapSeconds != 120 ||
		got.InsightsGuardrailArtifact != "test-guardrails*" {
		t.Fatalf("defaults = %+v", got)
	}
	want := map[string]Range{
		"insights_ci_data_days":               {7, 365},
		"insights_ci_data_mib_per_repository": {64, 20480},
		"insights_test_results_days":          {14, 400},
		"insights_resource_samples_days":      {1, 30},
		"insights_hold_cap_seconds":           {30, 900},
	}
	for key, r := range want {
		if Ranges[key] != r {
			t.Errorf("%s range = %v, want %v", key, Ranges[key], r)
		}
		for _, v := range []int{r.Min, r.Max} {
			if _, err := s.Update(ctx, owner, patchFor(key, v)); err != nil {
				t.Errorf("%s=%d at the limit: %v", key, v, err)
			}
		}
		for _, v := range []int{r.Min - 1, r.Max + 1} {
			if _, err := s.Update(ctx, owner, patchFor(key, v)); err == nil {
				t.Errorf("%s=%d outside the range was accepted", key, v)
			}
		}
	}
	off, name, empty := false, "ci-guard*", ""
	if _, err := s.Update(ctx, owner, Patch{InsightsKeepCIData: &off, InsightsGuardrailArtifact: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, owner, Patch{InsightsGuardrailArtifact: &empty}); err == nil {
		t.Error("an empty guard report name was accepted")
	}
	got, _ = s.Get(ctx)
	if got.InsightsKeepCIData || got.InsightsGuardrailArtifact != "ci-guard*" {
		t.Fatalf("saved = %+v", got)
	}
	if l := CIDataLimits(got); l.Keep || l.Days != 365*24*time.Hour || l.BytesPerRepository != 20480<<20 {
		t.Fatalf("limits = %+v", l)
	}
}

// CIX23 (D18, D19): the update period of CI run insights: default every run, N from 2 to 50, three periods.
func TestInsightsUpdatePeriod(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	got, _ := s.Get(ctx)
	if got.InsightsUpdatePeriod != "every_run" || got.InsightsUpdateEveryNthRuns != 5 {
		t.Fatalf("defaults = %q, %d", got.InsightsUpdatePeriod, got.InsightsUpdateEveryNthRuns)
	}
	if Ranges["insights_update_every_n_runs"] != (Range{2, 50}) {
		t.Errorf("N range = %v", Ranges["insights_update_every_n_runs"])
	}
	for _, n := range []int{1, 51} {
		if _, err := s.Update(ctx, owner, Patch{InsightsUpdateEveryNthRuns: &n}); err == nil {
			t.Errorf("N=%d was accepted", n)
		}
	}
	bad := "daily"
	if _, err := s.Update(ctx, owner, Patch{InsightsUpdatePeriod: &bad}); err == nil {
		t.Error("an unknown period was accepted")
	}
	p, n := "every_nth_run", 10
	if _, err := s.Update(ctx, owner, Patch{InsightsUpdatePeriod: &p, InsightsUpdateEveryNthRuns: &n}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx)
	if got.InsightsUpdatePeriod != "every_nth_run" || got.InsightsUpdateEveryNthRuns != 10 {
		t.Fatalf("saved = %q, %d", got.InsightsUpdatePeriod, got.InsightsUpdateEveryNthRuns)
	}
}

// CRSP261: the overflow label is any one cloud runner label; empty means the GitHub-hosted defaults.
func TestOverflowLabel(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	got, _ := s.Get(ctx)
	if got.OverflowLabel != "" {
		t.Fatalf("default %q", got.OverflowLabel)
	}
	v := " acme-cloud-4x "
	if _, err := s.Update(ctx, owner, Patch{OverflowLabel: &v}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx)
	if got.OverflowLabel != "acme-cloud-4x" {
		t.Fatalf("saved %q", got.OverflowLabel)
	}
	for _, bad := range []string{"two words", "a,b", `x"y`, string(make([]byte, 101))} {
		b := bad
		if _, err := s.Update(ctx, owner, Patch{OverflowLabel: &b}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	empty := ""
	if _, err := s.Update(ctx, owner, Patch{OverflowLabel: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx)
	if got.OverflowLabel != "" {
		t.Fatalf("cleared %q", got.OverflowLabel)
	}
}
