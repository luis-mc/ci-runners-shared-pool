package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file is the reader half of the Jobs section store (jobsdata.Reader). The filtered lists and the groupings
// are built here; each grouping is one query plus a Go pass for the percentiles, which SQLite does not have.

const (
	defaultPageSize = 50
	// groupWindow is how far back finished jobs count in a grouped table.
	groupWindow = 90 * 24 * time.Hour
	trendLen    = 20

	runCols = `repository_id, run_id, run_attempt, repository, workflow, workflow_name, event, head_branch, head_sha,
		pull_request, task_id, worktree, html_url, created_at, started_at, completed_at, conclusion, updated_at`

	// runSortKey orders runs by created_at (seconds), then run id, then attempt, in one integer: 32 bits of
	// seconds, 24 bits of run id and 7 bits of attempt. The same value is the paging key.
	runSortKey = `((CAST(COALESCE(strftime('%s', r.created_at), 0) AS INTEGER) << 31) | ((r.run_id & 16777215) << 7) | (r.run_attempt & 127))`
)

// runSortKeyOf is the paging key of a run as ListRuns computes it.
func runSortKeyOf(created time.Time, runID int64, attempt int) int64 {
	var secs int64
	if !created.IsZero() {
		secs = created.Unix()
	}
	return secs<<31 | (runID&16777215)<<7 | int64(attempt&127)
}

type scanner interface{ Scan(...any) error }

func scanRunRow(sc scanner, extra ...any) (sqlcgen.Run, error) {
	var r sqlcgen.Run
	dst := []any{&r.RepositoryID, &r.RunID, &r.RunAttempt, &r.Repository, &r.Workflow, &r.WorkflowName, &r.Event,
		&r.HeadBranch, &r.HeadSha, &r.PullRequest, &r.TaskID, &r.Worktree, &r.HtmlUrl, &r.CreatedAt, &r.StartedAt,
		&r.CompletedAt, &r.Conclusion, &r.UpdatedAt}
	err := sc.Scan(append(dst, extra...)...)
	return r, err
}

func scanJobRow(sc scanner) (sqlcgen.Job, error) {
	var r sqlcgen.Job
	err := sc.Scan(&r.ID, &r.RepositoryID, &r.Repository, &r.RunID, &r.RunAttempt, &r.Workflow, &r.Name, &r.JobType,
		&r.Matrix, &r.JobIdent, &r.Branch, &r.HeadSha, &r.Event, &r.Labels, &r.State, &r.Result, &r.HtmlUrl, &r.CreatedAt,
		&r.QueuedAt, &r.SlotAt, &r.StartedAt, &r.CompletedAt, &r.CleanupMs, &r.MachineID, &r.MachineName, &r.RunnerID,
		&r.RunnerName, &r.RunnerKind, &r.Backend, &r.Isolation, &r.Cache, &r.WaitReason, &r.WaitText, &r.Worktree,
		&r.Light, &r.FcLoMs, &r.FcMidMs, &r.FcHiMs, &r.FcBasis, &r.FcSamples, &r.UpdatedAt,
		&r.RunnerOrigin, &r.RunnerGroup, &r.RunnerLabels, &r.StepsSource, &r.CheckedAt) // CIX8 columns (migration 00030)
	return r, err
}

func pageSize(n int) int {
	if n <= 0 {
		return defaultPageSize
	}
	return n
}

// runState is a run's state: done once it has a conclusion, running when a job runs or some are done.
func runState(conclusion string, running, done int) jobsdata.State {
	switch {
	case conclusion != "":
		return jobsdata.Done
	case running > 0 || done > 0:
		return jobsdata.Running
	}
	return jobsdata.Queued
}

