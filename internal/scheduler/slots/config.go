package slots

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// OnTimeout values of R5.20.
const (
	OnTimeoutFail = "fail"
	OnTimeoutRun  = "run"
)

// JobTimeoutMin is the longest timeout-minutes crsp asks a job to carry
// (doctor workflow.timeout, guideline 60). GitHub's timeout-minutes is a safety
// net only: crsp's own job and run timers end a job first (scheduling R5.99).
const JobTimeoutMin = 60

// MaxHoldMin is the longest the job-started hook may hold a job: 2 minutes. A
// longer setting is cut to it when the hook reads it (R5.75).
const MaxHoldMin = 2

// Settings are the scheduling settings of one machine (R5.1).
type Settings struct {
	BudgetMemoryPct int
	BudgetCPUPct    int
	JobSlots        int
	LightSlots      int
	HeavySlots      int // 0 means no heavy limit
	SlotMaxAgeMin   int
	GateMaxWaitMin  int
	GatePollSec     int
	OnTimeout       string
	LightJobs       []string
	HeavyJobs       []string
	// WarmRunners is how many ready runners the machine keeps (scheduling
	// R5.66); 0 keeps none.
	WarmRunners int
	// Classes is the table of job classes and their weights (R5.69). It is
	// not a stored setting: every machine starts from DefaultClasses.
	Classes []JobClass
	// Sizes are the job sizes the scan, the measuring and the overrides gave
	// (R5.80 to R5.88), in precedence order: the first entry that matches a job
	// wins. Not a stored setting: the store fills it for every read.
	Sizes []JobSize
	// Admission is the pool admission of scheduling R5.90 to R5.94. The zero
	// value is off: the machine is admitted by unit weights (R5.69) as before.
	// Not a stored machine setting: the store fills it from the pool size of the
	// machine and the class settings.
	Admission Admission
}

// Defaults returns the dev-standards defaults (R5.1; machine_slots.py DEFAULT_CONFIG).
func Defaults() Settings {
	return Settings{
		BudgetMemoryPct: 85,
		BudgetCPUPct:    60,
		JobSlots:        2,
		LightSlots:      1,
		HeavySlots:      1,
		SlotMaxAgeMin:   120,
		GateMaxWaitMin:  MaxHoldMin,
		GatePollSec:     15,
		OnTimeout:       OnTimeoutFail,
		LightJobs:       []string{"smoke", "prune", "prune-*", "cleanup", "remove-train", "decide", "delete-*", "runners"},
		HeavyJobs:       []string{},
		Classes:         DefaultClasses(),
	}
}

// Keys are the setting names Write accepts (R5.3).
var Keys = []string{
	"budget_memory_pct", "budget_cpu_pct", "job_slots", "light_slots", "heavy_slots",
	"slot_max_age_minutes", "gate_max_wait_minutes", "gate_poll_seconds", "gate_on_timeout",
	"light_jobs", "heavy_jobs", "warm_runners",
}

// Reservation settings (scheduling R5.93) are global, not per machine: the
// keys of the settings table.
const (
	SettingReservePasses  = "reserve_after_passes"
	SettingReserveMinutes = "reserve_after_minutes"
	SettingSizeClasses    = "runner_size_classes"
)

// number reads v as a number. A bool is never a number (R5.2).
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// whole reads v as a whole number of at least lo.
func whole(v any, lo float64) (int, bool) {
	n, ok := number(v)
	if !ok || n < lo || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}

