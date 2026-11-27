package capacity

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// SlotSource says how many slots can take a label set over a window.
type SlotSource interface {
	SlotsFor(ctx context.Context, labels []string, w insights.Window) (int, error)
}

// Demand measures LabelSetLoad (R5.15) from the jobs that went through crsp's slot queue.
type Demand struct {
	Jobs  insights.JobSource
	Slots SlotSource
}

var _ DemandReader = Demand{}

// Demand implements DemandReader.
func (d Demand) Demand(ctx context.Context, w insights.Window) ([]LabelSetLoad, error) {
	jobs, err := d.Jobs.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, err
	}
	groups := groupByLabels(jobs)
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]LabelSetLoad, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		slots, err := d.Slots.SlotsFor(ctx, g.labels, w)
		if err != nil {
			return nil, err
		}
		out = append(out, Measure(g.labels, g.jobs, slots, w))
	}
	return out, nil
}

type labelGroup struct {
	labels []string
	jobs   []insights.JobFact
}

func labelKey(labels []string) []string {
	l := append([]string(nil), labels...)
	sort.Strings(l)
	return l
}

func groupByLabels(jobs []insights.JobFact) map[string]*labelGroup {
	groups := map[string]*labelGroup{}
	for _, j := range jobs {
		if j.QueuedAt.IsZero() { // never went through the slot queue (other runners, hosted)
			continue
		}
		l := labelKey(j.Labels)
		k := strings.Join(l, ",")
		g := groups[k]
		if g == nil {
			g = &labelGroup{labels: l}
			groups[k] = g
		}
		g.jobs = append(g.jobs, j)
	}
	return groups
}

// Measure computes the load of one label set. Wait is the time from queued to slot; service is run plus
// hold: from the slot to completion (to start when no slot time is known). Busy time is clipped to w.
func Measure(labels []string, jobs []insights.JobFact, slots int, w insights.Window) LabelSetLoad {
	load := LabelSetLoad{Labels: labels, Slots: slots}
	hours := w.To.Sub(w.From).Hours()
	var arrivals []time.Time
	var services, waits []float64
	var busy time.Duration
	byDay := map[string][]float64{}
	for _, j := range jobs {
		arrivals = append(arrivals, j.QueuedAt)
		begin := j.SlotAt
		if begin.IsZero() {
			begin = j.StartedAt
		}
		if !begin.IsZero() && !j.QueuedAt.IsZero() && !begin.Before(j.QueuedAt) {
			wait := begin.Sub(j.QueuedAt).Seconds()
			waits = append(waits, wait)
			day := j.QueuedAt.UTC().Format("2006-01-02")
			byDay[day] = append(byDay[day], wait)
		}
		if !begin.IsZero() && !j.CompletedAt.IsZero() && j.CompletedAt.After(begin) {
			services = append(services, j.CompletedAt.Sub(begin).Seconds())
		}
		busy += overlap(begin, j.CompletedAt, w)
	}
	if hours > 0 {
		load.JobsPerHour = float64(len(jobs)) / hours
		if slots > 0 {
			load.Utilisation = busy.Minutes() / (float64(slots) * w.To.Sub(w.From).Minutes())
		}
	}
	sort.Slice(arrivals, func(a, b int) bool { return arrivals[a].Before(arrivals[b]) })
	gaps := make([]float64, 0, len(arrivals))
	for i := 1; i < len(arrivals); i++ {
		gaps = append(gaps, arrivals[i].Sub(arrivals[i-1]).Seconds())
	}
	load.CVArrivals = cv(gaps)
	load.CVService = cv(services)
	load.MeanService = time.Duration(mean(services) * float64(time.Second))
	load.P50Wait = secs(percentile(waits, 0.50))
	load.P95Wait = secs(percentile(waits, 0.95))
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		load.P95WaitByDay = append(load.P95WaitByDay, DayValue{Day: d, Value: percentile(byDay[d], 0.95)})
	}
	return load
}

// overlap is the length of [from, to) inside w; an open end counts to the window end.
func overlap(from, to time.Time, w insights.Window) time.Duration {
	if from.IsZero() {
		return 0
	}
	if to.IsZero() || to.After(w.To) {
		to = w.To
	}
	if from.Before(w.From) {
		from = w.From
	}
	if !to.After(from) {
		return 0
	}
	return to.Sub(from)
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// cv is the coefficient of variation (population standard deviation over the mean).
func cv(v []float64) float64 {
	m := mean(v)
	if len(v) < 2 || m == 0 {
		return 0
	}
	var ss float64
	for _, x := range v {
		ss += (x - m) * (x - m)
	}
	return math.Sqrt(ss/float64(len(v))) / m
}

// percentile is the nearest-rank percentile, p in (0, 1].
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}
