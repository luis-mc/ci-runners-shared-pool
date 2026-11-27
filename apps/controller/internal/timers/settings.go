package timers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// The fixed parts of the clocks (scheduling R5.100, R5.101).
const (
	// Grace is the time between a timer running out and crsp acting.
	Grace = 30 * time.Second
	// ExtendBy is what "Extend 10 min" adds, once per job.
	ExtendBy = 10 * time.Minute
)

// Settings are the timers, all whole minutes (scheduling R5.99, R5.104). The
// owner edits every one.
type Settings struct {
	// JobMin is the job timer: from the job's first real step. Default 30.
	JobMin int `json:"job_minutes"`
	// RunMin is the run timer: from the first job's real start. Default 45.
	RunMin int `json:"run_minutes"`
	// HoldMin is the default hold of the job-started hook, the machine's Gate
	// maximum wait. Default 2, never above slots.MaxHoldMin.
	HoldMin int `json:"hold_minutes"`
	// TimeoutMin is the timeout-minutes crsp asks workflows to carry: GitHub's
	// safety net only. Default 60.
	TimeoutMin int `json:"timeout_minutes"`
	// QueuedMin is how long a job may wait in GitHub's queue before crsp
	// alerts: 12 h, before GitHub's 24 h cancel. Default 720.
	QueuedMin int `json:"queued_minutes"`
	// StartupMin is how long a runner may take to start its job. Default 2.
	StartupMin int `json:"startup_minutes"`
	// Enforce says whether a timer that ran out stops the job or cancels the run.
	// False (the default, on a new install and on upgrade) is observe mode: the
	// countdowns and the grace run, crsp reports what it would do and sends no
	// stop and no cancel. Only the owner switches it on, in Settings.
	Enforce bool `json:"enforce"`
}

// Defaults are the owner's defaults of 22 Nov 2026.
func Defaults() Settings {
	return Settings{JobMin: 30, RunMin: 45, HoldMin: slots.MaxHoldMin, TimeoutMin: 60, QueuedMin: 12 * 60, StartupMin: 2}
}

// Field names, as the API and the console use them.
const (
	FieldJob     = "job_minutes"
	FieldRun     = "run_minutes"
	FieldHold    = "hold_minutes"
	FieldTimeout = "timeout_minutes"
	FieldQueued  = "queued_minutes"
	FieldStartup = "startup_minutes"
	FieldEnforce = "enforce"
)

// Fields lists the setting names in display order.
var Fields = []string{FieldJob, FieldRun, FieldHold, FieldTimeout, FieldQueued, FieldStartup, FieldEnforce}

// Messages of a refused value, in the owner's words.
const (
	MsgWhole   = "Use a whole number of minutes above 0."
	MsgJobRun  = "The job timer must be shorter than the run timer."
	MsgHoldMax = "The hold is at most 2 minutes."
)

// Input is a change of settings: a nil field is not given.
type Input struct {
	JobMin, RunMin, HoldMin, TimeoutMin, QueuedMin, StartupMin *int
	Enforce                                                    *bool
}

// Apply returns cur with the given fields set.
func (in Input) Apply(cur Settings) Settings {
	set := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
		}
	}
	set(&cur.JobMin, in.JobMin)
	set(&cur.RunMin, in.RunMin)
	set(&cur.HoldMin, in.HoldMin)
	set(&cur.TimeoutMin, in.TimeoutMin)
	set(&cur.QueuedMin, in.QueuedMin)
	set(&cur.StartupMin, in.StartupMin)
	if in.Enforce != nil {
		cur.Enforce = *in.Enforce
	}
	return cur
}

// Problem is one refused field.
type Problem struct {
	Field   string
	Message string
}

// Validate checks every field: whole minutes above 0, the hold within its cap
// and the job timer below the run timer. It is the same check the console runs
// while the owner types (scheduling R5.104).
func (s Settings) Validate() []Problem {
	var out []Problem
	for _, f := range []struct {
		name string
		v    int
	}{{FieldJob, s.JobMin}, {FieldRun, s.RunMin}, {FieldHold, s.HoldMin}, {FieldTimeout, s.TimeoutMin}, {FieldQueued, s.QueuedMin}, {FieldStartup, s.StartupMin}} {
		if f.v < 1 {
			out = append(out, Problem{f.name, MsgWhole})
		}
	}
	bad := map[string]bool{}
	for _, p := range out {
		bad[p.Field] = true
	}
	if !bad[FieldHold] && s.HoldMin > slots.MaxHoldMin {
		out = append(out, Problem{FieldHold, MsgHoldMax})
	}
	if !bad[FieldJob] && !bad[FieldRun] && s.JobMin >= s.RunMin {
		out = append(out, Problem{FieldJob, MsgJobRun})
	}
	return out
}

// Check validates cur changed by in, for check-while-typing. It changes nothing.
func Check(cur Settings, in Input) (Settings, []Problem) {
	next := in.Apply(cur)
	return next, next.Validate()
}

// Job is the job timer as a duration.
func (s Settings) Job() time.Duration { return time.Duration(s.JobMin) * time.Minute }

// Run is the run timer as a duration.
func (s Settings) Run() time.Duration { return time.Duration(s.RunMin) * time.Minute }

// Queued is the queued timer as a duration.
func (s Settings) Queued() time.Duration { return time.Duration(s.QueuedMin) * time.Minute }

// Startup is the start-up timer as a duration.
func (s Settings) Startup() time.Duration { return time.Duration(s.StartupMin) * time.Minute }

// Parse reads saved settings. A missing or bad field keeps its default, and a
// set that does not validate as a whole is replaced by the defaults, so a bad
// saved value never makes a timer fire at once.
func Parse(raw string) Settings {
	s := Defaults()
	if raw == "" {
		return s
	}
	var in map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &in) != nil {
		return s
	}
	cand := s
	for k, dst := range map[string]*int{FieldJob: &cand.JobMin, FieldRun: &cand.RunMin, FieldHold: &cand.HoldMin,
		FieldTimeout: &cand.TimeoutMin, FieldQueued: &cand.QueuedMin, FieldStartup: &cand.StartupMin} {
		var v int
		if json.Unmarshal(in[k], &v) == nil {
			*dst = v
		}
	}
	// A set saved before the enforce setting has none: it stays in observe mode.
	var enf bool
	if json.Unmarshal(in[FieldEnforce], &enf) == nil {
		cand.Enforce = enf
	}
	if len(cand.Validate()) > 0 {
		return s
	}
	return cand
}

// Encode is the saved form of the settings.
func (s Settings) Encode() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Clock tells the time; tests pass a fake.
type Clock interface{ Now() time.Time }

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

func mmss(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
