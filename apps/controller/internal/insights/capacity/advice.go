package capacity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// Thresholds of R5.17 and R5.18.
const (
	poolWaitOver     = 2 * time.Minute
	poolMinCut       = 0.30
	repoLimitWaitMin = 30 * time.Minute
	maxExtraSlots    = 4
	week             = 7 * 24 * time.Hour
)

// Advisor is the capacity analyser (insights.Analyser): pool.size and repo.slots findings.
type Advisor struct {
	Demand    DemandReader
	Estimator Estimator
	Headroom  HeadroomReader
	Repos     RepoLoadReader
}

var _ insights.Analyser = (*Advisor)(nil)

// Name implements insights.Analyser.
func (*Advisor) Name() string { return "capacity" }

// Kinds implements insights.Analyser.
func (*Advisor) Kinds() []insights.Kind { return Kinds }

// Analyse implements insights.Analyser.
func (a *Advisor) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	loads, err := a.Demand.Demand(ctx, w)
	if err != nil {
		return nil, err
	}
	heads, err := a.Headroom.Headroom(ctx, w)
	if err != nil {
		return nil, err
	}
	repos, err := a.Repos.RepoLoads(ctx, w)
	if err != nil {
		return nil, err
	}
	est := a.Estimator
	if est == nil {
		est = AllenCunneen{}
	}
	var out []insights.Finding
	for _, l := range loads {
		if f, ok := poolSizeFinding(est, l, heads, w); ok {
			out = append(out, f)
		}
	}
	for _, r := range repos {
		if f, ok := repoSlotsFinding(r, heads, w); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// SlotRoom is how many more slots a machine takes inside the 85 percent memory and 60 percent CPU budget,
// from the measured size of a slot. A slot size not measured yet leaves the reader's own MaxExtraSlots.
func SlotRoom(h Headroom) int {
	room := h.MaxExtraSlots
	if h.SlotMemory > 0 {
		room = minInt(room, int(maxI64(h.FreeMemory, 0)/h.SlotMemory))
	}
	if h.SlotCPUMillis > 0 {
		room = minInt(room, int(maxI64(h.FreeCPUMillis, 0)/h.SlotCPUMillis))
	}
	return maxInt(room, 0)
}

func hasLabels(have, need []string) bool {
	set := map[string]bool{}
	for _, l := range have {
		set[strings.ToLower(l)] = true
	}
	for _, l := range need {
		if !set[strings.ToLower(l)] {
			return false
		}
	}
	return true
}

func weekFactor(w insights.Window) float64 {
	d := w.To.Sub(w.From)
	if d <= 0 {
		return 0
	}
	return float64(week) / float64(d)
}

func gib(b int64) int64 { return (b + (1 << 30) - 1) >> 30 }

func poolSizeFinding(est Estimator, l LabelSetLoad, heads []Headroom, w insights.Window) (insights.Finding, bool) {
	if l.P95Wait <= poolWaitOver || l.Slots <= 0 {
		return insights.Finding{}, false
	}
	now := est.Estimate(l, 0)
	need := 1
	var cutPct float64
	var after Estimate
	if now.Saturated {
		// The queue grows without bound: any slot count under MinSlots leaves it growing.
		need = maxInt(now.MinSlots-l.Slots, 1)
		after = est.Estimate(l, need)
		cutPct = 100
	} else {
		after = est.Estimate(l, 1)
		if after.Saturated {
			return insights.Finding{}, false
		}
		cut := 1 - float64(after.P95Wait)/float64(l.P95Wait)
		if cut < poolMinCut {
			return insights.Finding{}, false
		}
		cutPct = math.Round(cut * 100)
	}
	subject := strings.Join(l.Labels, ",")
	f := insights.Finding{
		Kind: insights.KindPoolSize, Scope: insights.ScopePool, Subject: subject, Area: insights.AreaPool,
	}
	// Minutes a week: jobs of the week times the mean wait that goes away.
	before := now.MeanWait
	if now.Saturated || before == 0 {
		before = l.P50Wait
	}
	jobs := l.JobsPerHour * w.To.Sub(w.From).Hours() * weekFactor(w)
	saved := before - after.MeanWait
	if now.Saturated {
		saved = before // the wait was unbounded: count what the median job waited
	}
	if saved > 0 {
		f.MinutesSavedWeek = int(math.Round(jobs * saved.Minutes()))
	}
	// The machine: labels fit, room for every slot needed, most room first.
	var best *Headroom
	var slotMem int64
	for i := range heads {
		h := heads[i]
		if !hasLabels(h.Labels, l.Labels) {
			continue
		}
		slotMem = maxI64(slotMem, h.SlotMemory)
		if SlotRoom(h) < need {
			continue
		}
		if best == nil || SlotRoom(h) > SlotRoom(*best) || (SlotRoom(h) == SlotRoom(*best) && h.MachineID < best.MachineID) {
			best = &heads[i]
		}
	}
	ev := map[string]any{
		"labels": l.Labels, "slots": l.Slots, "extra_slots": need, "utilisation": round2(l.Utilisation),
		"jobs_per_hour": round2(l.JobsPerHour), "cv_arrivals": round2(l.CVArrivals), "cv_service": round2(l.CVService),
		"mean_service_s": int64(l.MeanService.Seconds()), "p50_wait_s": int64(l.P50Wait.Seconds()),
		"p95_wait_s": int64(l.P95Wait.Seconds()), "saturated": now.Saturated,
		"estimated_p95_wait_s": int64(after.P95Wait.Seconds()), "estimated_cut_percent": cutPct,
		"budget_memory": MemoryBudget, "budget_cpu": CPUBudget,
	}
	slotWord := plural(need, "slot")
	if best != nil {
		f.Title = fmt.Sprintf("Jobs for %s wait %s at the p95", subject, shortDur(l.P95Wait))
		f.Fix = fmt.Sprintf("Add %d %s on %s. It has room inside the 85%% memory and 60%% CPU budget. Estimate: the p95 wait drops to about %s.",
			need, slotWord, best.MachineName, shortDur(after.P95Wait))
		f.Action = insights.ActionMachineSlots
		ev["machine_id"], ev["machine"] = best.MachineID, best.MachineName
	} else {
		n := gib(slotMem) * int64(need)
		if n < 1 {
			n = 1
		}
		f.Title = fmt.Sprintf("Jobs for %s wait %s at the p95 and no machine has room", subject, shortDur(l.P95Wait))
		f.Fix = fmt.Sprintf("No machine has room for %d more %s inside the budget. Add a machine with at least %d GiB free.", need, slotWord, n)
		ev["needed_memory_gib"] = n
	}
	f.Evidence = mustJSON(ev)
	return f, true
}

func repoSlotsFinding(r RepoLoad, heads []Headroom, w insights.Window) (insights.Finding, bool) {
	totalSlots, totalRoom, measured := 0, 0, false
	for _, h := range heads {
		totalSlots += h.Slots
		totalRoom += SlotRoom(h)
		if h.SlotMemory > 0 {
			measured = true
		}
	}
	f := insights.Finding{
		Kind: insights.KindRepoSlots, Scope: insights.ScopeRepo, RepositoryID: r.RepositoryID, Repository: r.Repository,
		Subject: r.Repository, Area: insights.AreaPool,
	}
	ev := map[string]any{
		"repository_parallel_limit": r.ParallelLimit, "limit_wait_s": int64(r.LimitWait.Seconds()),
		"p95_concurrent": r.P95Concurrent, "share_of_slots": round2(r.ShareOfSlots),
		"others_waited_s": int64(r.OthersWaited.Seconds()), "pool_slots": totalSlots,
	}
	switch {
	case r.ParallelLimit > 0 && r.LimitWait >= repoLimitWaitMin && r.P95Concurrent > r.ParallelLimit:
		target := r.P95Concurrent
		capped := false
		if measured && r.ParallelLimit+totalRoom < target {
			target, capped = r.ParallelLimit+totalRoom, true
		}
		f.MinutesSavedWeek = int(math.Round(r.LimitWait.Minutes() * weekFactor(w)))
		f.Title = fmt.Sprintf("%s waited %s for its own parallel limit while slots were free", r.Repository, shortDur(r.LimitWait))
		ev["advised_limit"], ev["capped_by_memory"] = target, capped
		if target <= r.ParallelLimit {
			f.Fix = "The machines have no memory room to raise the limit inside the 85% budget."
		} else {
			f.Fix = fmt.Sprintf("Raise the parallel jobs limit from %d to %d, the p95 of its concurrent jobs", r.ParallelLimit, target)
			if capped {
				f.Fix = fmt.Sprintf("Raise the parallel jobs limit from %d to %d, as far as the measured memory room allows", r.ParallelLimit, target)
			}
			f.Fix += "."
			f.Action = insights.ActionRepoParallelLimit
		}
	case r.ShareOfSlots > 0.5 && r.OthersWaited >= repoLimitWaitMin && totalSlots > 0 &&
		(r.ParallelLimit == 0 || r.ParallelLimit > totalSlots/2):
		target := maxInt(totalSlots/2, 1)
		f.MinutesSavedWeek = int(math.Round(r.OthersWaited.Minutes() * weekFactor(w)))
		f.Title = fmt.Sprintf("%s held %d%% of the slots while other projects waited", r.Repository, int(math.Round(r.ShareOfSlots*100)))
		f.Fix = fmt.Sprintf("Set a parallel jobs limit of %d, half of the %d slots, so other projects get a turn.", target, totalSlots)
		f.Action = insights.ActionRepoParallelLimit
		ev["advised_limit"] = target
	default:
		return insights.Finding{}, false
	}
	f.Evidence = mustJSON(ev)
	return f, true
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v) // map keys are sorted by encoding/json
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func plural(n int, w string) string {
	if n == 1 {
		return w
	}
	return w + "s"
}

func shortDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%.0f min", d.Minutes())
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
