package capacity

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

type fakeJobs []insights.JobFact

func (f fakeJobs) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return f, nil
}

type fakeSlots map[string]int

func (f fakeSlots) SlotsFor(_ context.Context, labels []string, _ insights.Window) (int, error) {
	return f[labels[0]], nil
}

var t0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func job(id int64, labels string, queuedMin, waitMin, serviceMin int) insights.JobFact {
	q := t0.Add(time.Duration(queuedMin) * time.Minute)
	s := q.Add(time.Duration(waitMin) * time.Minute)
	return insights.JobFact{ID: id, Labels: []string{labels}, QueuedAt: q, SlotAt: s, StartedAt: s, CompletedAt: s.Add(time.Duration(serviceMin) * time.Minute)}
}

// R5.15: utilisation is busy slot minutes over available slot minutes; arrivals and waits come from the jobs.
func TestUtilisationAndArrivals(t *testing.T) {
	w := insights.Window{From: t0, To: t0.Add(2 * time.Hour)} // 120 min, 2 slots: 240 slot minutes
	jobs := fakeJobs{
		job(1, "linux", 0, 0, 30), job(2, "linux", 10, 5, 30), job(3, "linux", 20, 10, 30), job(4, "linux", 30, 0, 30),
		{ID: 9, Labels: []string{"linux"}, CreatedAt: t0}, // never queued by crsp: ignored
		job(5, "gpu", 0, 1, 10),
	}
	got, err := Demand{Jobs: jobs, Slots: fakeSlots{"linux": 2, "gpu": 1}}.Demand(context.Background(), w)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	gpu, linux := got[0], got[1]
	if gpu.Labels[0] != "gpu" || linux.Labels[0] != "linux" {
		t.Fatalf("order %v %v", gpu.Labels, linux.Labels)
	}
	if want := 120.0 / 240; math.Abs(linux.Utilisation-want) > 1e-9 {
		t.Fatalf("utilisation %v want %v", linux.Utilisation, want)
	}
	if linux.JobsPerHour != 2 || linux.MeanService != 30*time.Minute || linux.CVService != 0 {
		t.Fatalf("%+v", linux)
	}
	// Gaps between arrivals are 10 min each: no variation.
	if linux.CVArrivals != 0 {
		t.Fatalf("cv arrivals %v", linux.CVArrivals)
	}
	// Waits 0, 5, 10, 0 min: nearest-rank p50 = 5 min, p95 = 10 min.
	if linux.P50Wait != 0 && linux.P50Wait != 5*time.Minute {
		t.Fatalf("p50 %v", linux.P50Wait)
	}
	if linux.P95Wait != 10*time.Minute {
		t.Fatalf("p95 %v", linux.P95Wait)
	}
	if len(linux.P95WaitByDay) != 1 || linux.P95WaitByDay[0].Day != "2026-11-10" || linux.P95WaitByDay[0].Value != 600 {
		t.Fatalf("by day %+v", linux.P95WaitByDay)
	}
	if gpu.Utilisation <= 0 || gpu.Slots != 1 {
		t.Fatalf("gpu %+v", gpu)
	}
}

// A job still running at the window end counts its busy time to the end; utilisation may pass 1.
func TestUtilisationClipsToWindowAndMayExceedOne(t *testing.T) {
	w := insights.Window{From: t0, To: t0.Add(time.Hour)}
	j := job(1, "linux", -30, 0, 600)
	l := Measure([]string{"linux"}, []insights.JobFact{j, job(2, "linux", 0, 0, 600)}, 1, w)
	if l.Utilisation != 2 {
		t.Fatalf("utilisation %v", l.Utilisation)
	}
	open := insights.JobFact{QueuedAt: t0, SlotAt: t0.Add(30 * time.Minute)}
	if l := Measure([]string{"x"}, []insights.JobFact{open}, 1, w); math.Abs(l.Utilisation-0.5) > 1e-9 {
		t.Fatalf("open job utilisation %v", l.Utilisation)
	}
}

func TestMeasureVariation(t *testing.T) {
	w := insights.Window{From: t0, To: t0.Add(24 * time.Hour)}
	// Arrivals at 0, 10, 40 min: gaps 600 s, 1800 s; mean 1200, sd 600, cv 0.5.
	jobs := []insights.JobFact{job(1, "l", 0, 0, 10), job(2, "l", 10, 0, 10), job(3, "l", 40, 0, 10)}
	l := Measure([]string{"l"}, jobs, 1, w)
	if math.Abs(l.CVArrivals-0.5) > 1e-9 {
		t.Fatalf("cv arrivals %v", l.CVArrivals)
	}
}
