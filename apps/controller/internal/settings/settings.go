// Package settings reads and changes the controller settings: retention
// (observability R5.32, R6.8), the backup time and the number of backup days
// (operations R5.21, R5.23). A value outside its floor and ceiling is refused
// with the allowed range. Every change writes one audit row in the same
// transaction (observability R5.24, R5.25).
//
// Other areas read settings with Service.Get. Unset keys have the defaults
// below, so a fresh database needs no seeding.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Settings are the controller settings with defaults applied.
type Settings struct {
	RetentionJobHistoryDays    int
	RetentionLogsDays          int
	RetentionLogsMiBPerMachine int
	RetentionAuditDays         int
	RetentionLoadRawHours      int
	BackupTimeLocal            string // HH:MM, 24-hour local time
	BackupKeepDays             int
	MaxEventStreams            int
	// FallbackGraceMinutes is how long no crsp machine of an OS must have been
	// up before a repository that opted in falls back to GitHub-hosted runners
	// (github-integration R5.43).
	FallbackGraceMinutes int
	// OverflowLabel is the one cloud runner label, of any provider, that an
	// opted-in repository runs on while no crsp machine is up and its policy
	// names no label of its own. Empty means the GitHub-hosted defaults
	// (github-integration R5.42, CRSP261).
	OverflowLabel string
	// OverflowTargets are the owner's priority rules for cloud overflow, first
	// target first (github-integration R5.114, CRSP281). Empty means the single
	// OverflowLabel rule above.
	OverflowTargets []OverflowTarget
	// AutoUpdate is operations R5.15: on by default; a newer stable release
	// installs by itself after a drain.
	AutoUpdate bool
	// The CI data keys (ci-run-insights R5.5, R5.14, R5.31, R6.2; owner decision D4).
	InsightsKeepCIData             bool
	InsightsCIDataDays             int
	InsightsCIDataMiBPerRepository int
	InsightsTestResultsDays        int
	InsightsResourceSamplesDays    int
	InsightsHoldCapSeconds         int
	InsightsGuardrailArtifact      string
	// InsightsUpdatePeriod is how often insights are re-analysed: every_run, every_nth_run or hourly (D18, D19).
	InsightsUpdatePeriod       string
	InsightsUpdateEveryNthRuns int
}

// Patch is a partial change. A nil field is left alone.
type Patch struct {
	RetentionJobHistoryDays    *int
	RetentionLogsDays          *int
	RetentionLogsMiBPerMachine *int
	RetentionAuditDays         *int
	RetentionLoadRawHours      *int
	BackupTimeLocal            *string
	BackupKeepDays             *int
	MaxEventStreams            *int
	FallbackGraceMinutes       *int
	OverflowLabel              *string
	OverflowTargets            *[]OverflowTarget
	AutoUpdate                 *bool

	InsightsKeepCIData             *bool
	InsightsCIDataDays             *int
	InsightsCIDataMiBPerRepository *int
	InsightsTestResultsDays        *int
	InsightsResourceSamplesDays    *int
	InsightsHoldCapSeconds         *int
	InsightsGuardrailArtifact      *string
	InsightsUpdatePeriod           *string
	InsightsUpdateEveryNthRuns     *int
}

// Defaults are the owner's defaults (observability R6.8, operations R5.21).
var Defaults = Settings{
	RetentionJobHistoryDays:    90,
	RetentionLogsDays:          14,
	RetentionLogsMiBPerMachine: 512,
	RetentionAuditDays:         400,
	RetentionLoadRawHours:      24,
	BackupTimeLocal:            "03:00",
	BackupKeepDays:             14,
	MaxEventStreams:            50,
	FallbackGraceMinutes:       3,
	AutoUpdate:                 true,

	InsightsKeepCIData:             true,
	InsightsCIDataDays:             30,
	InsightsCIDataMiBPerRepository: 1024,
	InsightsTestResultsDays:        90,
	InsightsResourceSamplesDays:    7,
	InsightsHoldCapSeconds:         120,
	InsightsGuardrailArtifact:      "test-guardrails*",
	InsightsUpdatePeriod:           "every_run",
	InsightsUpdateEveryNthRuns:     5,
}

