package timeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

const (
	idleMin     = 2 * time.Minute
	idleWeekMin = 15 * time.Minute
	week        = 7 * 24 * time.Hour
)

// IdleAnalyser is the timeline insights.Analyser (KindIdleNextToQueue, R5.22).
type IdleAnalyser struct {
	Jobs   insights.JobSource
	States RunnerStates
}

var _ insights.Analyser = (*IdleAnalyser)(nil)

// Name implements insights.Analyser.
func (*IdleAnalyser) Name() string { return "timeline" }

// Kinds implements insights.Analyser.
func (*IdleAnalyser) Kinds() []insights.Kind { return Kinds }

type iv struct{ s, e time.Time }

func merge(in []iv) []iv {
	sort.Slice(in, func(i, k int) bool { return in[i].s.Before(in[k].s) })
	var out []iv
	for _, x := range in {
		if n := len(out); n > 0 && !x.s.After(out[n-1].e) {
			if x.e.After(out[n-1].e) {
				out[n-1].e = x.e
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

// Analyse implements insights.Analyser.
func (a *IdleAnalyser) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	jobs, err := a.Jobs.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, fmt.Errorf("timeline: read jobs: %w", err)
	}
	idles, err := a.States.IdleSpans(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("timeline: read runner states: %w", err)
	}
	type group struct {
		labels  []string
		machine string
		spans   []iv
	}
	groups := map[string]*group{}
	for _, g := range idles {
		if g.End.Sub(g.Start) < idleMin {
			continue
		}
		k := LabelKey(g.Labels)
		x := groups[k]
		if x == nil {
			x = &group{labels: g.Labels}
			groups[k] = x
		}
		if x.machine == "" {
			x.machine = g.MachineName
		}
		x.spans = append(x.spans, iv{g.Start, g.End})
	}
	type pair struct{ idle, wait string }
	mins := map[pair]time.Duration{}
	jobsN := map[pair]int{}
	waitLabels := map[string][]string{}
	for _, j := range jobs {
		s, e, ok := waitSpan(j, w.To)
		if !ok || len(j.Labels) == 0 {
			continue
		}
		wk := LabelKey(j.Labels)
		for ik, g := range groups {
			if ik == wk || canTake(g.labels, j.Labels) {
				continue
			}
			var d time.Duration
			for _, m := range merge(append([]iv(nil), g.spans...)) {
				d += overlap(s, e, m.s, m.e)
			}
			if d > 0 {
				p := pair{ik, wk}
				mins[p] += d
				jobsN[p]++
				waitLabels[wk] = j.Labels
			}
		}
	}
	var out []insights.Finding
	for p, d := range mins {
		weekly := time.Duration(float64(d) * float64(week) / float64(w.To.Sub(w.From)))
		if weekly < idleWeekMin {
			continue
		}
		g := groups[p.idle]
		ev, err := json.Marshal(map[string]any{
			"idle_labels": g.labels, "waiting_labels": waitLabels[p.wait],
			"waiting_minutes_week": int(weekly.Minutes()), "waiting_jobs": jobsN[p],
		})
		if err != nil {
			return nil, fmt.Errorf("timeline: evidence: %w", err)
		}
		f := insights.Finding{
			Kind: insights.KindIdleNextToQueue, Scope: insights.ScopePool, Subject: p.idle + " > " + p.wait,
			Area:             insights.AreaPool,
			Title:            fmt.Sprintf("Idle runners (%s) sit next to a queue (%s)", p.idle, p.wait),
			Fix:              "Runners sit idle while jobs for other labels wait. Make the idle capacity useful: run Linux runners on a Windows machine, use the runners feature, or move a slot to the waiting side.",
			Evidence:         ev,
			MinutesSavedWeek: int(weekly.Minutes()),
		}
		if g.machine != "" {
			f.Action = insights.ActionMachineSlots
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Subject < out[k].Subject })
	return out, nil
}
