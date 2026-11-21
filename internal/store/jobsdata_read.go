package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
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
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	cte := `WITH f AS (SELECT r.*, ` + runSortKey + ` AS sort_key,
			(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt) AS n_total,
			(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'done') AS n_done,
			(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'done' AND j.result IN ('failure', 'timed_out', 'lost')) AS n_failed,
			(SELECT count(*) FROM jobs j WHERE j.repository_id = r.repository_id AND j.run_id = r.run_id AND j.run_attempt = r.run_attempt AND j.state = 'running') AS n_running
			FROM runs r` + where + `),
		g AS (SELECT f.*, CASE WHEN conclusion <> '' THEN 'done' WHEN n_running > 0 OR n_done > 0 THEN 'running' ELSE 'queued' END AS state FROM f),
		h AS (SELECT * FROM g WHERE (? = '' OR state = ?))`
	args = append(args, string(f.State), string(f.State))

	var total int
	if err := s.db.QueryRowContext(ctx, cte+` SELECT count(*) FROM h`, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count runs: %w", err)
	}
	args = append(args, f.After, f.After, pageSize(f.Limit)+1)
	rows, err := s.db.QueryContext(ctx, cte+` SELECT `+runCols+`, sort_key, n_total, n_done, n_failed, n_running, state
		FROM h WHERE (? = 0 OR sort_key < ?) ORDER BY sort_key DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsdata.RunRow
	for rows.Next() {
		var key, nTotal, nDone, nFailed, nRunning int64
		var state string
		r, err := scanRunRow(rows, &key, &nTotal, &nDone, &nFailed, &nRunning, &state)
		if err != nil {
			return nil, 0, fmt.Errorf("scan run: %w", err)
		}
		run := runFromRow(r)
		out = append(out, jobsdata.RunRow{
			Run: run, State: jobsdata.State(state), JobsTotal: int(nTotal), JobsDone: int(nDone), JobsFailed: int(nFailed),
			TotalMS: totalMS(run), SortKey: key,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	if err := s.fillFollowUps(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
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
	r, err := scanRunRow(s.db.QueryRowContext(ctx, query+` ORDER BY run_attempt DESC LIMIT 1`, args...))
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
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM jobs WHERE repository_id = ? AND run_id = ? AND run_attempt = ?
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
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}
	if f.After != 0 {
		if where == "" {
			where = " WHERE id < ?"
		} else {
			where += " AND id < ?"
		}
		args = append(args, f.After)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM jobs`+where+` ORDER BY id DESC LIMIT ?`, append(args, pageSize(f.Limit)+1)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	out, err := collectJobs(rows)
	return out, total, err
}

func collectJobs(rows *sql.Rows) ([]jobsdata.Job, error) {
	defer func() { _ = rows.Close() }()
	var out []jobsdata.Job
	for rows.Next() {
		r, err := scanJobRow(rows)
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
	rows, err := s.db.QueryContext(ctx, `SELECT started_at, completed_at, result, branch, COALESCE(NULLIF(machine_name, ''), machine_id)
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
		query = `SELECT j.id, j.job_ident, j.workflow, j.completed_at, j.started_at, j.machine_id FROM jobs j
			WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY j.completed_at DESC, j.id DESC LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, query, append(args, limit)...)
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
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM jobs WHERE state IN ('queued', 'running') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("open jobs: %w", err)
	}
	return collectJobs(rows)
}

type groupAcc struct {
	name                  string
	filter                string
	running, queued, done int
	failed                int
	wait, run, trend      []float64
	light                 bool
}