// validUpdatePeriod says whether v is one of the update periods of CI run insights (D18, D19).
func validUpdatePeriod(v string) bool {
	return v == "every_run" || v == "every_nth_run" || v == "hourly"
}

// MaxGuardrailArtifactLen is the longest guard report name pattern (openapi maxLength).
const MaxGuardrailArtifactLen = 100

// Range is the floor and ceiling of a numeric setting.
type Range struct{ Min, Max int }

// Ranges match the Settings schema in packages/contract/openapi.yaml.
var Ranges = map[string]Range{
	"retention_job_history_days":     {7, 400},
	"retention_logs_days":            {1, 90},
	"retention_logs_mib_per_machine": {64, 10240},
	"retention_audit_days":           {90, 1825},
	"retention_load_raw_hours":       {1, 168},
	"backup_keep_days":               {1, 365},
	"max_event_streams":              {1, 200},
	"fallback_grace_minutes":         {1, 60},

	"insights_ci_data_days":               {7, 365},
	"insights_ci_data_mib_per_repository": {64, 20480},
	"insights_test_results_days":          {14, 400},
	"insights_resource_samples_days":      {1, 30},
	"insights_hold_cap_seconds":           {30, 900},
	"insights_update_every_n_runs":        {2, 50},
}

// RangeError lists every refused value with its allowed range.
type RangeError struct {
	Problems []string
}

func (e *RangeError) Error() string { return strings.Join(e.Problems, "; ") }

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Service reads and writes settings.
type Service struct {
	db *store.Store
}

// New returns the settings service over db.
func New(db *store.Store) *Service { return &Service{db: db} }

// Get returns the settings with defaults for every key not stored. A stored
// value that no longer fits its range (an edited database) falls back to the
// default, so a bad row never stops retention or backup.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	rows, err := s.db.ListSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	out := Defaults
	for _, r := range rows {
		switch r.Key {
		case "auto_update":
			var v bool
			if json.Unmarshal([]byte(r.Value), &v) == nil {
				out.AutoUpdate = v
			}
		case "insights_keep_ci_data":
			var v bool
			if json.Unmarshal([]byte(r.Value), &v) == nil {
				out.InsightsKeepCIData = v
			}
		case "insights_guardrail_artifact":
			var v string
			if json.Unmarshal([]byte(r.Value), &v) == nil && validGuardrailArtifact(v) {
				out.InsightsGuardrailArtifact = v
			}
		case "insights_update_period":
			var v string
			if json.Unmarshal([]byte(r.Value), &v) == nil && validUpdatePeriod(v) {
				out.InsightsUpdatePeriod = v
			}
		case "overflow_label":
			var v string
			if json.Unmarshal([]byte(r.Value), &v) == nil && validOverflowLabel(v) {
				out.OverflowLabel = v
			}
		case "overflow_targets":
			var v []OverflowTarget
			if json.Unmarshal([]byte(r.Value), &v) == nil && validOverflowTargets(v) == nil {
				out.OverflowTargets = v
			}
		case "backup_time_local":
			var v string
			if json.Unmarshal([]byte(r.Value), &v) == nil && hhmm.MatchString(v) {
				out.BackupTimeLocal = v
			}
		default:
			var v int
			if json.Unmarshal([]byte(r.Value), &v) != nil || !inRange(r.Key, v) {
				continue
			}
			if p := intField(&out, r.Key); p != nil {
				*p = v
			}
		}
	}
	return out, nil
}

func inRange(key string, v int) bool {
	r, ok := Ranges[key]
	return ok && v >= r.Min && v <= r.Max
}