// ListRuns returns up to Limit+1 attempts, newest first, and the total of the filter.
func (s *JobsStore) ListRuns(ctx context.Context, f jobsdata.RunFilter) ([]jobsdata.RunRow, int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var conds []string
	var args []any
	add := func(c string, a ...any) { conds = append(conds, c); args = append(args, a...) }
	if f.Result != "" {
		add("r.conclusion = ?", string(f.Result))
	}
	if f.RepositoryID != 0 {
		add("r.repository_id = ?", f.RepositoryID)
	}
	if f.Branch != "" {
		add("r.head_branch = ?", f.Branch)
	}
	if !f.From.IsZero() {
		add("r.created_at >= ?", fmtTime(f.From))
	}
	if !f.To.IsZero() {
		add("r.created_at <= ?", fmtTime(f.To))
	}
	// The state of a run is its conclusion, else running when a job runs or is done, else queued. The state filter
	// asks the jobs index for existence instead of counting every run's jobs.
	const started = `EXISTS (SELECT 1 FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state IN ('running', 'done'))`
	switch f.State {
	case jobsdata.Done:
		add("r.conclusion <> ''")
	case jobsdata.Running:
		add("r.conclusion = '' AND " + started)
	case jobsdata.Queued:
		add("r.conclusion = '' AND NOT " + started)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.rdb.QueryRowContext(ctx, `SELECT count(*) FROM runs r`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count runs: %w", err)
	}
	pageWhere := where
	if f.After != 0 {
		if pageWhere == "" {
			pageWhere = " WHERE "
		} else {
			pageWhere += " AND "
		}
		pageWhere += runSortKey + " < ?"
		args = append(args, f.After)
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+runCols+`, `+runSortKey+` AS sort_key FROM runs r`+pageWhere+
		` ORDER BY `+runSortKey+` DESC LIMIT ?`, append(args, pageSize(f.Limit)+1)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsdata.RunRow
	for rows.Next() {
		var key int64
		r, err := scanRunRow(rows, &key)
		if err != nil {
			return nil, 0, fmt.Errorf("scan run: %w", err)
		}
		run := runFromRow(r)
		out = append(out, jobsdata.RunRow{Run: run, TotalMS: totalMS(run), SortKey: key})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	_ = rows.Close()
	if err := s.fillRunCounts(ctx, out); err != nil {
		return nil, 0, err
	}
	if err := s.fillFollowUps(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

type runKey struct {
	repo, run int64
	attempt   int
}

type runCounts struct{ total, done, failed, running int }

// fillRunCounts sets the job counts and the state of the page rows with one grouped query per 200 runs.
func (s *JobsStore) fillRunCounts(ctx context.Context, rows []jobsdata.RunRow) error {
	got := make(map[runKey]runCounts, len(rows))
	const chunk = 200
	for start := 0; start < len(rows); start += chunk {
		if err := s.countRunJobs(ctx, rows[start:min(start+chunk, len(rows))], got); err != nil {
			return err
		}
	}
	for i := range rows {
		r := &rows[i]
		c := got[runKey{r.RepositoryID, r.RunID, r.Attempt}]
		r.JobsTotal, r.JobsDone, r.JobsFailed = c.total, c.done, c.failed
		r.State = runState(string(r.Conclusion), c.running, c.done)
	}
	return nil
}

func (s *JobsStore) countRunJobs(ctx context.Context, part []jobsdata.RunRow, got map[runKey]runCounts) error {
	args := make([]any, 0, len(part)*3)
	marks := make([]string, len(part))
	for i, r := range part {
		marks[i] = "(?, ?, ?)"
		args = append(args, r.RepositoryID, r.RunID, r.Attempt)
	}
	q, err := s.rdb.QueryContext(ctx, `SELECT j.repository_id, j.run_id, j.run_attempt, count(*),
		COALESCE(sum(j.state = 'done'), 0),
		COALESCE(sum(j.state = 'done' AND j.result IN ('failure', 'timed_out', 'lost')), 0),
		COALESCE(sum(j.state = 'running'), 0)
		FROM (VALUES `+strings.Join(marks, ", ")+`) v
		JOIN jobs j ON j.repository_id = v.column1 AND j.run_id = v.column2 AND j.run_attempt = v.column3
		GROUP BY j.repository_id, j.run_id, j.run_attempt`, args...)
	if err != nil {
		return fmt.Errorf("count run jobs: %w", err)
	}
	defer func() { _ = q.Close() }()
	for q.Next() {
		var k runKey
		var c runCounts
		if err := q.Scan(&k.repo, &k.run, &k.attempt, &c.total, &c.done, &c.failed, &c.running); err != nil {
			return fmt.Errorf("scan run jobs: %w", err)
		}
		got[k] = c
	}
	if err := q.Err(); err != nil {
		return fmt.Errorf("count run jobs: %w", err)
	}
	return nil
}

func totalMS(r jobsdata.Run) int64 {
	if r.CreatedAt.IsZero() || r.CompletedAt.IsZero() || r.CompletedAt.Before(r.CreatedAt) {
		return 0
	}
	return r.CompletedAt.Sub(r.CreatedAt).Milliseconds()
}

// GetRun returns one attempt of a run with its jobs; attempt 0 is the latest.
func (s *JobsStore) GetRun(ctx context.Context, runID int64, attempt int) (jobsdata.RunDetail, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	query := `SELECT ` + runCols + ` FROM runs WHERE run_id = ?`
	args := []any{runID}
	if attempt > 0 {
		query += ` AND run_attempt = ?`
		args = append(args, attempt)
	}
	r, err := scanRunRow(s.rdb.QueryRowContext(ctx, query+` ORDER BY run_attempt DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return jobsdata.RunDetail{}, false, nil
	}
	if err != nil {
		return jobsdata.RunDetail{}, false, fmt.Errorf("get run: %w", err)
	}
	run := runFromRow(r)
	d := jobsdata.RunDetail{RunRow: jobsdata.RunRow{Run: run, TotalMS: totalMS(run), SortKey: runSortKeyOf(run.CreatedAt, run.RunID, run.Attempt)}}
	attempts, err := s.q.ListRunAttempts(ctx, runID)
	if err != nil {
		return d, false, fmt.Errorf("run attempts: %w", err)
	}
	for _, a := range attempts {
		d.Attempts = append(d.Attempts, int(a))
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT * FROM jobs WHERE repository_id = ? AND run_id = ? AND run_attempt = ?
		ORDER BY COALESCE(NULLIF(started_at, ''), created_at), id`, r.RepositoryID, r.RunID, r.RunAttempt)
	if err != nil {
		return d, false, fmt.Errorf("run jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	running := 0
	for rows.Next() {
		jr, err := scanJobRow(rows)
		if err != nil {
			return d, false, fmt.Errorf("scan job: %w", err)
		}
		j := jobFromRow(jr)
		d.Jobs = append(d.Jobs, j)
		d.JobsTotal++
		switch j.State {
		case jobsdata.Done:
			d.JobsDone++
			if isFailed(string(j.Result)) {
				d.JobsFailed++
			}
		case jobsdata.Running:
			running++
		}
	}
	if err := rows.Err(); err != nil {
		return d, false, fmt.Errorf("run jobs: %w", err)
	}
	d.State = runState(r.Conclusion, running, d.JobsDone)
	one := []jobsdata.RunRow{d.RunRow}
	if err := s.fillFollowUps(ctx, one); err != nil {
		return d, false, err
	}
	d.StartedNext, d.FollowUps, d.FollowUpsOpen, d.ParentRunID = one[0].StartedNext, one[0].FollowUps, one[0].FollowUpsOpen, one[0].ParentRunID
	if run.Workflow != "" {
		g, ok, err := s.GetGraph(ctx, run.RepositoryID, run.Workflow, run.HeadSHA)
		if err != nil {
			return d, false, err
		}
		if ok {
			d.Graph = g
		}
	}
	return d, true, nil
}

// jobWhere builds the conditions of a JobFilter (not After and Limit).
func jobWhere(f jobsdata.JobFilter) (string, []any) {
	var conds []string
	var args []any
	add := func(c string, a any) { conds = append(conds, c); args = append(args, a) }
	str := func(col, v string) {
		if v != "" {
			add(col+" = ?", v)
		}
	}
	str("state", string(f.State))
	str("result", string(f.Result))
	if f.RepositoryID != 0 {
		add("repository_id = ?", f.RepositoryID)
	}
	str("machine_id", f.MachineID)
	str("runner_name", f.RunnerName)
	str("runner_kind", string(f.RunnerKind))
	str("backend", f.Backend)
	str("job_type", f.JobType)
	str("job_ident", f.JobKey)
	str("branch", f.Branch)
	if f.RunID != 0 {
		add("run_id = ?", f.RunID)
	}
	if !f.From.IsZero() {
		add("created_at >= ?", fmtTime(f.From))
	}
	if !f.To.IsZero() {
		add("created_at <= ?", fmtTime(f.To))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListJobs returns up to Limit+1 jobs newest first (by job id) and the total of the filter.
func (s *JobsStore) ListJobs(ctx context.Context, f jobsdata.JobFilter) ([]jobsdata.Job, int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	where, args := jobWhere(f)
	var total int
	if !f.NoCount {
		if err := s.rdb.QueryRowContext(ctx, `SELECT count(*) FROM jobs`+where, args...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count jobs: %w", err)
		}
	}
	if f.After != 0 {
		if where == "" {
			where = " WHERE id < ?"
		} else {
			where += " AND id < ?"
		}
		args = append(args, f.After)
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+jobListCols+` FROM jobs`+where+` ORDER BY id DESC LIMIT ?`, append(args, pageSize(f.Limit)+1)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	out, err := collectJobs(rows, scanJobListRow)
	return out, total, err
}

// jobListCols are the columns of a job as the list shows it; the columns jobFromRow does not read are left out.
const jobListCols = `id, repository_id, repository, run_id, run_attempt, workflow, name, job_type, matrix, job_ident, branch,
	head_sha, event, labels, state, result, html_url, created_at, queued_at, slot_at, started_at, completed_at, cleanup_ms,
	machine_id, machine_name, runner_id, runner_name, runner_kind, backend, isolation, cache, wait_reason, wait_text,
	worktree, light, fc_lo_ms, fc_mid_ms, fc_hi_ms, fc_basis, fc_samples`

func scanJobListRow(sc scanner) (sqlcgen.Job, error) {
	var r sqlcgen.Job
	err := sc.Scan(&r.ID, &r.RepositoryID, &r.Repository, &r.RunID, &r.RunAttempt, &r.Workflow, &r.Name, &r.JobType,
		&r.Matrix, &r.JobIdent, &r.Branch, &r.HeadSha, &r.Event, &r.Labels, &r.State, &r.Result, &r.HtmlUrl, &r.CreatedAt,
		&r.QueuedAt, &r.SlotAt, &r.StartedAt, &r.CompletedAt, &r.CleanupMs, &r.MachineID, &r.MachineName, &r.RunnerID,
		&r.RunnerName, &r.RunnerKind, &r.Backend, &r.Isolation, &r.Cache, &r.WaitReason, &r.WaitText, &r.Worktree,
		&r.Light, &r.FcLoMs, &r.FcMidMs, &r.FcHiMs, &r.FcBasis, &r.FcSamples)
	return r, err
}

func collectJobs(rows *sql.Rows, scan func(scanner) (sqlcgen.Job, error)) ([]jobsdata.Job, error) {
	defer func() { _ = rows.Close() }()
	var out []jobsdata.Job
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, jobFromRow(r))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return out, nil
}

// GetJob returns a job with its steps and attempts; Needs is left to the API (it comes from the graph).
func (s *JobsStore) GetJob(ctx context.Context, id int64) (jobsdata.JobDetail, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := s.q.GetJobsRow(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobsdata.JobDetail{}, false, nil
	}
	if err != nil {
		return jobsdata.JobDetail{}, false, fmt.Errorf("get job: %w", err)
	}
	d := jobsdata.JobDetail{Job: jobFromRow(row)}
	steps, err := s.q.ListJobSteps(ctx, id)
	if err != nil {
		return d, false, fmt.Errorf("job steps: %w", err)
	}
	for _, st := range steps {
		d.Steps = append(d.Steps, jobsdata.Step{
			JobID: id, Number: int(st.Number), Name: st.Name, State: st.State, Result: st.Result,
			StartedAt: parseTimeOpt(st.StartedAt), CompletedAt: parseTimeOpt(st.CompletedAt),
		})
	}
	atts, err := s.q.ListJobAttempts(ctx, sqlcgen.ListJobAttemptsParams{RepositoryID: row.RepositoryID, RunID: row.RunID, Name: row.Name})
	if err != nil {
		return d, false, fmt.Errorf("job attempts: %w", err)
	}
	for _, a := range atts {
		d.Attempts = append(d.Attempts, jobsdata.JobAttempt{Attempt: int(a.RunAttempt), JobID: a.ID, State: jobsdata.State(a.State), Result: jobsdata.Result(a.Result)})
	}
	return d, true, nil
}

// History is the last n finished runs of a job key, oldest first.
func (s *JobsStore) History(ctx context.Context, repositoryID int64, jobKey string, n int) ([]jobsdata.HistoryPoint, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.rdb.QueryContext(ctx, `SELECT started_at, completed_at, result, branch, COALESCE(NULLIF(machine_name, ''), machine_id)
		FROM jobs WHERE repository_id = ? AND job_ident = ? AND state = 'done' AND completed_at <> ''
		ORDER BY completed_at DESC, id DESC LIMIT ?`, repositoryID, jobKey, n)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsdata.HistoryPoint
	for rows.Next() {
		var started, completed, result, branch, machine string
		if err := rows.Scan(&started, &completed, &result, &branch, &machine); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		sec, _ := seconds(started, completed)
		out = append(out, jobsdata.HistoryPoint{At: parseTime(completed), Seconds: sec, Result: jobsdata.Result(result), Branch: branch, Machine: machine})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Samples returns finished successful durations, newest first, for the forecast.
func (s *JobsStore) Samples(ctx context.Context, q jobsdata.SampleQuery) ([]jobsdata.Sample, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	conds := []string{"j.state = 'done'", "j.result = 'success'", "j.started_at <> ''", "j.completed_at <> ''"}
	var args []any
	add := func(c string, a ...any) { conds = append(conds, c); args = append(args, a...) }
	if q.RepositoryID != 0 {
		add("j.repository_id = ?", q.RepositoryID)
	}
	switch {
	case q.JobKey != "":
		add("j.job_ident = ?", q.JobKey)
	case q.Workflow != "" || q.JobType != "":
		if q.Workflow != "" {
			add("j.workflow = ?", q.Workflow)
		}
		if q.JobType != "" {
			add("j.job_type = ?", q.JobType)
		}
	}
	if !q.Since.IsZero() {
		add("j.completed_at >= ?", fmtTime(q.Since))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	var query string
	if q.StepName != "" {
		add("st.name = ?", q.StepName)
		add("st.result = 'success'")
		add("st.started_at <> ''")
		add("st.completed_at <> ''")
		query = `SELECT j.id, j.job_ident, j.workflow, st.completed_at, st.started_at, j.machine_id FROM job_steps st
			JOIN jobs j ON j.id = st.job_id WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY st.completed_at DESC, j.id DESC LIMIT ?`
	} else {
		query = `SELECT j.id, j.job_ident, j.workflow, j.completed_at, j.started_at, j.machine_id FROM ` + samplesFrom(q) + `
			WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY j.completed_at DESC, j.id DESC LIMIT ?`
	}
	rows, err := s.rdb.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsdata.Sample
	for rows.Next() {
		var smp jobsdata.Sample
		var completed, started string
		if err := rows.Scan(&smp.JobID, &smp.JobKey, &smp.Workflow, &completed, &started, &smp.MachineID); err != nil {
			return nil, fmt.Errorf("samples scan: %w", err)
		}
		sec, ok := seconds(started, completed)
		if !ok {
			continue
		}
		smp.At, smp.Seconds, smp.StepName = parseTime(completed), sec, q.StepName
		out = append(out, smp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	return out, nil
}

// OpenJobs lists every queued and running job.
func (s *JobsStore) OpenJobs(ctx context.Context) ([]jobsdata.Job, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.rdb.QueryContext(ctx, `SELECT * FROM jobs WHERE state IN ('queued', 'running') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("open jobs: %w", err)
	}
	return collectJobs(rows, scanJobRow)
}

type groupAcc struct {
	name                  string
	filter                string
	running, queued, done int
	failed                int
	wait, run, trend      []float64
	light                 bool
}

// groupKeys names the columns a grouping is made of.
func groupKeys(by jobsdata.GroupBy) (k1, k2 string, err error) {
	switch by {
	case jobsdata.ByRepository:
		return "repository", "''", nil
	case jobsdata.ByMachine:
		return "machine_id", "''", nil
	case jobsdata.ByRunner:
		return "runner_name", "''", nil
	case jobsdata.ByRunnerType:
		return "runner_kind", "backend", nil
	case jobsdata.ByJobType:
		return "job_type", "''", nil
	}
	return "", "", fmt.Errorf("groups: unknown grouping %q", by)
}

// flagFailed and flagLight are the facts of a finished job that the grouped reads take as one integer; flagForecast
// marks a successful job with a forecast (the stats points).
const (
	flagFailed   = 1
	flagLight    = 2
	flagForecast = 4
)

// Groups is the grouped table of one Jobs sub-section (audit R3-2). The open jobs are counted by the engine; the
// finished jobs of the window are read in finish order as a key, one flag integer and three times, with no sort, and the
// times are cut apart without time.Parse. A machine's name is one lookup per machine, not a column of every row.
func (s *JobsStore) groups(ctx context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	k1, k2, err := groupKeys(by)
	if err != nil {
		return nil, err
	}
	groups := map[string]*groupAcc{}
	group := func(key1, key2 string) *groupAcc {
		id := key1 + "\x00" + key2
		g := groups[id]
		if g == nil {
			g = &groupAcc{name: key1, filter: by2filter(by, key1, key2)}
			if by == jobsdata.ByRunnerType && key2 != "" {
				g.name = key1 + "/" + key2
			}
			groups[id] = g
		}
		return g
	}
	open, err := s.rdb.QueryContext(ctx, `SELECT `+k1+`, `+k2+`, state, count(*), COALESCE(MAX(light), 0) FROM jobs
		WHERE state IN ('queued', 'running') AND `+k1+` <> '' GROUP BY `+k1+`, `+k2+`, state`)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	for open.Next() {
		var key1, key2, state string
		var n, light int64
		if err := open.Scan(&key1, &key2, &state, &n, &light); err != nil {
			_ = open.Close()
			return nil, fmt.Errorf("groups scan: %w", err)
		}
		g := group(key1, key2)
		if jobsdata.State(state) == jobsdata.Queued {
			g.queued += int(n)
		} else {
			g.running += int(n)
		}
		g.light = g.light || light != 0
	}
	err = open.Err()
	_ = open.Close()
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+k1+`, `+k2+`, (result IN ('failure', 'timed_out', 'lost')) + 2 * (light <> 0), created_at, started_at, completed_at
		FROM jobs WHERE state = 'done' AND `+k1+` <> '' AND completed_at >= ? ORDER BY completed_at, id`, fmtTime(time.Now().Add(-groupWindow)))
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var last *groupAcc
	var lastK1, lastK2 string
	for rows.Next() {
		var key1, key2, created, started, completed string
		var flags int64
		if err := rows.Scan(&key1, &key2, &flags, &created, &started, &completed); err != nil {
			return nil, fmt.Errorf("groups scan: %w", err)
		}
		if last == nil || key1 != lastK1 || key2 != lastK2 {
			last, lastK1, lastK2 = group(key1, key2), key1, key2
		}
		g := last
		g.done++
		if flags&flagFailed != 0 {
			g.failed++
		}
		g.light = g.light || flags&flagLight != 0
		if w, ok := secondsBetween(created, started); ok {
			g.wait = append(g.wait, w)
		}
		if r, ok := secondsBetween(started, completed); ok {
			g.run = append(g.run, r)
			g.trend = append(g.trend, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	if by == jobsdata.ByMachine {
		if err := s.machineNames(ctx, groups); err != nil {
			return nil, err
		}
	}
	out := make([]jobsdata.GroupRow, 0, len(groups))
	for _, g := range groups {
		row := jobsdata.GroupRow{Name: g.name, Filter: g.filter, Running: g.running, Queued: g.queued, Done: g.done, Failed: g.failed}
		row.WaitP50, row.WaitP90 = pct(g.wait)
		row.RunP50, row.RunP90 = pct(g.run)
		if len(g.trend) > trendLen {
			g.trend = g.trend[len(g.trend)-trendLen:]
		}
		row.Trend = g.trend
		if by == jobsdata.ByJobType {
			l := g.light
			row.Light = &l
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Filter < out[j].Filter
	})
	return out, nil
}

// machineNames gives each machine group the newest name a job of the machine carried, one indexed lookup per group.
func (s *JobsStore) machineNames(ctx context.Context, groups map[string]*groupAcc) error {
	for id, g := range groups {
		machineID, _, _ := strings.Cut(id, "\x00")
		var name string
		err := s.rdb.QueryRowContext(ctx, `SELECT machine_name FROM jobs WHERE machine_id = ? AND machine_name <> ''
			ORDER BY completed_at DESC, id DESC LIMIT 1`, machineID).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("groups machine name: %w", err)
		}
		g.name = name
	}
	return nil
}

// by2filter is the query that lists a group's jobs: "repository=owner/name", "runner_kind=persistent&backend=docker".
func by2filter(by jobsdata.GroupBy, k1, k2 string) string {
	switch by {
	case jobsdata.ByRepository:
		return "repository=" + k1
	case jobsdata.ByMachine:
		return "machine=" + k1
	case jobsdata.ByRunner:
		return "runner=" + k1
	case jobsdata.ByRunnerType:
		if k2 != "" {
			return "runner_kind=" + k1 + "&backend=" + k2
		}
		return "runner_kind=" + k1
	}
	return "job_type=" + k1
}

// Stats is the chart data of the last days days (UTC, today included): daily counts and percentiles, the hour
// grid and the forecast points of finished jobs.
func (s *JobsStore) stats(ctx context.Context, days int, f jobsdata.JobFilter) (jobsdata.Stats, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if days <= 0 {
		days = 30
	}
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	var st jobsdata.Stats
	type dayAcc struct {
		jobs, failed int
		wait, run    []float64
	}
	acc := make([]dayAcc, days)

	df := f
	df.State = jobsdata.Done
	where, args := jobWhere(df)
	where += " AND completed_at >= ?"
	// Per finished job: one flag integer, three times, and the forecast of the ones that have one (audit R3-2).
	rows, err := s.rdb.QueryContext(ctx, `SELECT (result IN ('failure', 'timed_out', 'lost')) + 4 * (result = 'success' AND fc_mid_ms > 0),
		created_at, started_at, completed_at, id, fc_lo_ms, fc_mid_ms, fc_hi_ms, CASE WHEN fc_mid_ms > 0 THEN fc_basis ELSE '' END
		FROM jobs`+where+` ORDER BY completed_at, id`, append(args, fmtTime(first))...)
	if err != nil {
		return st, fmt.Errorf("stats days: %w", err)
	}
	firstMS := first.UnixMilli()
	for rows.Next() {
		var created, started, completed, basis string
		var flags, id, lo, mid, hi int64
		if err := rows.Scan(&flags, &created, &started, &completed, &id, &lo, &mid, &hi, &basis); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("stats scan: %w", err)
		}
		doneMS, ok := unixMillisAny(completed)
		if !ok || doneMS < firstMS {
			continue
		}
		i := int((doneMS - firstMS) / dayMS)
		if i >= days {
			continue
		}
		a := &acc[i]
		a.jobs++
		if flags&flagFailed != 0 {
			a.failed++
		}
		if w, ok := secondsBetween(created, started); ok {
			a.wait = append(a.wait, w)
		}
		run, ok := secondsBetween(started, completed)
		if ok {
			a.run = append(a.run, run)
		}
		if ok && flags&flagForecast != 0 {
			st.ForecastPoints = append(st.ForecastPoints, jobsdata.ForecastPoint{
				JobID: id, ForecastS: float64(mid) / 1000, ActualS: run, LoS: float64(lo) / 1000, HiS: float64(hi) / 1000, Basis: basis,
			})
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return st, fmt.Errorf("stats days: %w", err)
	}
	for i := range acc {
		d := jobsdata.Day{Day: first.AddDate(0, 0, i).Format("2006-01-02"), Jobs: acc[i].jobs, Failed: acc[i].failed}
		d.WaitP50, d.WaitP90 = pct(acc[i].wait)
		d.RunP50, d.RunP90 = pct(acc[i].run)
		st.Days = append(st.Days, d)
	}

	// The hour grid is a count per weekday and hour of the creation time: the engine groups it, on the created_at index.
	where, args = jobWhere(f)
	if where == "" {
		where = " WHERE created_at >= ?"
	} else {
		where += " AND created_at >= ?"
	}
	rows, err = s.rdb.QueryContext(ctx, `SELECT CAST(strftime('%w', created_at) AS INTEGER), CAST(strftime('%H', created_at) AS INTEGER), count(*)
		FROM jobs`+where+` GROUP BY 1, 2 ORDER BY 1, 2`, append(args, fmtTime(first))...)
	if err != nil {
		return st, fmt.Errorf("stats hours: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h jobsdata.HourCount
		if err := rows.Scan(&h.Weekday, &h.Hour, &h.Count); err != nil {
			return st, fmt.Errorf("stats scan: %w", err)
		}
		st.Hours = append(st.Hours, h)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("stats hours: %w", err)
	}
	return st, nil
}

// Summaries lists the daily summaries of the days from to to (UTC days, both included).
func (s *JobsStore) Summaries(ctx context.Context, from, to time.Time) ([]jobsdata.DailySummary, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListDailySummaries(ctx, sqlcgen.ListDailySummariesParams{
		Day: from.UTC().Format("2006-01-02"), Day_2: to.UTC().Format("2006-01-02"),
	})
	if err != nil {
		return nil, fmt.Errorf("summaries: %w", err)
	}
	out := make([]jobsdata.DailySummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, jobsdata.DailySummary{
			Day: r.Day, Repository: r.Repository, JobType: r.JobType, Jobs: int(r.Jobs), Failed: int(r.Failed),
			WaitP50MS: r.WaitP50Ms, WaitP90MS: r.WaitP90Ms, RunP50MS: r.RunP50Ms, RunP90MS: r.RunP90Ms,
		})
	}
	return out, nil
}
