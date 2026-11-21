package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// Follow-up links (CIX24): a workflow_run run is linked to the run that started it in run_follow_ups. The link is
// written when GitHub names the parent (Run.TriggeredBy) or inferred from the head sha and the completion time.
// It is recomputed after every run or job write of that sha, because the child can arrive before the store knows
// the parent finished.

// aggRow is the latest attempt of a run with its job counts.
type aggRow struct {
	run                                          jobsdata.Run
	nTotal, nDone, nRunning, nFailed, nCancelled int
	jobsCompleted                                time.Time
}

const aggCols = `r.repository_id, r.run_id, r.run_attempt, r.workflow_name, r.workflow, r.event, r.conclusion, r.created_at,
	r.started_at, r.completed_at, r.html_url, r.head_sha,
	(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt),
	(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'done'),
	(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'running'),
	(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'done' AND j.result IN ('failure', 'timed_out', 'lost')),
	(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'done' AND j.result = 'cancelled'),
	COALESCE((SELECT max(j.completed_at) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt), '')`

const latestAttempt = `r.run_attempt = (SELECT max(x.run_attempt) FROM runs x WHERE x.repository_id = r.repository_id AND x.run_id = r.run_id)`

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// scanAggRow reads one aggCols row; lead are extra columns selected before them.
func scanAggRow(rows *sql.Rows, lead ...any) (aggRow, error) {
	var a aggRow
	var created, started, completed, conclusion, jobsDone string
	r := &a.run
	var attempt int64
	dst := append(lead, &r.RepositoryID, &r.RunID, &attempt, &r.WorkflowName, &r.Workflow, &r.Event, &conclusion, &created,
		&started, &completed, &r.HTMLURL, &r.HeadSHA, &a.nTotal, &a.nDone, &a.nRunning, &a.nFailed, &a.nCancelled, &jobsDone)
	if err := rows.Scan(dst...); err != nil {
		return a, fmt.Errorf("scan run: %w", err)
	}
	r.Attempt = int(attempt)
	r.CreatedAt, r.StartedAt, r.CompletedAt = parseTimeOpt(created), parseTimeOpt(started), parseTimeOpt(completed)
	r.Conclusion = jobsdata.Result(conclusion)
	a.jobsCompleted = parseTimeOpt(jobsDone)
	return a, nil
}