func intField(s *Settings, key string) *int {
	switch key {
	case "retention_job_history_days":
		return &s.RetentionJobHistoryDays
	case "retention_logs_days":
		return &s.RetentionLogsDays
	case "retention_logs_mib_per_machine":
		return &s.RetentionLogsMiBPerMachine
	case "retention_audit_days":
		return &s.RetentionAuditDays
	case "retention_load_raw_hours":
		return &s.RetentionLoadRawHours
	case "backup_keep_days":
		return &s.BackupKeepDays
	case "max_event_streams":
		return &s.MaxEventStreams
	case "fallback_grace_minutes":
		return &s.FallbackGraceMinutes
	case "insights_ci_data_days":
		return &s.InsightsCIDataDays
	case "insights_ci_data_mib_per_repository":
		return &s.InsightsCIDataMiBPerRepository
	case "insights_test_results_days":
		return &s.InsightsTestResultsDays
	case "insights_resource_samples_days":
		return &s.InsightsResourceSamplesDays
	case "insights_hold_cap_seconds":
		return &s.InsightsHoldCapSeconds
	case "insights_update_every_n_runs":
		return &s.InsightsUpdateEveryNthRuns
	}
	return nil
}

// Update validates p, writes the changed keys and one audit row in one
// transaction, and returns the new settings. A refused change writes a
// "refused" audit row and changes nothing.
func (s *Service) Update(ctx context.Context, actor audit.Actor, p Patch) (Settings, error) {
	before, err := s.Get(ctx)
	if err != nil {
		return Settings{}, err
	}
	after := before
	ints := map[string]*int{
		"retention_job_history_days":     p.RetentionJobHistoryDays,
		"retention_logs_days":            p.RetentionLogsDays,
		"retention_logs_mib_per_machine": p.RetentionLogsMiBPerMachine,
		"retention_audit_days":           p.RetentionAuditDays,
		"retention_load_raw_hours":       p.RetentionLoadRawHours,
		"backup_keep_days":               p.BackupKeepDays,
		"max_event_streams":              p.MaxEventStreams,
		"fallback_grace_minutes":         p.FallbackGraceMinutes,

		"insights_ci_data_days":               p.InsightsCIDataDays,
		"insights_ci_data_mib_per_repository": p.InsightsCIDataMiBPerRepository,
		"insights_test_results_days":          p.InsightsTestResultsDays,
		"insights_resource_samples_days":      p.InsightsResourceSamplesDays,
		"insights_hold_cap_seconds":           p.InsightsHoldCapSeconds,
		"insights_update_every_n_runs":        p.InsightsUpdateEveryNthRuns,
	}
	var problems []string
	changes := map[string]any{}
	writes := map[string]string{}
	for _, key := range sortedKeys(Ranges) {
		v := ints[key]
		if v == nil {
			continue
		}
		r := Ranges[key]
		if *v < r.Min || *v > r.Max {
			problems = append(problems, fmt.Sprintf("%s must be between %d and %d", key, r.Min, r.Max))
			continue
		}
		old := *intField(&before, key)
		*intField(&after, key) = *v
		if old != *v {
			changes[key] = strconv.Itoa(old) + " -> " + strconv.Itoa(*v)
			writes[key] = strconv.Itoa(*v)
		}
	}
	if p.BackupTimeLocal != nil {
		v := *p.BackupTimeLocal
		switch {
		case !hhmm.MatchString(v):
			problems = append(problems, "backup_time_local must be HH:MM in 24-hour time, from 00:00 to 23:59")
		case v != before.BackupTimeLocal:
			after.BackupTimeLocal = v
			changes["backup_time_local"] = before.BackupTimeLocal + " -> " + v
			b, _ := json.Marshal(v)
			writes["backup_time_local"] = string(b)
		}
	}
	if p.OverflowLabel != nil {
		v := strings.TrimSpace(*p.OverflowLabel)
		switch {
		case !validOverflowLabel(v):
			problems = append(problems, fmt.Sprintf("overflow_label must be empty or one runner label of up to %d characters without spaces, commas or quotes", MaxOverflowLabelLen))
		case v != before.OverflowLabel:
			after.OverflowLabel = v
			changes["overflow_label"] = before.OverflowLabel + " -> " + v
			b, _ := json.Marshal(v)
			writes["overflow_label"] = string(b)
		}
	}
	if p.OverflowTargets != nil {
		v := normalizeTargets(*p.OverflowTargets)
		if err := validOverflowTargets(v); err != nil {
			problems = append(problems, "overflow_targets: "+err.Error())
		} else if !sameTargets(v, before.OverflowTargets) {
			after.OverflowTargets = v
			changes["overflow_targets"] = describeTargets(before.OverflowTargets) + " -> " + describeTargets(v)
			b, _ := json.Marshal(v)
			writes["overflow_targets"] = string(b)
		}
	}
	if p.AutoUpdate != nil && *p.AutoUpdate != before.AutoUpdate {
		after.AutoUpdate = *p.AutoUpdate
		changes["auto_update"] = strconv.FormatBool(before.AutoUpdate) + " -> " + strconv.FormatBool(*p.AutoUpdate)
		writes["auto_update"] = strconv.FormatBool(*p.AutoUpdate)
	}
	if p.InsightsKeepCIData != nil && *p.InsightsKeepCIData != before.InsightsKeepCIData {
		after.InsightsKeepCIData = *p.InsightsKeepCIData
		changes["insights_keep_ci_data"] = strconv.FormatBool(before.InsightsKeepCIData) + " -> " + strconv.FormatBool(*p.InsightsKeepCIData)
		writes["insights_keep_ci_data"] = strconv.FormatBool(*p.InsightsKeepCIData)
	}
	if p.InsightsGuardrailArtifact != nil {
		v := *p.InsightsGuardrailArtifact
		switch {
		case !validGuardrailArtifact(v):
			problems = append(problems, fmt.Sprintf("insights_guardrail_artifact must be 1 to %d characters", MaxGuardrailArtifactLen))
		case v != before.InsightsGuardrailArtifact:
			after.InsightsGuardrailArtifact = v
			changes["insights_guardrail_artifact"] = before.InsightsGuardrailArtifact + " -> " + v
			b, _ := json.Marshal(v)
			writes["insights_guardrail_artifact"] = string(b)
		}
	}
	if p.InsightsUpdatePeriod != nil {
		v := *p.InsightsUpdatePeriod
		switch {
		case !validUpdatePeriod(v):
			problems = append(problems, "insights_update_period must be every_run, every_nth_run or hourly")
		case v != before.InsightsUpdatePeriod:
			after.InsightsUpdatePeriod = v
			changes["insights_update_period"] = before.InsightsUpdatePeriod + " -> " + v
			b, _ := json.Marshal(v)
			writes["insights_update_period"] = string(b)
		}
	}
	target := audit.Target{Type: "settings", ID: "controller"}
	if len(problems) > 0 {
		_ = audit.New(s.db).RecordRefused(ctx, actor, "settings.update", target, map[string]any{"summary": strings.Join(problems, "; ")})
		return Settings{}, &RangeError{Problems: problems}
	}
	if len(writes) == 0 {
		return after, nil
	}
	err = s.db.WithTx(ctx, func(tx *store.Tx) error {
		for _, key := range sortedKeys(writes) {
			if err := tx.SetSetting(key, writes[key], actor.Login); err != nil {
				return err
			}
		}
		return audit.RecordTx(tx, actor, "settings.update", target, changes)
	})
	if err != nil {
		return Settings{}, err
	}
	return after, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// small maps: insertion sort keeps the file free of another import
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func validGuardrailArtifact(v string) bool { return v != "" && len(v) <= MaxGuardrailArtifactLen }

// MaxOverflowLabelLen is the longest overflow label (openapi maxLength).
const MaxOverflowLabelLen = 100

// validOverflowLabel accepts empty (the GitHub-hosted defaults) or one label
// that is safe inside the JSON list written to a runs-on variable.
func validOverflowLabel(v string) bool {
	return len(v) <= MaxOverflowLabelLen && !strings.ContainsAny(v, " \t\r\n,\"\\")
}

// The overflow target kinds (github-integration R5.114).
const (
	TargetGitHubHosted = "github_hosted"
	TargetAWSEC2       = "aws_ec2"
)

// MaxOverflowCapMinutes bounds a target's monthly cap per repository.
const MaxOverflowCapMinutes = 1000000

// MaxOverflowPriceUSD bounds a target's price per minute.
const MaxOverflowPriceUSD = 1000

// MaxOverflowBudgetUSD bounds a target's monthly money budget.
const MaxOverflowBudgetUSD = 1000000

// OverflowTarget is one rule of the owner's overflow priority list: a cloud a
// queued job may go to while no crsp machine can take it.
type OverflowTarget struct {
	// Kind is github_hosted or aws_ec2, once each.
	Kind    string
	Enabled bool
	// Label is the runner label jobs run on at this target. Empty is allowed for
	// github_hosted only (the ubuntu-latest, windows-latest, macos-latest defaults).
	Label string
	// CapMinutes is the minutes per repository and month this target may take;
	// 0 means no cap. When it is used up the next target is tried.
	CapMinutes int
	// TotalCapMinutes is the minutes per month this target may take across all
	// repositories together; 0 means no cap. A target is used only while both
	// caps have minutes left.
	TotalCapMinutes int
	// BudgetUSD is the money, in US dollars per month across all repositories,
	// this target may cost; 0 means no budget (R5.115). It is checked beside
	// the minute caps: a target is used only while all of them have room.
	BudgetUSD float64
	// PriceUSDPerMinute is what one minute at this target costs, in US dollars
	// (R5.115, CRSP372); 0 means no price of its own. A set price counts the
	// budget at any target; github_hosted without one uses the savings rate
	// table, aws_ec2 without one has no rate and its budget is not enforced.
	PriceUSDPerMinute float64
	// Systems are the operating systems this target serves ("linux", "windows",
	// "macos"); empty means all.
	Systems []string
}

var targetSystems = map[string]bool{"linux": true, "windows": true, "macos": true}

func validOverflowTargets(v []OverflowTarget) error {
	seen := map[string]bool{}
	for _, t := range v {
		if t.Kind != TargetGitHubHosted && t.Kind != TargetAWSEC2 {
			return fmt.Errorf("kind %q is not github_hosted or aws_ec2", t.Kind)
		}
		if seen[t.Kind] {
			return fmt.Errorf("%s is listed twice", t.Kind)
		}
		seen[t.Kind] = true
		t.Label = strings.TrimSpace(t.Label)
		if !validOverflowLabel(t.Label) {
			return fmt.Errorf("the label of %s must be one runner label of up to %d characters without spaces, commas or quotes", t.Kind, MaxOverflowLabelLen)
		}
		if t.Kind == TargetAWSEC2 && t.Enabled && t.Label == "" {
			return fmt.Errorf("aws_ec2 needs a runner label while it is on")
		}
		if t.CapMinutes < 0 || t.CapMinutes > MaxOverflowCapMinutes {
			return fmt.Errorf("the cap of %s must be from 0 to %d minutes", t.Kind, MaxOverflowCapMinutes)
		}
		if t.TotalCapMinutes < 0 || t.TotalCapMinutes > MaxOverflowCapMinutes {
			return fmt.Errorf("the total cap of %s must be from 0 to %d minutes", t.Kind, MaxOverflowCapMinutes)
		}
		if math.IsNaN(t.BudgetUSD) || t.BudgetUSD < 0 || t.BudgetUSD > MaxOverflowBudgetUSD {
			return fmt.Errorf("the budget of %s must be from 0 to %d dollars", t.Kind, MaxOverflowBudgetUSD)
		}
		if math.IsNaN(t.PriceUSDPerMinute) || t.PriceUSDPerMinute < 0 || t.PriceUSDPerMinute > MaxOverflowPriceUSD {
			return fmt.Errorf("the price per minute of %s must be from 0 to %d dollars", t.Kind, MaxOverflowPriceUSD)
		}
		for _, s := range t.Systems {
			if !targetSystems[s] {
				return fmt.Errorf("system %q is not linux, windows or macos", s)
			}
		}
	}
	return nil
}

func sameTargets(a, b []OverflowTarget) bool {
	return describeTargets(a) == describeTargets(b)
}

func describeTargets(v []OverflowTarget) string {
	if len(v) == 0 {
		return "none"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func normalizeTargets(in []OverflowTarget) []OverflowTarget {
	out := make([]OverflowTarget, len(in))
	for i, t := range in {
		t.Label = strings.TrimSpace(t.Label)
		out[i] = t
	}
	return out
}
