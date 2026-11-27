package waits

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

const (
	// DefaultHoldCap is the hold cap when the setting is unset (R5.14).
	DefaultHoldCap = 2 * time.Minute
	holdWeekMin    = 10 * time.Minute
	houseRunMax    = time.Minute
	houseWaitMin   = 2 * time.Minute
	houseMinJobs   = 3
	week           = 7 * 24 * time.Hour
)

// LightRoom says whether a machine has room for a light slot (action light_lane, R5.28).
type LightRoom interface {
	HasLightRoom(ctx context.Context) (bool, error)
}

// Analyser is the waits insights.Analyser (KindWaitHold and KindHousekeepingLane).
type Analyser struct {
	Jobs    insights.JobSource
	Explain HoldExplainer // nil: reasons are unknown
	Room    LightRoom     // nil: no light_lane action
	// HoldCap returns the setting insights_hold_cap_seconds; nil or out of 30..900 s means the default.
	HoldCap func(ctx context.Context) time.Duration
}

var _ insights.Analyser = (*Analyser)(nil)

// Name implements insights.Analyser.
func (*Analyser) Name() string { return "waits" }

// Kinds implements insights.Analyser.
func (*Analyser) Kinds() []insights.Kind { return Kinds }

func (a *Analyser) cap(ctx context.Context) time.Duration {
	if a.HoldCap != nil {
		if c := a.HoldCap(ctx); c >= 30*time.Second && c <= 900*time.Second {
			return c
		}
	}
	return DefaultHoldCap
}

// scale turns a total over w into a weekly total.
func scale(d time.Duration, w insights.Window) time.Duration {
	l := w.To.Sub(w.From)
	if l <= 0 {
		return 0
	}
	return time.Duration(float64(d) * float64(week) / float64(l))
}

// Analyse implements insights.Analyser.
func (a *Analyser) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	jobs, err := a.Jobs.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, fmt.Errorf("waits: read jobs: %w", err)
	}
	byKey := map[string][]insights.JobFact{}
	for _, j := range jobs {
		byKey[j.JobIdent] = append(byKey[j.JobIdent], j)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []insights.Finding
	for _, k := range keys {
		if f, ok, err := a.hold(ctx, w, k, byKey[k]); err != nil {
			return nil, err
		} else if ok {
			out = append(out, f)
		}
		if f, ok, err := a.housekeeping(ctx, w, k, byKey[k]); err != nil {
			return nil, err
		} else if ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func (a *Analyser) hold(ctx context.Context, w insights.Window, key string, jobs []insights.JobFact) (insights.Finding, bool, error) {
	hc := a.cap(ctx)
	var over, excess time.Duration
	n := 0
	reasons := map[HoldReason]int{}
	sp := NewSplitter()
	for _, j := range jobs {
		s := sp.Split(j)
		if !s.HoldKnown || s.Hold <= hc {
			continue
		}
		n++
		over += s.Hold
		excess += s.Hold - hc
		r := HoldUnknown
		if a.Explain != nil {
			if got, err := a.Explain.Explain(ctx, j); err == nil && got != "" {
				r = got
			} else if err != nil && ctx.Err() != nil {
				return insights.Finding{}, false, fmt.Errorf("waits: explain hold: %w", ctx.Err())
			}
		}
		reasons[r]++
	}
	if scale(over, w) < holdWeekMin {
		return insights.Finding{}, false, nil
	}
	top, topN := HoldUnknown, 0
	for r, c := range reasons {
		if c > topN || (c == topN && r < top) {
			top, topN = r, c
		}
	}
	ev, err := json.Marshal(map[string]any{
		"holds_over_cap": n, "hold_cap_seconds": int(hc.Seconds()),
		"minutes_week": int(scale(over, w).Minutes()), "reasons": reasons,
	})
	if err != nil {
		return insights.Finding{}, false, fmt.Errorf("waits: evidence: %w", err)
	}
	return insights.Finding{
		Kind: insights.KindWaitHold, Scope: insights.ScopePool, Subject: key, Area: insights.AreaWaits,
		Title:            fmt.Sprintf("%s holds a slot too long before it starts", key),
		Fix:              holdFix(top),
		Evidence:         ev,
		MinutesSavedWeek: int(scale(excess, w).Minutes()),
	}, true, nil
}

func holdFix(r HoldReason) string {
	switch r {
	case HoldImagePull:
		return "The runner image is pulled on every start. Keep the image on the machine or pull it ahead of time."
	case HoldRunnerStart:
		return "The runner takes long to start. Check the machine for load and the runner start log."
	case HoldRegistration:
		return "Registering the runner with GitHub takes long. Check the GitHub status and the token refresh."
	case HoldLoad:
		return "The machine is over its load budget when the job starts. Give it fewer slots or a lighter load."
	default:
		return "The slot is held a long time before the job starts and the log does not say why. Open the job's set-up log."
	}
}

func (a *Analyser) housekeeping(ctx context.Context, w insights.Window, key string, jobs []insights.JobFact) (insights.Finding, bool, error) {
	var waits, runs []time.Duration
	sp := NewSplitter()
	var total time.Duration
	for _, j := range jobs {
		s := sp.Split(j)
		if !s.WaitKnown || !s.RunKnown {
			continue
		}
		waits = append(waits, s.Wait)
		runs = append(runs, s.Run)
		total += s.Wait
	}
	if len(runs) < houseMinJobs {
		return insights.Finding{}, false, nil
	}
	mr, mw := ComputeStats(runs).P50, ComputeStats(waits).P50
	if mr >= houseRunMax || mw <= houseWaitMin {
		return insights.Finding{}, false, nil
	}
	f := insights.Finding{
		Kind: insights.KindHousekeepingLane, Scope: insights.ScopePool, Subject: key, Area: insights.AreaWaits,
		Title: fmt.Sprintf("%s runs under a minute but waits %d min", key, int(mw.Minutes())),
		Fix: "Run it on a light lane (light slots), move it to a schedule outside the busy hours, " +
			"or run it on a GitHub-hosted runner.",
		MinutesSavedWeek: int(scale(total, w).Minutes()),
	}
	if a.Room != nil {
		ok, err := a.Room.HasLightRoom(ctx)
		if err != nil {
			return insights.Finding{}, false, fmt.Errorf("waits: light room: %w", err)
		}
		if ok {
			f.Action = insights.ActionLightLane
		}
	}
	ev, err := json.Marshal(map[string]any{
		"jobs": len(runs), "median_run_seconds": int(mr.Seconds()), "median_wait_seconds": int(mw.Seconds()),
	})
	if err != nil {
		return insights.Finding{}, false, fmt.Errorf("waits: evidence: %w", err)
	}
	f.Evidence = ev
	return f, true, nil
}