// Groups is the grouped table of one Jobs sub-section: one query, then counts and percentiles in Go.
func (s *JobsStore) Groups(ctx context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var k1, k2 string
	switch by {
	case jobsdata.ByRepository:
		k1, k2 = "repository", "''"
	case jobsdata.ByMachine:
		k1, k2 = "machine_id", "''"
	case jobsdata.ByRunner:
		k1, k2 = "runner_name", "''"
	case jobsdata.ByRunnerType:
		k1, k2 = "runner_kind", "backend"
	case jobsdata.ByJobType:
		k1, k2 = "job_type", "''"
	default:
		return nil, fmt.Errorf("groups: unknown grouping %q", by)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+k1+`, `+k2+`, machine_name, state, result, created_at, started_at, completed_at, light
		FROM jobs WHERE `+k1+` <> '' AND (state <> 'done' OR completed_at >= ?) ORDER BY completed_at, id`, fmtTime(time.Now().Add(-groupWindow)))
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := map[string]*groupAcc{}
	for rows.Next() {
		var key1, key2, machineName, state, result, created, started, completed string
		var light int64
		if err := rows.Scan(&key1, &key2, &machineName, &state, &result, &created, &started, &completed, &light); err != nil {
			return nil, fmt.Errorf("groups scan: %w", err)
		}
		id := key1 + "\x00" + key2
		g := groups[id]
		if g == nil {
			g = &groupAcc{name: key1, filter: by2filter(by, key1, key2)}
			if by == jobsdata.ByRunnerType && key2 != "" {
				g.name = key1 + "/" + key2
			}
			groups[id] = g
		}
		if by == jobsdata.ByMachine && machineName != "" {
			g.name = machineName
		}
		g.light = g.light || light != 0
		switch jobsdata.State(state) {
		case jobsdata.Queued:
			g.queued++
		case jobsdata.Running:
			g.running++
		default:
			g.done++
			if isFailed(result) {
				g.failed++
			}
			if w, ok := seconds(created, started); ok {
				g.wait = append(g.wait, w)
			}
			if r, ok := seconds(started, completed); ok {
				g.run = append(g.run, r)
				g.trend = append(g.trend, r)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups: %w", err)
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
func (s *JobsStore) Stats(ctx context.Context, days int, f jobsdata.JobFilter) (jobsdata.Stats, error) {
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, result, created_at, started_at, completed_at, fc_lo_ms, fc_mid_ms, fc_hi_ms, fc_basis
		FROM jobs`+where+` ORDER BY completed_at, id`, append(args, fmtTime(first))...)
	if err != nil {
		return st, fmt.Errorf("stats days: %w", err)
	}
	for rows.Next() {
		var id, lo, mid, hi int64
		var result, created, started, completed, basis string
		if err := rows.Scan(&id, &result, &created, &started, &completed, &lo, &mid, &hi, &basis); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("stats scan: %w", err)
		}
		i := int(parseTime(completed).Sub(first).Hours() / 24)
		if i < 0 || i >= days {
			continue
		}
		a := &acc[i]
		a.jobs++
		if isFailed(result) {
			a.failed++
		}
		if w, ok := seconds(created, started); ok {
			a.wait = append(a.wait, w)
		}
		run, ok := seconds(started, completed)
		if ok {
			a.run = append(a.run, run)
		}
		if ok && mid > 0 && jobsdata.Result(result) == jobsdata.Success {
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

	where, args = jobWhere(f)
	if where == "" {
		where = " WHERE created_at >= ?"
	} else {
		where += " AND created_at >= ?"
	}
	rows, err = s.db.QueryContext(ctx, `SELECT created_at FROM jobs`+where, append(args, fmtTime(first))...)
	if err != nil {
		return st, fmt.Errorf("stats hours: %w", err)
	}
	defer func() { _ = rows.Close() }()
	grid := map[[2]int]int{}
	for rows.Next() {
		var created string
		if err := rows.Scan(&created); err != nil {
			return st, fmt.Errorf("stats scan: %w", err)
		}
		t := parseTime(created)
		grid[[2]int{int(t.Weekday()), t.Hour()}]++
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("stats hours: %w", err)
	}
	for k, n := range grid {
		st.Hours = append(st.Hours, jobsdata.HourCount{Weekday: k[0], Hour: k[1], Count: n})
	}
	sort.Slice(st.Hours, func(i, j int) bool {
		if st.Hours[i].Weekday != st.Hours[j].Weekday {
			return st.Hours[i].Weekday < st.Hours[j].Weekday
		}
		return st.Hours[i].Hour < st.Hours[j].Hour
	})
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
