package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// This file is the writer half of the Jobs section store (jobsdata.Writer): runs, jobs, steps, facts, graphs
// and pruning. The reader half is in jobsdata_read.go. Times are UTC RFC3339 text, "" when unknown.

// JobsStore is the Jobs section store. It is a wrapper because *Store already has ListJobs and GetJob for the
// scheduler (slots.Store) with other types; the wrapper's methods shadow them.
type JobsStore struct{ *Store }

var _ jobsdata.Store = (*JobsStore)(nil)

// Jobs returns the Jobs section view of the store.
func (s *Store) Jobs() *JobsStore { return &JobsStore{s} }

// pendingFactsMaxAge is how long facts for a job that never arrived stay parked.
const pendingFactsMaxAge = 24 * time.Hour

func fmtTimeOpt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmtTime(t)
}

func parseTimeOpt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	return parseTime(s)
}

func stateRank(s string) int {
	switch jobsdata.State(s) {
	case jobsdata.Done:
		return 2
	case jobsdata.Running:
		return 1
	}
	return 0
}

// fillEmpty copies a field of old into dst where dst's is the zero value (strings and integers).
func fillEmpty[T any](dst *T, old T) {
	d, o := reflect.ValueOf(dst).Elem(), reflect.ValueOf(old)
	for i := 0; i < d.NumField(); i++ {
		if d.Field(i).IsZero() {
			d.Field(i).Set(o.Field(i))
		}
	}
}

