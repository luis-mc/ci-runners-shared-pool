package main

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/history"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// poolOverviewSource is the live view's source: the queue and machines stay empty until the store adapter is
// wired (CRSP23); the minutes saved are real (observability R5.15).
type poolOverviewSource struct {
	overview.EmptySource
	DB *store.Store
}

// MinutesSavedSince sums, per OS, the finished pool jobs since the given time, each rounded up to a whole minute.
func (p poolOverviewSource) MinutesSavedSince(ctx context.Context, since time.Time) ([]overview.OSMinutes, error) {
	jobs, err := history.DBSource{DB: p.DB}.FinishedPoolJobs(ctx, since, time.Now().Add(time.Minute))
	if err != nil {
		return nil, err
	}
	by := map[string]int{}
	for _, j := range jobs {
		by[j.OS] += history.RoundUpMinutes(j.Run)
	}
	out := make([]overview.OSMinutes, 0, len(by))
	for os, m := range by {
		out = append(out, overview.OSMinutes{OS: os, Minutes: m})
	}
	return out, nil
}
