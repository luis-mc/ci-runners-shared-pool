package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// CRSP286 (audit P-1 to P-4): a list page of runs or jobs answers in under 200 ms at 10,000 runs and 100,000 jobs.
const listP95Limit = 200 * time.Millisecond

// seedLists writes 10,000 runs of 10 jobs each straight in SQL.
func seedLists(tb testing.TB, s *Store) {
	tb.Helper()
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 10000)
		INSERT INTO runs (repository_id, run_id, run_attempt, repository, workflow, head_branch, created_at, conclusion)
		SELECT i % 40, i, 1, 'o/r', 'ci.yml', 'b' || (i % 20), strftime('%Y-%m-%dT%H:%M:%SZ', 1700000000 + i * 60, 'unixepoch'),
			CASE WHEN i % 10 < 6 THEN 'success' WHEN i % 10 < 7 THEN 'failure' ELSE '' END FROM n`)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100000)
		INSERT INTO jobs (repository_id, repository, run_id, run_attempt, workflow, name, job_ident, branch, state, result, created_at)
		SELECT (i % 10000 + 1) % 40, 'o/r', i % 10000 + 1, 1, 'ci.yml', 'job' || i, 'k' || i, 'b' || (i % 20),
			CASE WHEN i % 10 < 6 THEN 'done' WHEN i % 10 < 8 THEN 'running' ELSE 'queued' END,
			CASE WHEN i % 10 < 6 THEN 'success' ELSE '' END,
			strftime('%Y-%m-%dT%H:%M:%SZ', 1700000000 + i, 'unixepoch') FROM n`)
	if err != nil {
		tb.Fatal(err)
	}
}

func listStore(tb testing.TB) *JobsStore {
	tb.Helper()
	ctx := context.Background()
	s, err := OpenForTests(ctx, tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	seedLists(tb, s)
	return s.Jobs()
}

func p95(d []time.Duration) time.Duration {
	slices.Sort(d)
	return d[len(d)*95/100]
}

func TestListPagesP95(t *testing.T) {
	if raceOn {
		t.Skip("timings mean nothing under the race detector")
	}
	js := listStore(t)
	ctx := context.Background()
	runPage := func(f jobsdata.RunFilter) func() error {
		return func() error {
			rows, total, err := js.ListRuns(ctx, f)
			if err == nil && (len(rows) != 51 || total == 0) {
				t.Fatalf("runs page: %d rows of %d", len(rows), total)
			}
			return err
		}
	}
	cases := map[string]func() error{
		"runs":         runPage(jobsdata.RunFilter{}),
		"runs deep":    runPage(jobsdata.RunFilter{After: runSortKeyOf(time.Unix(1700000000+3000*60, 0), 3000, 1)}),
		"runs running": runPage(jobsdata.RunFilter{State: jobsdata.Running}),
		"runs queued":  runPage(jobsdata.RunFilter{State: jobsdata.Queued}),
		"runs failed":  runPage(jobsdata.RunFilter{Result: jobsdata.Result("failure")}),
		"runs repo":    runPage(jobsdata.RunFilter{RepositoryID: 7}),
		"jobs": func() error {
			rows, total, err := js.ListJobs(ctx, jobsdata.JobFilter{})
			if err == nil && (len(rows) != 51 || total != 100000) {
				t.Fatalf("jobs page: %d rows of %d", len(rows), total)
			}
			return err
		},
	}
	for name, fn := range cases {
		var d []time.Duration
		for range 20 {
			start := time.Now()
			if err := fn(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			d = append(d, time.Since(start))
		}
		if got := p95(d); got > listP95Limit {
			t.Errorf("%s: p95 %v over %v", name, got, listP95Limit)
		}
	}
}

func BenchmarkListRunsPage(b *testing.B) {
	js := listStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if rows, _, err := js.ListRuns(ctx, jobsdata.RunFilter{}); err != nil || len(rows) != 51 {
			b.Fatal(len(rows), err)
		}
	}
}

func BenchmarkListJobsPage(b *testing.B) {
	js := listStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if rows, _, err := js.ListJobs(ctx, jobsdata.JobFilter{}); err != nil || len(rows) != 51 {
			b.Fatal(len(rows), err)
		}
	}
}

// The list queries must use the indexes of migration 00046 and never scan the jobs table once per run.
func TestListQueryPlans(t *testing.T) {
	if raceOn {
		t.Skip("seeding 110,000 rows is slow under the race detector")
	}
	js := listStore(t)
	plan := func(q string, args ...any) string {
		rows, err := js.rdb.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	t.Log("page:\n" + plan(`SELECT repository_id FROM runs r ORDER BY `+runSortKey+` DESC LIMIT 51`))
	t.Log("counts:\n" + plan(`SELECT count(*) FROM (VALUES (1,1,1),(2,2,1)) v JOIN jobs j ON j.repository_id = v.column1 AND j.run_id = v.column2 AND j.run_attempt = v.column3 GROUP BY j.repository_id, j.run_id, j.run_attempt`))
	if c := plan(`SELECT count(*) FROM (VALUES (1,1,1)) v JOIN jobs j ON j.repository_id = v.column1 AND j.run_id = v.column2 AND j.run_attempt = v.column3`); !strings.Contains(c, "jobs_run") {
		t.Errorf("count plan: %s", c)
	}
	if p := plan(`SELECT repository_id FROM runs r ORDER BY ` + runSortKey + ` DESC LIMIT 51`); !strings.Contains(p, "runs_sort_key") || strings.Contains(p, "TEMP B-TREE") {
		t.Errorf("run page plan: %s", p)
	}
}
