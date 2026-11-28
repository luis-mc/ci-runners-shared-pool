package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// seedDone writes 20,000 done jobs over 20 days, so a one-day window is a small slice of the done jobs.
func seedDone(tb testing.TB, s *Store) {
	tb.Helper()
	_, err := s.db.ExecContext(context.Background(), `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 20000)
		INSERT INTO jobs (repository_id, repository, run_id, run_attempt, workflow, name, job_ident, branch, state, result, machine_id, started_at, created_at, completed_at)
		SELECT i % 40, 'o/r', i, 1, 'ci.yml', 'job' || i, 'k' || (i % 8), 'main', 'done', 'success', 'm1',
			strftime('%Y-%m-%dT%H:%M:%SZ', 1700000000 + i * 86 - 30, 'unixepoch'),
			strftime('%Y-%m-%dT%H:%M:%SZ', 1700000000 + i * 86 - 60, 'unixepoch'),
			strftime('%Y-%m-%dT%H:%M:%SZ', 1700000000 + i * 86, 'unixepoch') FROM n`)
	if err != nil {
		tb.Fatal(err)
	}
}

func explain(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.rdb.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
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

// R3-1: the window reads of finished jobs use (state, completed_at) and never sort.
func TestFinishedWindowQueryPlans(t *testing.T) {
	if raceOn {
		t.Skip("seeding is slow under the race detector")
	}
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedDone(t, s)
	from, to := fmtTime(time.Unix(1700000000, 0)), fmtTime(time.Unix(1700086400, 0))
	for name, q := range map[string]string{
		"pool jobs": finishedPoolJobsSQL,
		"between":   finishedBetweenSQL,
		"any":       anyFinishedSQL,
	} {
		args := []any{from, to}
		if name == "between" {
			args = append(args, 100)
		}
		p := explain(t, s, q, args...)
		// The pool jobs read groups in SQL (CRSP256), so a temp tree for GROUP BY is the aggregation; no ORDER BY sort is allowed.
		if name == "pool jobs" {
			p = strings.ReplaceAll(p, "USE TEMP B-TREE FOR GROUP BY", "")
		}
		if !strings.Contains(p, "jobs_state_completed") || strings.Contains(p, "TEMP B-TREE") || strings.Contains(p, "SCAN") && !strings.Contains(p, "USING") {
			t.Errorf("%s plan: %s", name, p)
		}
	}
}

// R3-1: a forecast sample read of one repository stays on jobs_sample when a Since bound is set.
func TestSampleQueryPlanKeepsSampleIndex(t *testing.T) {
	if raceOn {
		t.Skip("seeding is slow under the race detector")
	}
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedDone(t, s)
	q, args := sampleSQL(jobsdata.SampleQuery{RepositoryID: 5, JobKey: "k1", Since: time.Unix(1700000000, 0)})
	if p := explain(t, s, q, args...); !strings.Contains(p, "jobs_sample") || strings.Contains(p, "jobs_state_completed") {
		t.Errorf("sample plan: %s", p)
	}
}

// R3-3: the hour grid and the created-at window read jobs_created, not the whole table.
func TestCreatedWindowQueryPlan(t *testing.T) {
	if raceOn {
		t.Skip("seeding is slow under the race detector")
	}
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedDone(t, s)
	p := explain(t, s, `SELECT CAST(strftime('%w', created_at) AS INTEGER), CAST(strftime('%H', created_at) AS INTEGER), count(*) FROM jobs WHERE created_at >= ? GROUP BY 1, 2`, "2023-11-20T00:00:00.000Z")
	if !strings.Contains(p, "jobs_created") {
		t.Errorf("hour grid plan: %s", p)
	}
}

// R3-4: the follow-up read starts from run_follow_ups by its parent index and looks each run up by key.
func TestFollowUpsQueryPlan(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var args []any
	vals := make([]string, 0, 50)
	for i := range 50 {
		vals = append(vals, "(?, ?)")
		args = append(args, i%40, 100+i)
	}
	p := explain(t, s, followUpsSQL(vals), args...)
	first, _, _ := strings.Cut(p, "\n")
	if !strings.Contains(first, "SEARCH f USING INDEX run_follow_ups_parent") || strings.Contains(p, "runs_repository") {
		t.Errorf("follow-ups plan: %s", p)
	}
}
