package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Batched reads for the API lists (CRSP290, audit F1, F2, F11). A list page used to ask the store one question per
// row; these answer the whole page in a few queries, on the read pool.

const (
	// sampleTerms is how many sample queries go into one compound SELECT (SQLite allows 500 terms).
	sampleTerms = 35
	// sampleWorkers is how many chunks of sample questions are read at the same time.
	sampleWorkers = 4
	// runKeyChunk is how many runs go into one query (three bound values each, far below the variable limit).
	runKeyChunk = 200
)

// samplesFrom names the jobs table of a sample read. A question for one repository reads the newest finished jobs of a
// key through jobs_sample; without planner statistics SQLite would take (state, completed_at) once a Since bound is set
// and walk every finished job of the window for one key (audit R3-1).
func samplesFrom(q jobsdata.SampleQuery) string {
	if q.RepositoryID != 0 {
		return "jobs j INDEXED BY jobs_sample"
	}
	return "jobs j"
}

// sampleSQL builds the query of one job-level sample question (no step); the caller wraps it.
func sampleSQL(q jobsdata.SampleQuery) (string, []any) {
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
	return `SELECT j.id, j.job_ident, j.workflow, j.completed_at, j.started_at, j.machine_id FROM ` + samplesFrom(q) + ` WHERE ` +
		strings.Join(conds, " AND ") + ` ORDER BY j.completed_at DESC, j.id DESC LIMIT ?`, append(args, limit)
}

// SamplesMany answers several sample questions in as few queries as it can: the questions without a step are
// joined into compound SELECTs of sampleTerms terms (each term keeps its own ORDER BY and LIMIT, so it reads the
// (repository, key, completed_at) index and stops early); a question with a step is asked on its own. The answers
// come back in the order of qs, each newest first like Samples.
func (s *JobsStore) SamplesMany(ctx context.Context, qs []jobsdata.SampleQuery) ([][]jobsdata.Sample, error) {
	// The newest samples of a key change only when a job is written, so the answers of a page that was just listed are
	// kept for the next one (audit R3-5). A step question is not kept: step rows are written elsewhere. The slices are
	// shared between callers; the forecast reads them and never changes them.
	gen := s.JobsVersion()
	out := make([][]jobsdata.Sample, len(qs))
	var miss []jobsdata.SampleQuery
	var at []int
	for i, q := range qs {
		if q.StepName == "" {
			if got, ok := s.samples.get(sampleCacheKey(q), gen); ok {
				out[i] = got
				continue
			}
		}
		miss = append(miss, q)
		at = append(at, i)
	}
	if len(miss) == 0 {
		return out, nil
	}
	got, err := s.samplesMany(ctx, miss)
	if err != nil {
		return nil, err
	}
	for j, i := range at {
		out[i] = got[j]
		if miss[j].StepName == "" {
			s.samples.put(sampleCacheKey(miss[j]), gen, got[j])
		}
	}
	return out, nil
}

// sampleCacheKey names a sample question without its Since bound, which moves with the clock: an answer a few seconds
// old reaches back a few seconds less, which changes nothing the forecast shows.
func sampleCacheKey(q jobsdata.SampleQuery) string {
	return fmt.Sprintf("%d|%s|%s|%s|%d", q.RepositoryID, q.JobKey, q.Workflow, q.JobType, q.Limit)
}