func jsonList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseList(s string) []string {
	var v []string
	if s == "" || json.Unmarshal([]byte(s), &v) != nil || len(v) == 0 {
		return nil
	}
	return v
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func jobToRow(j jobsdata.Job, now time.Time) sqlcgen.Job {
	r := sqlcgen.Job{
		ID: j.ID, RepositoryID: j.RepositoryID, Repository: j.Repository, RunID: j.RunID, RunAttempt: int64(j.Attempt),
		Workflow: j.Workflow, Name: j.Name, JobType: j.JobType, Matrix: jsonList(j.Matrix), JobIdent: j.JobKey,
		Branch: j.Branch, HeadSha: j.HeadSHA, Event: j.Event, Labels: jsonList(j.Labels), State: string(j.State),
		Result: string(j.Result), HtmlUrl: j.HTMLURL, CreatedAt: fmtTimeOpt(j.CreatedAt), QueuedAt: fmtTimeOpt(j.QueuedAt),
		SlotAt: fmtTimeOpt(j.SlotAt), StartedAt: fmtTimeOpt(j.StartedAt), CompletedAt: fmtTimeOpt(j.CompletedAt),
		CleanupMs: j.CleanupMS, MachineID: j.MachineID, MachineName: j.MachineName, RunnerID: j.RunnerID,
		RunnerName: j.RunnerName, RunnerKind: string(j.RunnerKind), Backend: j.Backend, Isolation: j.Isolation,
		Cache: j.Cache, WaitReason: j.WaitReason, WaitText: j.WaitText, Worktree: j.Worktree, Light: b2i(j.Light),
		UpdatedAt: fmtTime(now),
	}
	if r.State == "" {
		r.State = string(jobsdata.Queued)
	}
	if j.Forecast != nil {
		r.FcLoMs, r.FcMidMs, r.FcHiMs = j.Forecast.LoMS, j.Forecast.MidMS, j.Forecast.HiMS
		r.FcBasis, r.FcSamples = j.Forecast.Basis, int64(j.Forecast.Samples)
	}
	return r
}

func jobFromRow(r sqlcgen.Job) jobsdata.Job {
	j := jobsdata.Job{
		ID: r.ID, RepositoryID: r.RepositoryID, Repository: r.Repository, RunID: r.RunID, Attempt: int(r.RunAttempt),
		Workflow: r.Workflow, Name: r.Name, JobType: r.JobType, Matrix: parseList(r.Matrix), JobKey: r.JobIdent,
		Branch: r.Branch, HeadSHA: r.HeadSha, Event: r.Event, Labels: parseList(r.Labels), State: jobsdata.State(r.State),
		Result: jobsdata.Result(r.Result), HTMLURL: r.HtmlUrl, CreatedAt: parseTimeOpt(r.CreatedAt),
		QueuedAt: parseTimeOpt(r.QueuedAt), SlotAt: parseTimeOpt(r.SlotAt), StartedAt: parseTimeOpt(r.StartedAt),
		CompletedAt: parseTimeOpt(r.CompletedAt), CleanupMS: r.CleanupMs, MachineID: r.MachineID,
		MachineName: r.MachineName, RunnerID: r.RunnerID, RunnerName: r.RunnerName,
		RunnerKind: jobsdata.RunnerKind(r.RunnerKind), Backend: r.Backend, Isolation: r.Isolation, Cache: r.Cache,
		WaitReason: r.WaitReason, WaitText: r.WaitText, Worktree: r.Worktree, Light: r.Light != 0,
	}
	if r.FcMidMs != 0 || r.FcLoMs != 0 || r.FcHiMs != 0 || r.FcBasis != "" {
		j.Forecast = &jobsdata.ForecastRecord{LoMS: r.FcLoMs, MidMS: r.FcMidMs, HiMS: r.FcHiMs, Basis: r.FcBasis, Samples: int(r.FcSamples)}
	}
	return j
}

func runToRow(r jobsdata.Run, now time.Time) sqlcgen.Run {
	return sqlcgen.Run{
		RepositoryID: r.RepositoryID, RunID: r.RunID, RunAttempt: int64(r.Attempt), Repository: r.Repository,
		Workflow: r.Workflow, WorkflowName: r.WorkflowName, Event: r.Event, HeadBranch: r.Branch, HeadSha: r.HeadSHA,
		PullRequest: int64(r.PullRequest), TaskID: r.TaskID, Worktree: r.Worktree, HtmlUrl: r.HTMLURL,
		CreatedAt: fmtTimeOpt(r.CreatedAt), StartedAt: fmtTimeOpt(r.StartedAt), CompletedAt: fmtTimeOpt(r.CompletedAt),
		Conclusion: string(r.Conclusion), UpdatedAt: fmtTime(now),
	}
}

func runFromRow(r sqlcgen.Run) jobsdata.Run {
	return jobsdata.Run{
		RepositoryID: r.RepositoryID, Repository: r.Repository, RunID: r.RunID, Attempt: int(r.RunAttempt),
		Workflow: r.Workflow, WorkflowName: r.WorkflowName, Event: r.Event, Branch: r.HeadBranch, HeadSHA: r.HeadSha,
		PullRequest: int(r.PullRequest), TaskID: r.TaskID, Worktree: r.Worktree, HTMLURL: r.HtmlUrl,
		CreatedAt: parseTimeOpt(r.CreatedAt), StartedAt: parseTimeOpt(r.StartedAt),
		CompletedAt: parseTimeOpt(r.CompletedAt), Conclusion: jobsdata.Result(r.Conclusion),
	}
}

// inTx runs fn in one transaction with a deadline.
func (s *Store) inTx(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries) error) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// UpsertRun inserts or updates an attempt. A field the update leaves empty keeps its stored value, so a finished
// attempt is never reopened.
func (s *JobsStore) UpsertRun(ctx context.Context, r jobsdata.Run) error {
	return s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries) error {
		now := time.Now()
		row := runToRow(r, now)
		old, err := q.GetRunsRow(ctx, sqlcgen.GetRunsRowParams{RepositoryID: r.RepositoryID, RunID: r.RunID, RunAttempt: int64(r.Attempt)})
		switch {
		case err == nil:
			if old.Conclusion != "" {
				row.Conclusion, row.CompletedAt = old.Conclusion, old.CompletedAt
				if row.StartedAt == "" {
					row.StartedAt = old.StartedAt
				}
			}
			fillEmpty(&row, old)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("get run: %w", err)
		}
		if err := q.UpsertRunsRow(ctx, sqlcgen.UpsertRunsRowParams(row)); err != nil {
			return fmt.Errorf("upsert run: %w", err)
		}
		if r.TriggeredBy != 0 {
			if err := q.SetRunFollowUp(ctx, sqlcgen.SetRunFollowUpParams{RepositoryID: r.RepositoryID, RunID: r.RunID,
				ParentRunID: r.TriggeredBy, Source: jobsdata.LinkPayload, UpdatedAt: fmtTime(now)}); err != nil {
				return fmt.Errorf("link run: %w", err)
			}
		}
		return relink(ctx, tx, q, r.RepositoryID, row.HeadSha, now)
	})
}

