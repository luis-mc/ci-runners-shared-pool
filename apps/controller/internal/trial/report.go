package trial

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sides of an outcome.
const (
	SideHosted = "hosted"
	SideCrsp   = "crsp"
)

// JobEvent is a finished job as the webhook or the poller sees it. The hook
// OnJob takes it; the trial keeps no GitHub types.
type JobEvent struct {
	Repo        string
	RunID       int64
	Job         string
	Workflow    string
	Ref         string
	Labels      []string
	Conclusion  string
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

// Outcome is one recorded job.
type Outcome struct {
	Job        string `json:"job"`
	Side       string `json:"side"`
	Conclusion string `json:"conclusion"`
	WaitSec    int    `json:"wait_sec"`
	RunSec     int    `json:"run_sec"`
	// Would is set in observe mode: crsp would have taken this job.
	Would bool      `json:"would,omitempty"`
	At    time.Time `json:"at"`
}

// OSOf maps hosted and self-hosted labels to linux, windows or macos; "" when
// no label names one.
func OSOf(labels []string) string {
	for _, l := range labels {
		l = strings.ToLower(l)
		switch {
		case l == "linux" || strings.HasPrefix(l, "ubuntu"):
			return "linux"
		case l == "windows" || strings.HasPrefix(l, "windows"):
			return "windows"
		case l == "macos" || strings.HasPrefix(l, "macos"):
			return "macos"
		}
	}
	return ""
}

func sideOf(labels []string) string {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return SideCrsp
		}
	}
	return SideHosted
}

func sortStrings(s []string) { sort.Strings(s) }

func secs(a, b time.Time) int {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return 0
	}
	return int(b.Sub(a).Seconds())
}

// OnJob is the hook for a finished job. It does nothing for a repository
// without a trial. Observe records what crsp would take; shadow records the
// result of a shadow copy as crsp and dispatches a copy of a hosted job;
// canary records each job on the side its labels say.
func (m *Manager) OnJob(ctx context.Context, e JobEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.Get(ctx, e.Repo)
	if err != nil {
		return err
	}
	if st.Mode == Off {
		return nil
	}
	os := OSOf(e.Labels)
	o := Outcome{Job: e.Job, Side: sideOf(e.Labels), Conclusion: e.Conclusion,
		WaitSec: secs(e.CreatedAt, e.StartedAt), RunSec: secs(e.StartedAt, e.CompletedAt), At: e.CompletedAt}
	if o.At.IsZero() {
		o.At = m.now().UTC()
	}
	shadowCopy := st.Mode == Shadow && st.ShadowWorkflow != "" && strings.EqualFold(e.Workflow, st.ShadowWorkflow)
	if shadowCopy {
		o.Side = SideCrsp
	}
	if st.Mode == Observe {
		o.Would = os != "" && (m.serves == nil || m.serves(ctx, os))
	}
	if !shadowCopy && o.Side == SideHosted && os != "" {
		if st.Seen == nil {
			st.Seen = map[string]string{}
		}
		if _, ok := st.Seen[e.Job]; ok || len(st.Seen) < maxSeen {
			st.Seen[e.Job] = os
		}
	}
	if st.Mode == Shadow && !shadowCopy && o.Side == SideHosted && os != "" {
		if err := m.dispatchShadow(ctx, &st, e, o); err != nil {
			return err
		}
	}
	if err := m.addOutcome(ctx, e.Repo, o); err != nil {
		return err
	}
	return m.put(ctx, st)
}

func (m *Manager) dispatchShadow(ctx context.Context, st *State, e JobEvent, o Outcome) error {
	if len(st.ShadowJobs) > 0 && !containsFold(st.ShadowJobs, e.Job) {
		return nil
	}
	if m.serves != nil && !m.serves(ctx, OSOf(e.Labels)) {
		return nil
	}
	key := fmt.Sprintf("%d/%s", e.RunID, strings.ToLower(e.Job))
	if containsFold(st.Dispatched, key) {
		return nil
	}
	if m.disp == nil {
		return fmt.Errorf("shadow for %s: no way to start the shadow workflow", st.Repo)
	}
	ref := e.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if err := m.disp.Dispatch(ctx, st.Repo, st.ShadowWorkflow, ref, map[string]string{"job": e.Job, "run_id": fmt.Sprint(e.RunID)}); err != nil {
		return fmt.Errorf("shadow dispatch for %s: %w", st.Repo, err)
	}
	st.Dispatched = append(st.Dispatched, key)
	if n := len(st.Dispatched); n > maxDispatched {
		st.Dispatched = append([]string(nil), st.Dispatched[n-maxDispatched:]...)
	}
	return nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func (m *Manager) outcomes(ctx context.Context, repo string) ([]Outcome, error) {
	raw, ok, err := m.kv.Get(ctx, outcomeKey(repo))
	if err != nil || !ok || raw == "" {
		return nil, err
	}
	var out []Outcome
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("trial outcomes of %s: %w", repo, err)
	}
	return out, nil
}

