package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// InsightsJobsStore is the SQLite side of the Job page faults (docs/features/ci-run-insights R5.39 to R5.41):
// it implements reconcile.Store and reconcile.JobStore, and records a job's runner origin from ingest.
type InsightsJobsStore struct{ s *Store }

// InsightsJobs returns the Job page faults view of the store.
func (s *Store) InsightsJobs() *InsightsJobsStore { return &InsightsJobsStore{s} }

var (
	_ reconcile.Store    = (*InsightsJobsStore)(nil)
	_ reconcile.JobStore = (*InsightsJobsStore)(nil)
)

// StaleRuns lists run attempts with queued or running job rows that look stale: no update since before, or the
// run is completed in GitHub's words; none checked since checkedBefore. Oldest first, at most limit.
func (st *InsightsJobsStore) StaleRuns(ctx context.Context, before, checkedBefore time.Time, limit int) ([]reconcile.StaleRun, error) {
	q := sqlcgen.New(st.s.db)
	stale, err := q.ListStaleJobRuns(ctx, sqlcgen.ListStaleJobRunsParams{
		UpdatedAt: fmtTime(before), CheckedAt: fmtTime(checkedBefore), Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list stale runs: %w", err)
	}
	completed, err := q.ListCompletedRunStaleJobRuns(ctx, sqlcgen.ListCompletedRunStaleJobRunsParams{
		CheckedAt: fmtTime(checkedBefore), Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list completed runs: %w", err)
	}
	type key struct {
		repo     int64
		run, att int64
	}
	seen := map[key]reconcile.StaleRun{}
	add := func(repoID int64, repo string, run, attempt int64, oldest string) {
		k := key{repoID, run, attempt}
		o := parseTimeOpt(oldest)
		if prev, ok := seen[k]; ok && !o.Before(prev.OldestUpdate) {
			return
		}
		seen[k] = reconcile.StaleRun{RepositoryID: repoID, Repository: repo, RunID: run, RunAttempt: int(attempt), OldestUpdate: o}
	}
	for _, r := range stale {
		add(r.RepositoryID, r.Repository, r.RunID, r.RunAttempt, r.OldestUpdate)
	}
	for _, r := range completed {
		add(r.RepositoryID, r.Repository, r.RunID, r.RunAttempt, r.OldestUpdate)
	}
	out := make([]reconcile.StaleRun, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OldestUpdate.Equal(out[j].OldestUpdate) {
			return out[i].OldestUpdate.Before(out[j].OldestUpdate)
		}
		return out[i].RunID < out[j].RunID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// OpenJobs lists the queued and running job ids of one run attempt.
func (st *InsightsJobsStore) OpenJobs(ctx context.Context, repositoryID, runID int64, attempt int) ([]int64, error) {
	return sqlcgen.New(st.s.db).ListOpenJobIDsOfRun(ctx, sqlcgen.ListOpenJobIDsOfRunParams{
		RepositoryID: repositoryID, RunID: runID, RunAttempt: int64(attempt)})
}

// JobRef reads the facts of one job the steps reads need.
func (st *InsightsJobsStore) JobRef(ctx context.Context, jobID int64) (reconcile.JobRef, bool, error) {
	q := sqlcgen.New(st.s.db)
	row, err := q.GetJobsRow(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return reconcile.JobRef{}, false, nil
	}
	if err != nil {
		return reconcile.JobRef{}, false, fmt.Errorf("get job: %w", err)
	}
	n, err := q.CountJobSteps(ctx, jobID)
	if err != nil {
		return reconcile.JobRef{}, false, fmt.Errorf("count steps: %w", err)
	}
	return reconcile.JobRef{Repository: row.Repository, State: row.State, StepsSource: row.StepsSource, HasSteps: n > 0}, true, nil
}

// SetRunnerOrigin records who ran a job (R5.39). A job not stored yet is skipped: the next event or the
// reconcile sets it. An empty origin never wipes a stored one.
func (st *InsightsJobsStore) SetRunnerOrigin(ctx context.Context, jobID int64, origin reconcile.Origin, group string, labels []string) error {
	return st.s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := q.GetJobsRow(ctx, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		return setOrigin(ctx, q, row, origin, group, labels)
	})
}

func setOrigin(ctx context.Context, q *sqlcgen.Queries, row sqlcgen.Job, origin reconcile.Origin, group string, labels []string) error {
	o, g, l := string(origin), group, jsonList(labels)
	if o == "" {
		o = row.RunnerOrigin
	}
	if g == "" {
		g = row.RunnerGroup
	}
	if len(labels) == 0 {
		l = row.RunnerLabels
	}
	return q.SetJobRunnerOrigin(ctx, sqlcgen.SetJobRunnerOriginParams{RunnerOrigin: o, RunnerGroup: g, RunnerLabels: l, ID: row.ID})
}

func replaceRemoteSteps(ctx context.Context, q *sqlcgen.Queries, jobID int64, steps []reconcile.RemoteStep) error {
	if err := q.DeleteJobSteps(ctx, jobID); err != nil {
		return fmt.Errorf("delete steps: %w", err)
	}
	for _, s := range steps {
		state, result := "queued", ""
		switch s.Status {
		case "in_progress":
			state = "running"
		case "completed":
			state, result = "done", string(reconcile.ResultOf(s.Conclusion))
			if result == "" {
				result = s.Conclusion // skipped and the like are kept as GitHub says them
			}
		}
		if err := q.InsertJobStep(ctx, sqlcgen.InsertJobStepParams{JobID: jobID, Number: int64(s.Number), Name: s.Name,
			State: state, Result: result, StartedAt: fmtTimeOpt(s.StartedAt), CompletedAt: fmtTimeOpt(s.CompletedAt)}); err != nil {
			return fmt.Errorf("insert step: %w", err)
		}
	}
	return nil
}

// Apply merges what GitHub said about one job (R5.41): state never goes backward, a field GitHub leaves empty
// keeps its stored value, steps are replaced when GitHub sent some (steps_source "rest") unless they came from
// events, and checked_at is set. A job crsp does not hold is skipped.
func (st *InsightsJobsStore) Apply(ctx context.Context, repositoryID int64, j reconcile.RemoteJob, origin reconcile.Origin, checkedAt time.Time) error {
	return st.s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := q.GetJobsRow(ctx, j.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		if row.RepositoryID != repositoryID {
			return nil
		}
		if state := reconcile.StateOf(j.Status); state != "" && stateRank(string(state)) >= stateRank(row.State) {
			if row.State != string(state) || state == jobsdata.Done {
				row.UpdatedAt = fmtTime(checkedAt)
			}
			row.State = string(state)
			if state == jobsdata.Done {
				if res := reconcile.ResultOf(j.Conclusion); res != "" {
					row.Result = string(res)
				}
			}
		}
		if !j.StartedAt.IsZero() {
			row.StartedAt = fmtTime(j.StartedAt)
		}
		if !j.CompletedAt.IsZero() && row.State == string(jobsdata.Done) {
			row.CompletedAt = fmtTime(j.CompletedAt)
		}
		if !j.CreatedAt.IsZero() && row.CreatedAt == "" {
			row.CreatedAt = fmtTime(j.CreatedAt)
		}
		if j.RunnerName != "" && row.RunnerName == "" {
			row.RunnerName = j.RunnerName
		}
		row.CheckedAt = fmtTime(checkedAt)
		if err := q.UpsertJobsRow(ctx, sqlcgen.UpsertJobsRowParams(row)); err != nil {
			return fmt.Errorf("update job: %w", err)
		}
		if err := setOrigin(ctx, q, row, origin, j.RunnerGroupName, j.Labels); err != nil {
			return err
		}
		if len(j.Steps) > 0 && row.StepsSource != "events" {
			if err := replaceRemoteSteps(ctx, q, j.ID, j.Steps); err != nil {
				return err
			}
			return q.SetJobStepsSource(ctx, sqlcgen.SetJobStepsSourceParams{StepsSource: "rest", ID: j.ID})
		}
		return nil
	})
}

// ApplySteps stores the steps of one job read by REST (R5.40).
func (st *InsightsJobsStore) ApplySteps(ctx context.Context, jobID int64, steps []reconcile.RemoteStep, checkedAt time.Time) error {
	return st.s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := q.GetJobsRow(ctx, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		if row.StepsSource == "events" {
			return nil
		}
		if len(steps) > 0 {
			if err := replaceRemoteSteps(ctx, q, jobID, steps); err != nil {
				return err
			}
			if err := q.SetJobStepsSource(ctx, sqlcgen.SetJobStepsSourceParams{StepsSource: "rest", ID: jobID}); err != nil {
				return err
			}
		}
		return q.SetJobCheckedAt(ctx, sqlcgen.SetJobCheckedAtParams{CheckedAt: fmtTime(checkedAt), ID: jobID})
	})
}

// MarkLost sets one open job done with the result lost and the reason (R5.41). A job that is already done
// keeps its result. The reason is kept in wait_text: the contract has no reason column.
func (st *InsightsJobsStore) MarkLost(ctx context.Context, jobID int64, reason string, at time.Time) error {
	return st.s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := q.GetJobsRow(ctx, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		if row.State == string(jobsdata.Done) {
			return q.SetJobCheckedAt(ctx, sqlcgen.SetJobCheckedAtParams{CheckedAt: fmtTime(at), ID: jobID})
		}
		row.State, row.Result, row.WaitText = string(jobsdata.Done), string(jobsdata.Lost), reason
		row.CompletedAt, row.UpdatedAt, row.CheckedAt = fmtTime(at), fmtTime(at), fmtTime(at)
		if err := q.UpsertJobsRow(ctx, sqlcgen.UpsertJobsRowParams(row)); err != nil {
			return fmt.Errorf("mark lost: %w", err)
		}
		return nil
	})
}

// SetStepsSource records where a job's steps came from: "events" or "rest" (R5.40).
func (st *InsightsJobsStore) SetStepsSource(ctx context.Context, jobID int64, source string) error {
	return sqlcgen.New(st.s.db).SetJobStepsSource(ctx, sqlcgen.SetJobStepsSourceParams{StepsSource: source, ID: jobID})
}
