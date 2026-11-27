package tests

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func row(id string, job string, run int64, attempt int, sha string, res Result, d time.Duration) Row {
	return Row{RepositoryID: 1, RunID: run, RunAttempt: attempt, JobIdent: job, HeadSHA: sha, TestID: id,
		Kind: Unit, FinishedAt: t0, Case: Case{Name: id, Suite: "s", Result: res, Duration: d, Assertions: -1}}
}

type fakeStore struct{ rows []Row }

func (f *fakeStore) ReplaceReport(context.Context, int64, []Row) error { return nil }
func (f *fakeStore) Between(_ context.Context, repo int64, w insights.Window, limit, offset int) ([]Row, error) {
	var out []Row
	for _, r := range f.rows {
		if r.RepositoryID == repo && !r.FinishedAt.Before(w.From) && r.FinishedAt.Before(w.To) {
			out = append(out, r)
		}
	}
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

type fakeJobs struct{ jobs []insights.JobFact }

func (f fakeJobs) JobsBetween(_ context.Context, _ insights.Window, repo int64) ([]insights.JobFact, error) {
	var out []insights.JobFact
	for _, j := range f.jobs {
		if repo == 0 || j.RepositoryID == repo {
			out = append(out, j)
		}
	}
	return out, nil
}