// UpsertJob inserts or updates a job. The state never moves backward, fields the update leaves empty keep their
// stored value (so crsp facts stay), and facts parked for the job are merged and dropped. Light is kept true once
// stored, because a GitHub update cannot say it.
func (s *JobsStore) UpsertJob(ctx context.Context, j jobsdata.Job) error {
	return s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries) error {
		now := time.Now()
		row := jobToRow(j, now)
		old, err := q.GetJobsRow(ctx, j.ID)
		switch {
		case err == nil:
			if stateRank(old.State) > stateRank(row.State) {
				row.State, row.Result = old.State, old.Result
			}
			fillEmpty(&row, old)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("get job: %w", err)
		}
		merged := jobFromRow(row)
		pending, err := q.GetPendingFacts(ctx, j.ID)
		switch {
		case err == nil:
			var f jobsdata.Facts
			if json.Unmarshal([]byte(pending), &f) == nil {
				applyFacts(&merged, f)
			}
			if err := q.DeletePendingFacts(ctx, j.ID); err != nil {
				return fmt.Errorf("drop pending facts: %w", err)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("get pending facts: %w", err)
		}
		out := jobToRow(merged, now)
		if err := q.UpsertJobsRow(ctx, sqlcgen.UpsertJobsRowParams(out)); err != nil {
			return fmt.Errorf("upsert job: %w", err)
		}
		if out.State == string(jobsdata.Done) {
			// a finished job can finish its run, and with it the parent of a follow-up that arrived first
			return relink(ctx, tx, q, out.RepositoryID, out.HeadSha, now)
		}
		return nil
	})
}

// applyFacts sets the fields of f that carry a value.
func applyFacts(j *jobsdata.Job, f jobsdata.Facts) {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&j.MachineID, f.MachineID)
	set(&j.MachineName, f.MachineName)
	set(&j.RunnerID, f.RunnerID)
	set(&j.RunnerName, f.RunnerName)
	if f.RunnerKind != "" {
		j.RunnerKind = f.RunnerKind
	}
	set(&j.Backend, f.Backend)
	set(&j.Isolation, f.Isolation)
	set(&j.Cache, f.Cache)
	set(&j.WaitReason, f.WaitReason)
	set(&j.WaitText, f.WaitText)
	set(&j.Worktree, f.Worktree)
	if !f.QueuedAt.IsZero() {
		j.QueuedAt = f.QueuedAt
	}
	if !f.SlotAt.IsZero() {
		j.SlotAt = f.SlotAt
	}
	if f.CleanupMS != 0 {
		j.CleanupMS = f.CleanupMS
	}
	if f.Light != nil {
		j.Light = *f.Light
	}
	if f.Lost {
		j.Result = jobsdata.Lost
	}
}

// mergeFacts overlays the set fields of f on base, for facts parked twice.
func mergeFacts(base, f jobsdata.Facts) jobsdata.Facts {
	var j jobsdata.Job
	j.MachineID, j.MachineName, j.RunnerID, j.RunnerName, j.RunnerKind = base.MachineID, base.MachineName, base.RunnerID, base.RunnerName, base.RunnerKind
	j.Backend, j.Isolation, j.Cache, j.WaitReason, j.WaitText = base.Backend, base.Isolation, base.Cache, base.WaitReason, base.WaitText
	j.QueuedAt, j.SlotAt, j.CleanupMS, j.Worktree = base.QueuedAt, base.SlotAt, base.CleanupMS, base.Worktree
	if base.Light != nil {
		l := *base.Light
		j.Light = l
	}
	applyFacts(&j, f)
	out := jobsdata.Facts{
		MachineID: j.MachineID, MachineName: j.MachineName, RunnerID: j.RunnerID, RunnerName: j.RunnerName,
		RunnerKind: j.RunnerKind, Backend: j.Backend, Isolation: j.Isolation, Cache: j.Cache,
		WaitReason: j.WaitReason, WaitText: j.WaitText, QueuedAt: j.QueuedAt, SlotAt: j.SlotAt,
		CleanupMS: j.CleanupMS, Worktree: j.Worktree, Lost: base.Lost || f.Lost,
	}
	switch {
	case f.Light != nil:
		l := *f.Light
		out.Light = &l
	case base.Light != nil:
		l := *base.Light
		out.Light = &l
	}
	return out
}

// ReplaceSteps stores the steps of a job.
func (s *JobsStore) ReplaceSteps(ctx context.Context, jobID int64, steps []jobsdata.Step) error {
	return s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		if err := q.DeleteJobSteps(ctx, jobID); err != nil {
			return fmt.Errorf("delete steps: %w", err)
		}
		for _, st := range steps {
			if err := q.InsertJobStep(ctx, sqlcgen.InsertJobStepParams{
				JobID: jobID, Number: int64(st.Number), Name: st.Name, State: st.State, Result: st.Result,
				StartedAt: fmtTimeOpt(st.StartedAt), CompletedAt: fmtTimeOpt(st.CompletedAt),
			}); err != nil {
				return fmt.Errorf("insert step: %w", err)
			}
		}
		return nil
	})
}