func (m *Manager) addOutcome(ctx context.Context, repo string, o Outcome) error {
	list, err := m.outcomes(ctx, repo)
	if err != nil {
		return err
	}
	list = append(list, o)
	if n := len(list); n > maxOutcomes {
		list = list[n-maxOutcomes:]
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return m.kv.Set(ctx, outcomeKey(repo), string(b))
}

// SideStats is one side of a job's comparison.
type SideStats struct {
	Runs       int `json:"runs"`
	Succeeded  int `json:"succeeded"`
	AvgWaitSec int `json:"avg_wait_sec"`
	AvgRunSec  int `json:"avg_run_sec"`
}

// JobRow compares one job on both sides.
type JobRow struct {
	Job    string    `json:"job"`
	Hosted SideStats `json:"hosted"`
	Crsp   SideStats `json:"crsp"`
	// WouldTake counts the hosted runs observe said crsp would take.
	WouldTake int `json:"would_take"`
	// Agree says both sides ended the same way on every pair (needs runs on both).
	Agree *bool `json:"agree,omitempty"`
}

// Report is the per-repository comparison.
type Report struct {
	Repo   string    `json:"repo"`
	Mode   Mode      `json:"mode"`
	Since  time.Time `json:"since,omitempty"`
	Jobs   []JobRow  `json:"jobs"`
	Total  int       `json:"total"`
	Hosted SideStats `json:"hosted"`
	Crsp   SideStats `json:"crsp"`
	// Variables are the canary variables now written, empty outside a canary.
	Variables []string `json:"variables"`
}

type acc struct{ runs, ok, wait, run int }

func (a *acc) add(o Outcome) {
	a.runs++
	if o.Conclusion == "success" {
		a.ok++
	}
	a.wait += o.WaitSec
	a.run += o.RunSec
}

func (a acc) stats() SideStats {
	s := SideStats{Runs: a.runs, Succeeded: a.ok}
	if a.runs > 0 {
		s.AvgWaitSec, s.AvgRunSec = a.wait/a.runs, a.run/a.runs
	}
	return s
}

// Report compares outcomes and times of a repository since the trial began
// (or all kept outcomes after the trial ended).
func (m *Manager) Report(ctx context.Context, repo string) (Report, error) {
	st, err := m.Get(ctx, repo)
	if err != nil {
		return Report{}, err
	}
	list, err := m.outcomes(ctx, repo)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Repo: repo, Mode: st.Mode, Since: st.Since, Jobs: []JobRow{}, Variables: append([]string{}, st.Written...)}
	type pair struct {
		h, c   acc
		would  int
		hc, cc map[string]bool
	}
	byJob := map[string]*pair{}
	var th, tc acc
	for _, o := range list {
		if !st.Since.IsZero() && st.Mode != Off && o.At.Before(st.Since) {
			continue
		}
		p := byJob[o.Job]
		if p == nil {
			p = &pair{hc: map[string]bool{}, cc: map[string]bool{}}
			byJob[o.Job] = p
		}
		rep.Total++
		if o.Side == SideCrsp {
			p.c.add(o)
			tc.add(o)
			p.cc[o.Conclusion] = true
		} else {
			p.h.add(o)
			th.add(o)
			p.hc[o.Conclusion] = true
			if o.Would {
				p.would++
			}
		}
	}
	for n, p := range byJob {
		row := JobRow{Job: n, Hosted: p.h.stats(), Crsp: p.c.stats(), WouldTake: p.would}
		if p.h.runs > 0 && p.c.runs > 0 {
			same := len(p.hc) == 1 && len(p.cc) == 1
			if same {
				for k := range p.hc {
					same = p.cc[k]
				}
			}
			row.Agree = &same
		}
		rep.Jobs = append(rep.Jobs, row)
	}
	sort.Slice(rep.Jobs, func(i, j int) bool { return rep.Jobs[i].Job < rep.Jobs[j].Job })
	rep.Hosted, rep.Crsp = th.stats(), tc.stats()
	return rep, nil
}
