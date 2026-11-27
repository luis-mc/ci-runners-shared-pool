package install

import (
	"regexp"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// Phase weights of the shared progress bar (CRSP247).
var phaseWeight = map[string]float64{"download": 3, "install": 5, "restart": 1, "verify": 1, "prepare": 1, "undo": 1}

var (
	secretRe  = regexp.MustCompile(`(?i)((?:token|password|passwd|secret|authorization|api[_-]?key|bearer)["']?\s*[=: ]\s*)(?:bearer\s+)?[^\s"',;]+`)
	flagRe    = regexp.MustCompile(`(?i)(--?(?:token|password|passwd|secret|api[_-]?key)[= ])\S+`)
	winHomeRe = regexp.MustCompile(`(?i)([A-Z]:\\Users\\)[^\\\s"']+`)
	unixHome  = regexp.MustCompile(`(/home/|/Users/)[^/\s"']+`)
	ghTokenRe = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
)

// Redact removes tokens, passwords and the user name of a profile path.
func Redact(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = ghTokenRe.ReplaceAllString(s, "[redacted]")
	s = flagRe.ReplaceAllString(s, "${1}[redacted]")
	s = secretRe.ReplaceAllString(s, "${1}[redacted]")
	s = winHomeRe.ReplaceAllString(s, `${1}<user>`)
	s = unixHome.ReplaceAllString(s, "${1}<user>")
	return s
}

func (r *Runner) redact(s string) string {
	s = Redact(s)
	if r.cfg.Redact != nil {
		s = r.cfg.Redact(s)
	}
	return s
}

func (r *Runner) now() time.Time {
	if r.cfg.Now != nil {
		return r.cfg.Now()
	}
	return time.Now()
}

// job is one Install or Undo run: its identity, its phases and what it added.
type job struct {
	r      *Runner
	id     string
	name   string
	role   string
	os     string
	phases []string
	added  []*agentv1.DepsAdded
	// last keeps the last output lines of the running command for error words.
	last []string
}

func (j *job) base(phase string) *agentv1.DepsEvent {
	idx := 0
	for i, p := range j.phases {
		if p == phase {
			idx = i
		}
	}
	return &agentv1.DepsEvent{
		JobId: j.id, Phase: phase, PhaseIndex: int32(idx), PhaseCount: int32(len(j.phases)), //nolint:gosec // small
		PhaseWeight: phaseWeight[phase], MachineName: j.name, MachineRole: j.role, MachineOs: j.os,
		Ts: j.r.now().UnixMilli(),
	}
}

func (j *job) send(e *agentv1.DepsEvent) {
	if j.r.cfg.Emit != nil {
		j.r.cfg.Emit(e)
	}
}

// status sends one status line. pct stays unset: the step has no real percent.
func (j *job) status(phase, line string) {
	e := j.base(phase)
	e.Stream, e.Line = "status", j.r.redact(line)
	e.Reason = "This step does not report a percentage."
	j.send(e)
}

func (j *job) final(phase, outcome, errText, engineVersion string) {
	e := j.base(phase)
	e.Stream, e.Final, e.Outcome = "status", true, outcome
	e.Error = j.r.redact(errText)
	e.Added = j.added
	e.EngineVersion = engineVersion
	switch outcome {
	case "done":
		p := 100.0
		e.Pct = &p
		e.Line = "Done."
	case "failed":
		e.Line = e.Error
		e.Reason = "The job stopped."
	case "paused_restart":
		e.Line = "Paused: a restart is needed to finish."
		e.Reason = "Waiting for a restart."
	case "stopped":
		e.Line = "Stopped."
		e.Reason = "The job was stopped."
	}
	j.send(e)
}

func (j *job) add(item, kind, ref, version string) {
	j.added = append(j.added, &agentv1.DepsAdded{ItemId: item, Kind: kind, Ref: ref, Version: version})
}

// progress returns the onProgress of a download: real bytes, rate and time left.
func (j *job) progress(phase, label string) func(b, t int64) {
	start := j.r.now()
	var lastSent time.Time
	return func(b, t int64) {
		now := j.r.now()
		done := t > 0 && b >= t
		if !done && !lastSent.IsZero() && now.Sub(lastSent) < 250*time.Millisecond {
			return
		}
		lastSent = now
		e := j.base(phase)
		e.Stream, e.Bytes, e.Total = "status", b, t
		e.Line = label
		if secs := now.Sub(start).Seconds(); secs > 0 && b > 0 {
			e.Rate = int64(float64(b) / secs)
			if t > b && e.Rate > 0 {
				e.EtaSec = (t - b) / e.Rate
			}
		}
		if t > 0 {
			p := float64(b) * 100 / float64(t)
			e.Pct = &p
		} else {
			e.Reason = "The server did not say how big the file is."
		}
		j.send(e)
	}
}