func strList(v any) ([]string, bool) {
	switch l := v.(type) {
	case []string:
		return append([]string{}, l...), true
	case []any:
		out := []string{}
		for _, x := range l {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

// Normalise applies every valid key of raw over the defaults. A bad value is
// dropped and the default stays (R5.2; machine_slots.py normalise_config).
func Normalise(raw map[string]any) Settings {
	s := Defaults()
	for _, k := range []string{"budget_memory_pct", "budget_cpu_pct"} {
		if n, ok := number(raw[k]); ok && n > 0 && n <= 100 {
			if k == "budget_memory_pct" {
				s.BudgetMemoryPct = int(n)
			} else {
				s.BudgetCPUPct = int(n)
			}
		}
	}
	if n, ok := whole(raw["job_slots"], 1); ok {
		s.JobSlots = n
	}
	if n, ok := whole(raw["light_slots"], 0); ok {
		s.LightSlots = n
	}
	if n, ok := whole(raw["heavy_slots"], 0); ok {
		s.HeavySlots = n
	}
	if n, ok := whole(raw["slot_max_age_minutes"], 1); ok {
		s.SlotMaxAgeMin = n
	}
	if n, ok := whole(raw["gate_max_wait_minutes"], 0); ok {
		s.GateMaxWaitMin = n
	}
	if n, ok := whole(raw["gate_poll_seconds"], 0); ok {
		s.GatePollSec = n
	}
	if v, ok := raw["gate_on_timeout"].(string); ok && (v == OnTimeoutFail || v == OnTimeoutRun) {
		s.OnTimeout = v
	}
	if n, ok := whole(raw["warm_runners"], 0); ok {
		s.WarmRunners = n
	}
	if l, ok := strList(raw["light_jobs"]); ok {
		s.LightJobs = l
	}
	if l, ok := strList(raw["heavy_jobs"]); ok {
		s.HeavyJobs = l
	}
	return s
}

// CheckKeys refuses an unknown key and names it (R5.3).
func CheckKeys(raw map[string]any) error {
	var unknown []string
	for k := range raw {
		if !contains(Keys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown setting %s", strings.Join(unknown, ", "))
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// Budget is a load budget in percent.
type Budget struct{ MemoryPct, CPUPct int }

// Limits is the budget in force for a job: a repository may lower the machine's
// budget, never raise it; a value outside 1 to 100 is ignored (R5.4; load.py limits).
func Limits(machine Budget, repoMemoryPct, repoCPUPct int) Budget {
	out := machine
	if repoMemoryPct >= 1 && repoMemoryPct <= 100 && repoMemoryPct < out.MemoryPct {
		out.MemoryPct = repoMemoryPct
	}
	if repoCPUPct >= 1 && repoCPUPct <= 100 && repoCPUPct < out.CPUPct {
		out.CPUPct = repoCPUPct
	}
	return out
}

// GateSettings are the values the hook waits by (R5.5).
type GateSettings struct {
	MaxWaitMin int
	PollSec    int
	OnTimeout  string
}

// ResolveGate resolves gate settings: a flag, then the environment, then the
// machine settings, first found wins (R5.5; runner_load_gate.py settings). A
// flag value of -1 (or an empty string, for text) means "not given". A poll
// below 0 counts as 0; an unknown on-timeout counts as fail.
func ResolveGate(flagMaxWait, flagPoll int, flagOnTimeout string, getenv func(string) string, machine Settings) GateSettings {
	out := GateSettings{MaxWaitMin: machine.GateMaxWaitMin, PollSec: machine.GatePollSec, OnTimeout: machine.OnTimeout}
	if v, ok := envInt(getenv, "CRSP_GATE_MAX_WAIT_MIN"); ok {
		out.MaxWaitMin = v
	}
	if v, ok := envInt(getenv, "CRSP_GATE_POLL_SEC"); ok {
		out.PollSec = v
	}
	if v := strings.TrimSpace(getenv("CRSP_GATE_ON_TIMEOUT")); v != "" {
		out.OnTimeout = v
	}
	if flagMaxWait >= 0 {
		out.MaxWaitMin = flagMaxWait
	}
	if flagPoll >= 0 {
		out.PollSec = flagPoll
	}
	if flagOnTimeout != "" {
		out.OnTimeout = flagOnTimeout
	}
	if out.PollSec < 0 {
		out.PollSec = 0
	}
	if out.MaxWaitMin < 0 {
		out.MaxWaitMin = 0
	}
	if out.MaxWaitMin > MaxHoldMin {
		out.MaxWaitMin = MaxHoldMin
	}
	if out.OnTimeout != OnTimeoutRun {
		out.OnTimeout = OnTimeoutFail
	}
	return out
}

func envInt(getenv func(string) string, name string) (int, bool) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Getenv is os.Getenv, for ResolveGate callers.
var Getenv = os.Getenv

// matchGlob reports whether name matches pattern as an exact or glob match.
func matchGlob(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