func scanAgg(rows *sql.Rows) ([]aggRow, error) {
	defer func() { _ = rows.Close() }()
	var out []aggRow
	for rows.Next() {
		a, err := scanAggRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// finished says whether the run is done: it has a conclusion, or every job it has is done.
func (a aggRow) finished() bool {
	return a.run.Conclusion != "" || (a.nTotal > 0 && a.nDone == a.nTotal)
}

// completedAt is when the run finished, zero while it is open: the run's own completion, else the last job's.
func (a aggRow) completedAt() time.Time {
	switch {
	case a.run.Conclusion != "" && !a.run.CompletedAt.IsZero():
		return a.run.CompletedAt
	case a.finished():
		return a.jobsCompleted
	}
	return time.Time{}
}

func (a aggRow) followUp() jobsdata.FollowUp {
	f := jobsdata.FollowUp{RunID: a.run.RunID, Attempt: a.run.Attempt, WorkflowName: a.run.WorkflowName, State: jobsdata.Queued,
		CreatedAt: a.run.CreatedAt, StartedAt: a.run.StartedAt, CompletedAt: a.completedAt(), HTMLURL: a.run.HTMLURL}
	if f.WorkflowName == "" {
		f.WorkflowName = a.run.Workflow
	}
	switch {
	case a.finished():
		f.State = jobsdata.Done
		switch {
		case a.run.Conclusion != "":
			f.Result = a.run.Conclusion
		case a.nFailed > 0:
			f.Result = jobsdata.Failure
		case a.nCancelled > 0:
			f.Result = jobsdata.Cancelled
		default:
			f.Result = jobsdata.Success
		}
	case a.nRunning > 0 || a.nDone > 0:
		f.State = jobsdata.Running
	}
	return f
}

// relink recomputes the inferred parents of the workflow_run runs of one head sha.
func relink(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries, repoID int64, sha string, now time.Time) error {
	if sha == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+aggCols+` FROM runs r WHERE r.repository_id = ? AND r.head_sha = ? AND `+latestAttempt, repoID, sha)
	if err != nil {
		return fmt.Errorf("link candidates: %w", err)
	}
	runs, err := scanAgg(rows)
	if err != nil {
		return err
	}
	cands := make([]jobsdata.RunCandidate, 0, len(runs))
	for _, a := range runs {
		cands = append(cands, jobsdata.RunCandidate{RunID: a.run.RunID, CreatedAt: a.run.CreatedAt, CompletedAt: a.completedAt()})
	}
	for i, a := range runs {
		if a.run.Event != jobsdata.EventWorkflowRun {
			continue
		}
		old, err := q.GetRunFollowUp(ctx, sqlcgen.GetRunFollowUpParams{RepositoryID: repoID, RunID: a.run.RunID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get link: %w", err)
		}
		if err == nil && old.Source == jobsdata.LinkPayload {
			continue
		}
		parent, ok := jobsdata.InferParent(cands[i], cands)
		if !ok || (err == nil && old.ParentRunID == parent) {
			continue
		}
		if err := q.SetRunFollowUp(ctx, sqlcgen.SetRunFollowUpParams{RepositoryID: repoID, RunID: a.run.RunID, ParentRunID: parent,
			Source: jobsdata.LinkInferred, UpdatedAt: fmtTime(now)}); err != nil {
			return fmt.Errorf("set link: %w", err)
		}
	}
	return nil
}

// followUpsOf returns the follow-ups of the given runs, oldest first, keyed by "repositoryID/runID" of the parent.
func (s *JobsStore) followUpsOf(ctx context.Context, db queryer, parents map[[2]int64]bool) (map[[2]int64][]jobsdata.FollowUp, error) {
	out := map[[2]int64][]jobsdata.FollowUp{}
	if len(parents) == 0 {
		return out, nil
	}
	var vals []string
	var args []any
	for k := range parents {
		vals = append(vals, "(?, ?)")
		args = append(args, k[0], k[1])
	}
	rows, err := db.QueryContext(ctx, `SELECT f.parent_run_id, `+aggCols+` FROM run_follow_ups f
		JOIN runs r ON r.repository_id = f.repository_id AND r.run_id = f.run_id
		WHERE (f.repository_id, f.parent_run_id) IN (VALUES `+strings.Join(vals, ", ")+`) AND `+latestAttempt+`
		ORDER BY r.created_at, r.run_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("follow-ups: %w", err)
	}
	return groupFollowUps(rows)
}

// fillFollowUps sets the follow-up counts and the parent of listed runs.
func (s *JobsStore) fillFollowUps(ctx context.Context, rows []jobsdata.RunRow) error {
	if len(rows) == 0 {
		return nil
	}
	parents := map[[2]int64]bool{}
	for _, r := range rows {
		parents[[2]int64{r.RepositoryID, r.RunID}] = true
	}
	fus, err := s.followUpsOf(ctx, s.db, parents)
	if err != nil {
		return err
	}
	for i := range rows {
		list := fus[[2]int64{rows[i].RepositoryID, rows[i].RunID}]
		rows[i].StartedNext = list
		rows[i].FollowUps = len(list)
		for _, f := range list {
			if f.Open() {
				rows[i].FollowUpsOpen++
			}
		}
		if rows[i].Event == jobsdata.EventWorkflowRun {
			if l, err := s.q.GetRunFollowUp(ctx, sqlcgen.GetRunFollowUpParams{RepositoryID: rows[i].RepositoryID, RunID: rows[i].RunID}); err == nil {
				rows[i].ParentRunID = l.ParentRunID
			}
		}
	}
	return nil
}

// groupFollowUps reads the rows of followUpsOf: parent run id, then aggCols.
func groupFollowUps(rows *sql.Rows) (map[[2]int64][]jobsdata.FollowUp, error) {
	out := map[[2]int64][]jobsdata.FollowUp{}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var parent int64
		a, err := scanAggRow(rows, &parent)
		if err != nil {
			return nil, err
		}
		k := [2]int64{a.run.RepositoryID, parent}
		out[k] = append(out[k], a.followUp())
	}
	return out, rows.Err()
}