func (s *JobsStore) samplesMany(ctx context.Context, qs []jobsdata.SampleQuery) ([][]jobsdata.Sample, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	out := make([][]jobsdata.Sample, len(qs))
	var plain []int
	for i, q := range qs {
		if q.StepName != "" {
			got, err := s.Samples(ctx, q)
			if err != nil {
				return nil, err
			}
			out[i] = got
			continue
		}
		plain = append(plain, i)
	}
	// Chunks run side by side on the read pool: each term is a handful of index lookups, so the wall time of a page
	// with many distinct keys falls with the number of chunks. A chunk writes only its own answers.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(sampleWorkers)
	for len(plain) > 0 {
		n := min(len(plain), sampleTerms)
		chunk := plain[:n]
		plain = plain[n:]
		g.Go(func() error { return s.samplesChunk(gctx, qs, chunk, out) })
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// samplesChunk reads the questions at the indexes in chunk with one compound SELECT and stores each answer in out.
func (s *JobsStore) samplesChunk(ctx context.Context, qs []jobsdata.SampleQuery, chunk []int, out [][]jobsdata.Sample) error {
	var parts []string
	var args []any
	for _, i := range chunk {
		inner, a := sampleSQL(qs[i])
		parts = append(parts, fmt.Sprintf(`SELECT %d AS qi, id, job_ident, workflow, completed_at, started_at, machine_id FROM (%s)`, i, inner))
		args = append(args, a...)
	}
	rows, err := s.rdb.QueryContext(ctx, strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return fmt.Errorf("samples many: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var qi int
		var smp jobsdata.Sample
		var completed, started string
		if err := rows.Scan(&qi, &smp.JobID, &smp.JobKey, &smp.Workflow, &completed, &started, &smp.MachineID); err != nil {
			return fmt.Errorf("samples many scan: %w", err)
		}
		sec, ok := seconds(started, completed)
		if !ok {
			continue
		}
		smp.At, smp.Seconds = parseTime(completed), sec
		out[qi] = append(out[qi], smp)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("samples many: %w", err)
	}
	// The compound SELECT does not promise the order of its terms: sort each answer like Samples does.
	for _, i := range chunk {
		got := out[i]
		sort.SliceStable(got, func(a, b int) bool {
			if !got[a].At.Equal(got[b].At) {
				return got[a].At.After(got[b].At)
			}
			return got[a].JobID > got[b].JobID
		})
	}
	return nil
}

// RunsWithJobs gives each run row its jobs (in the order GetRun uses) and its workflow graph, in two queries for any
// number of runs. The rows already carry the counts and follow-ups, so nothing else is read.
func (s *JobsStore) RunsWithJobs(ctx context.Context, rows []jobsdata.RunRow) ([]jobsdata.RunDetail, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	out := make([]jobsdata.RunDetail, len(rows))
	type key struct {
		repo, run int64
		attempt   int
	}
	at := map[key]int{}
	for i, r := range rows {
		out[i].RunRow = r
		at[key{r.RepositoryID, r.RunID, r.Attempt}] = i
	}
	for start := 0; start < len(rows); start += runKeyChunk {
		part := rows[start:min(len(rows), start+runKeyChunk)]
		vals := make([]string, len(part))
		args := make([]any, 0, len(part)*3)
		for i, r := range part {
			vals[i] = "(?,?,?)"
			args = append(args, r.RepositoryID, r.RunID, r.Attempt)
		}
		jr, err := s.rdb.QueryContext(ctx, `SELECT * FROM jobs WHERE (repository_id, run_id, run_attempt) IN (VALUES `+strings.Join(vals, ",")+
			`) ORDER BY COALESCE(NULLIF(started_at, ''), created_at), id`, args...)
		if err != nil {
			return nil, fmt.Errorf("runs jobs: %w", err)
		}
		jobs, err := collectJobs(jr, scanJobRow)
		if err != nil {
			return nil, err
		}
		for _, j := range jobs {
			if i, ok := at[key{j.RepositoryID, j.RunID, j.Attempt}]; ok {
				out[i].Jobs = append(out[i].Jobs, j)
			}
		}
	}
	type gkey struct {
		repo          int64
		workflow, sha string
	}
	want := map[gkey][]int{}
	var gk []gkey
	for i, r := range rows {
		if r.Workflow == "" {
			continue
		}
		k := gkey{r.RepositoryID, r.Workflow, r.HeadSHA}
		if _, seen := want[k]; !seen {
			gk = append(gk, k)
		}
		want[k] = append(want[k], i)
	}
	for start := 0; start < len(gk); start += runKeyChunk {
		part := gk[start:min(len(gk), start+runKeyChunk)]
		vals := make([]string, len(part))
		args := make([]any, 0, len(part)*3)
		for i, k := range part {
			vals[i] = "(?,?,?)"
			args = append(args, k.repo, k.workflow, k.sha)
		}
		gr, err := s.rdb.QueryContext(ctx, `SELECT repository_id, workflow, head_sha, ok, needs, dynamic, fetched_at FROM workflow_graphs
			WHERE (repository_id, workflow, head_sha) IN (VALUES `+strings.Join(vals, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("runs graphs: %w", err)
		}
		err = func() error {
			defer func() { _ = gr.Close() }()
			for gr.Next() {
				var g sqlcgen.WorkflowGraph
				if err := gr.Scan(&g.RepositoryID, &g.Workflow, &g.HeadSha, &g.Ok, &g.Needs, &g.Dynamic, &g.FetchedAt); err != nil {
					return fmt.Errorf("runs graphs scan: %w", err)
				}
				graph := graphOf(g)
				for _, i := range want[gkey{g.RepositoryID, g.Workflow, g.HeadSha}] {
					out[i].Graph = graph
				}
			}
			return gr.Err()
		}()
		if err != nil {
			return nil, fmt.Errorf("runs graphs: %w", err)
		}
	}
	return out, nil
}

// The window reads of finished jobs; each one is a range of the (state, completed_at) index (audit R3-1).
const (
	finishedBetweenSQL = `SELECT ` + jobListCols + ` FROM jobs WHERE completed_at >= ? AND completed_at < ? AND state = 'done'
		ORDER BY completed_at DESC LIMIT ?`
	anyFinishedSQL = `SELECT 1 FROM jobs WHERE completed_at >= ? AND completed_at < ? AND state = 'done'`
)

// FinishedBetween reads the done jobs that completed in [from, to), newest first, on the completed-at index. It reads
// only the window it is asked for, so the timeline costs the same on a database of any age (audit F3).
func (s *JobsStore) FinishedBetween(ctx context.Context, from, to time.Time, limit int) ([]jobsdata.Job, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.rdb.QueryContext(ctx, finishedBetweenSQL, fmtTime(from), fmtTime(to), limit)
	if err != nil {
		return nil, fmt.Errorf("finished jobs: %w", err)
	}
	return collectJobs(rows, scanJobListRow)
}

// AnyFinishedBetween says whether some done job completed in [from, to), of machineID when it is set.
func (s *JobsStore) AnyFinishedBetween(ctx context.Context, from, to time.Time, machineID string) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	q := anyFinishedSQL
	args := []any{fmtTime(from), fmtTime(to)}
	if machineID != "" {
		q += ` AND machine_id = ?`
		args = append(args, machineID)
	}
	var one int
	err := s.rdb.QueryRowContext(ctx, q+` LIMIT 1`, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("older finished jobs: %w", err)
	}
	return true, nil
}