// SetFacts merges crsp's facts into the job; for a job not seen yet they are parked until its upsert.
func (s *JobsStore) SetFacts(ctx context.Context, jobID int64, f jobsdata.Facts) error {
	return s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		now := time.Now()
		old, err := q.GetJobsRow(ctx, jobID)
		if err == nil {
			j := jobFromRow(old)
			applyFacts(&j, f)
			out := jobToRow(j, now)
			if err := q.UpsertJobsRow(ctx, sqlcgen.UpsertJobsRowParams(out)); err != nil {
				return fmt.Errorf("set facts: %w", err)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get job: %w", err)
		}
		merged := f
		raw, err := q.GetPendingFacts(ctx, jobID)
		switch {
		case err == nil:
			var base jobsdata.Facts
			if json.Unmarshal([]byte(raw), &base) == nil {
				merged = mergeFacts(base, f)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("get pending facts: %w", err)
		}
		b, err := json.Marshal(merged)
		if err != nil {
			return fmt.Errorf("encode facts: %w", err)
		}
		if err := q.PutPendingFacts(ctx, sqlcgen.PutPendingFactsParams{JobID: jobID, Json: string(b), UpdatedAt: fmtTime(now)}); err != nil {
			return fmt.Errorf("park facts: %w", err)
		}
		return nil
	})
}

// SetForecast stores the forecast made when the job started.
func (s *JobsStore) SetForecast(ctx context.Context, jobID int64, f jobsdata.ForecastRecord) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := s.q.SetJobForecast(ctx, sqlcgen.SetJobForecastParams{
		FcLoMs: f.LoMS, FcMidMs: f.MidMS, FcHiMs: f.HiMS, FcBasis: f.Basis, FcSamples: int64(f.Samples),
		UpdatedAt: fmtTime(time.Now()), ID: jobID,
	})
	if err != nil {
		return fmt.Errorf("set forecast: %w", err)
	}
	return nil
}

// GetGraph reads the cached workflow graph.
func (s *JobsStore) GetGraph(ctx context.Context, repositoryID int64, workflow, headSHA string) (jobsdata.Graph, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := s.q.GetWorkflowGraph(ctx, sqlcgen.GetWorkflowGraphParams{RepositoryID: repositoryID, Workflow: workflow, HeadSha: headSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return jobsdata.Graph{}, false, nil
	}
	if err != nil {
		return jobsdata.Graph{}, false, fmt.Errorf("get graph: %w", err)
	}
	g := jobsdata.Graph{RepositoryID: repositoryID, Workflow: workflow, HeadSHA: headSHA, OK: row.Ok != 0, FetchedAt: parseTimeOpt(row.FetchedAt)}
	_ = json.Unmarshal([]byte(row.Needs), &g.Needs)
	_ = json.Unmarshal([]byte(row.Dynamic), &g.Dynamic)
	return g, true, nil
}

// PutGraph stores a workflow graph.
func (s *JobsStore) PutGraph(ctx context.Context, g jobsdata.Graph) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	needs, err := json.Marshal(g.Needs)
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}
	dyn, err := json.Marshal(g.Dynamic)
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}
	if g.Needs == nil {
		needs = []byte("{}")
	}
	if g.Dynamic == nil {
		dyn = []byte("{}")
	}
	fetched := g.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now()
	}
	if err := s.q.PutWorkflowGraph(ctx, sqlcgen.PutWorkflowGraphParams{
		RepositoryID: g.RepositoryID, Workflow: g.Workflow, HeadSha: g.HeadSHA, Ok: b2i(g.OK),
		Needs: string(needs), Dynamic: string(dyn), FetchedAt: fmtTime(fetched),
	}); err != nil {
		return fmt.Errorf("put graph: %w", err)
	}
	return nil
}

// percentile is the nearest-rank percentile (p in 0..1) of sorted values; 0 for none.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p*float64(len(sorted))+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func pct(v []float64) (p50, p90 float64) {
	sort.Float64s(v)
	return percentile(v, 0.5), percentile(v, 0.9)
}

type dailyKey struct{ day, repository, jobType string }

type dailyAcc struct {
	jobs, failed int
	wait, run    []float64
}

// isFailed is a result that counts as a failed job.
func isFailed(result string) bool {
	switch jobsdata.Result(result) {
	case jobsdata.Failure, jobsdata.TimedOut, jobsdata.Lost:
		return true
	}
	return false
}

