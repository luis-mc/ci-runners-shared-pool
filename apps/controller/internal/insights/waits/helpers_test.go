package waits

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

type fakeExplain struct {
	r   HoldReason
	err error
}

func (f fakeExplain) Explain(context.Context, insights.JobFact) (HoldReason, error) {
	return f.r, f.err
}

type fakeRoom bool

func (f fakeRoom) HasLightRoom(context.Context) (bool, error) { return bool(f), nil }

// crspJob: created at 0, queued after gh minutes, slot after wait, started after hold, run for run.
func crspJob(id int64, key string, gh, wait, hold, run float64) insights.JobFact {
	return insights.JobFact{
		ID: id, JobIdent: key, Repository: "o/r", Origin: insights.OriginCrsp,
		CreatedAt: at(0), QueuedAt: at(gh), SlotAt: at(wait), StartedAt: at(wait + hold), CompletedAt: at(wait + hold + run),
	}
}

var weekWindow = insights.Window{From: t0.AddDate(0, 0, -7), To: t0}
