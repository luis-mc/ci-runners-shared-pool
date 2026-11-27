package timeline

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func at(m float64) time.Time { return t0.Add(time.Duration(m * float64(time.Minute))) }

type fakeSource struct{ jobs []insights.JobFact }

func (f fakeSource) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return f.jobs, nil
}

type fakeStates struct{ idle []RunnerIdle }

func (f fakeStates) IdleSpans(context.Context, insights.Window) ([]RunnerIdle, error) {
	return f.idle, nil
}