// seconds is the time between two stored times, ok false when either is unknown or the order is wrong.
func seconds(from, to string) (float64, bool) {
	if from == "" || to == "" {
		return 0, false
	}
	d := parseTime(to).Sub(parseTime(from)).Seconds()
	return d, d >= 0
}

// Prune rolls the summaries of days not summarised yet, then deletes what is past Retention.
func (s *JobsStore) Prune(ctx context.Context, now time.Time, r jobsdata.Retention) (jobsdata.Pruned, error) {
	var out jobsdata.Pruned
	err := s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries) error {
		if err := rollupDaily(ctx, tx, q, now); err != nil {
			return err
		}
		jobsCut := fmtTime(now.Add(-r.Jobs))
		var err error
		count := func(dst *int64, n int64, e error) {
			if err == nil {
				if e != nil {
					err = e
					return
				}
				*dst += n
			}
		}
		n, e := q.PruneJobSteps(ctx, fmtTime(now.Add(-r.Steps)))
		count(&out.Steps, n, e)
		n, e = q.PruneJobStepsOfJobs(ctx, jobsCut)
		count(&out.Steps, n, e)
		n, e = q.PruneJobFacts(ctx, sqlcgen.PruneJobFactsParams{CompletedAt: jobsCut, UpdatedAt: fmtTime(now.Add(-pendingFactsMaxAge))})
		_ = n
		if err == nil && e != nil {
			err = e
		}
		n, e = q.PruneJobs(ctx, jobsCut)
		count(&out.Jobs, n, e)
		n, e = q.PruneRuns(ctx, jobsCut)
		count(&out.Runs, n, e)
		n, e = q.PruneRunFollowUps(ctx)
		_ = n
		if err == nil && e != nil {
			err = e
		}
		n, e = q.PruneDailySummaries(ctx, now.Add(-r.Summaries).UTC().Format("2006-01-02"))
		count(&out.Summaries, n, e)
		n, e = q.PruneWorkflowGraphs(ctx, jobsCut)
		count(&out.Graphs, n, e)
		if err != nil {
			return fmt.Errorf("prune: %w", err)
		}
		return nil
	})
	return out, err
}

// rollupDaily writes job_daily rows for every finished day before today that has none yet.
func rollupDaily(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries, now time.Time) error {
	days, err := q.DailySummarisedDays(ctx)
	if err != nil {
		return fmt.Errorf("summarised days: %w", err)
	}
	done := make(map[string]bool, len(days))
	for _, d := range days {
		done[d] = true
	}
	acc, err := readRollup(ctx, tx, now, done)
	if err != nil {
		return err
	}
	for k, a := range acc {
		w50, w90 := pct(a.wait)
		r50, r90 := pct(a.run)
		if err := q.UpsertDailySummary(ctx, sqlcgen.UpsertDailySummaryParams{
			Day: k.day, Repository: k.repository, JobType: k.jobType, Jobs: int64(a.jobs), Failed: int64(a.failed),
			WaitP50Ms: int64(w50 * 1000), WaitP90Ms: int64(w90 * 1000), RunP50Ms: int64(r50 * 1000), RunP90Ms: int64(r90 * 1000),
		}); err != nil {
			return fmt.Errorf("rollup write: %w", err)
		}
	}
	return nil
}

// readRollup reads the finished jobs of days before today and sums them per day, repository and job type,
// leaving out the days already summarised.
func readRollup(ctx context.Context, tx *sql.Tx, now time.Time, skip map[string]bool) (map[dailyKey]*dailyAcc, error) {
	rows, err := tx.QueryContext(ctx, `SELECT repository, job_type, result, created_at, started_at, completed_at
		FROM jobs WHERE state = 'done' AND completed_at <> '' AND substr(completed_at, 1, 10) < ?`, now.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("rollup read: %w", err)
	}
	defer func() { _ = rows.Close() }()
	acc := map[dailyKey]*dailyAcc{}
	for rows.Next() {
		var repo, jt, result, created, started, completed string
		if err := rows.Scan(&repo, &jt, &result, &created, &started, &completed); err != nil {
			return nil, fmt.Errorf("rollup scan: %w", err)
		}
		k := dailyKey{completed[:10], repo, jt}
		if skip[k.day] {
			continue
		}
		a := acc[k]
		if a == nil {
			a = &dailyAcc{}
			acc[k] = a
		}
		a.jobs++
		if isFailed(result) {
			a.failed++
		}
		if w, ok := seconds(created, started); ok {
			a.wait = append(a.wait, w)
		}
		if w, ok := seconds(started, completed); ok {
			a.run = append(a.run, w)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup read: %w", err)
	}
	return acc, nil
}
