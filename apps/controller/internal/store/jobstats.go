package store

import (
	"context"
	"fmt"
	"time"
)

// JobStatsScope picks the jobs of one machine or one runner; set one.
type JobStatsScope struct {
	MachineID string
	RunnerID  string
}

func (sc JobStatsScope) clause() (string, string) {
	if sc.RunnerID != "" {
		return "j.runner_id = ?", sc.RunnerID
	}
	return "j.machine_id = ?", sc.MachineID
}

// JobStatsCounts is how the jobs that completed in a window ended. A job crsp
// ended itself counts by its reason (timer, memory, stop); the rest by the
// result GitHub gave. Other holds the jobs with no known result (lost).
type JobStatsCounts struct {
	Succeeded, Failed, Cancelled, TimedOut, OverMemory, Stopped, Other int64
}

// Total is every job of the window.
func (c JobStatsCounts) Total() int64 {
	return c.Succeeded + c.Failed + c.Cancelled + c.TimedOut + c.OverMemory + c.Stopped + c.Other
}

// JobFailureRef is one failed job with the links to its job and run.
type JobFailureRef struct {
	JobID            int64
	Repository, Name string
	RunID            int64
	Class            string
	At               time.Time
	JobURL, RunURL   string
}

// FailingJob is a job name and how often it failed in the window.
type FailingJob struct {
	Repository, Name string
	Failures         int64
	Last             time.Time
}

// JobStats is what the console shows of one machine or runner over a window.
type JobStats struct {
	From, To time.Time
	Counts   JobStatsCounts
	// AvgRunSeconds and AvgQueueWaitSeconds are the means; the Known flags say
	// whether any job had the times, so 0 is never a stand-in for unknown.
	AvgRunSeconds       float64
	AvgRunKnown         bool
	AvgQueueWaitSeconds float64
	AvgQueueWaitKnown   bool
	// BusySeconds is the time jobs ran inside the window, summed (two jobs at
	// once count twice); the caller divides by the window and the slots.
	BusySeconds float64
	LastFailure *JobFailureRef
	TopFailing  []FailingJob
}

const classExpr = `CASE WHEN r.reason = 'timeout' THEN 'timed_out' WHEN r.reason = 'over_memory' THEN 'over_memory' WHEN r.reason = 'stopped' THEN 'stopped'
 WHEN j.result = 'success' THEN 'succeeded' WHEN j.result = 'failure' THEN 'failed' WHEN j.result = 'cancelled' THEN 'cancelled'
 WHEN j.result = 'timed_out' THEN 'timed_out' ELSE 'other' END`

// failureClasses are the classes the last failure and the top failing jobs count.
const failureClasses = `('failed', 'timed_out', 'over_memory', 'stopped')`

// JobStats reads the stats of one machine or runner for the jobs that completed in [from, to).
// It runs four indexed queries (jobs_machine_done, jobs_runner_done).
func (s *Store) JobStats(ctx context.Context, sc JobStatsScope, from, to time.Time) (JobStats, error) {
	out := JobStats{From: from, To: to}
	where, arg := sc.clause()
	f, t := fmtTime(from), fmtTime(to)

	if err := s.statsCounts(ctx, where, arg, f, t, &out); err != nil {
		return out, err
	}

	// Busy time: the part of every job's run that lies inside the window.
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(MAX(0, (CAST(strftime('%s', MIN(CASE WHEN j.completed_at = '' THEN ? ELSE j.completed_at END, ?)) AS REAL)
 - CAST(strftime('%s', MAX(j.started_at, ?)) AS REAL)))), 0) FROM jobs j
 WHERE `+where+` AND j.state IN ('running', 'done') AND j.started_at != '' AND j.started_at < ? AND (j.completed_at = '' OR j.completed_at > ?)`,
		t, t, f, arg, t, f).Scan(&out.BusySeconds); err != nil {
		return out, fmt.Errorf("job stats busy: %w", err)
	}

	lf, err := s.statsLastFailure(ctx, where, arg, f, t)
	if err != nil {
		return out, err
	}
	out.LastFailure = lf

	top, err := s.db.QueryContext(ctx, `SELECT j.repository, j.name, COUNT(*) AS n, MAX(j.completed_at)
 FROM jobs j LEFT JOIN job_end_reasons r ON r.job_id = j.id
 WHERE `+where+` AND j.state = 'done' AND j.completed_at >= ? AND j.completed_at < ? AND `+classExpr+` IN `+failureClasses+`
 GROUP BY j.repository, j.name ORDER BY n DESC, MAX(j.completed_at) DESC, j.repository, j.name LIMIT 5`, arg, f, t)
	if err != nil {
		return out, fmt.Errorf("job stats top failing: %w", err)
	}
	defer func() { _ = top.Close() }()
	for top.Next() {
		var fj FailingJob
		var last string
		if err := top.Scan(&fj.Repository, &fj.Name, &fj.Failures, &last); err != nil {
			return out, err
		}
		fj.Last = parseTime(last)
		out.TopFailing = append(out.TopFailing, fj)
	}
	return out, top.Err()
}

// RunnerRef names a runner that ran jobs.
type RunnerRef struct{ ID, Name string }

// MachineRunnersWithJobs lists the runners of a machine that ran or run a job in [from, to).
func (s *Store) MachineRunnersWithJobs(ctx context.Context, machineID string, from, to time.Time) ([]RunnerRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT runner_id, MAX(runner_name) FROM jobs
 WHERE machine_id = ? AND runner_id != '' AND (completed_at >= ? AND completed_at < ? OR (state = 'running' AND started_at < ?))
 GROUP BY runner_id ORDER BY MAX(runner_name), runner_id`, machineID, fmtTime(from), fmtTime(to), fmtTime(to))
	if err != nil {
		return nil, fmt.Errorf("runners with jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RunnerRef
	for rows.Next() {
		var r RunnerRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) statsCounts(ctx context.Context, where, arg, f, t string, out *JobStats) error {
	rows, err := s.db.QueryContext(ctx, `SELECT class, COUNT(*), COALESCE(SUM(run_s), 0), COUNT(run_s), COALESCE(SUM(wait_s), 0), COUNT(wait_s) FROM (
 SELECT `+classExpr+` AS class,
  CASE WHEN j.started_at != '' THEN (CAST(strftime('%s', j.completed_at) AS REAL) - CAST(strftime('%s', j.started_at) AS REAL)) END AS run_s,
  CASE WHEN j.started_at != '' AND COALESCE(NULLIF(j.queued_at, ''), NULLIF(j.created_at, '')) IS NOT NULL
   THEN (CAST(strftime('%s', j.started_at) AS REAL) - CAST(strftime('%s', COALESCE(NULLIF(j.queued_at, ''), j.created_at)) AS REAL)) END AS wait_s
 FROM jobs j LEFT JOIN job_end_reasons r ON r.job_id = j.id
 WHERE `+where+` AND j.state = 'done' AND j.completed_at >= ? AND j.completed_at < ?) GROUP BY class`, arg, f, t)
	if err != nil {
		return fmt.Errorf("job stats counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var runSum, waitSum float64
	var runN, waitN int64
	for rows.Next() {
		var class string
		var n, rn, wn int64
		var rs, ws float64
		if err := rows.Scan(&class, &n, &rs, &rn, &ws, &wn); err != nil {
			return err
		}
		switch class {
		case "succeeded":
			out.Counts.Succeeded = n
		case "failed":
			out.Counts.Failed = n
		case "cancelled":
			out.Counts.Cancelled = n
		case "timed_out":
			out.Counts.TimedOut = n
		case "over_memory":
			out.Counts.OverMemory = n
		case "stopped":
			out.Counts.Stopped = n
		default:
			out.Counts.Other = n
		}
		runSum, runN, waitSum, waitN = runSum+rs, runN+rn, waitSum+ws, waitN+wn
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if runN > 0 {
		out.AvgRunSeconds, out.AvgRunKnown = runSum/float64(runN), true
	}
	if waitN > 0 {
		out.AvgQueueWaitSeconds, out.AvgQueueWaitKnown = waitSum/float64(waitN), true
	}
	return nil
}

func (s *Store) statsLastFailure(ctx context.Context, where, arg, f, t string) (*JobFailureRef, error) {
	fail, err := s.db.QueryContext(ctx, `SELECT j.id, j.repository, j.name, j.run_id, `+classExpr+` AS class, j.completed_at, j.html_url, COALESCE(u.html_url, '')
 FROM jobs j LEFT JOIN job_end_reasons r ON r.job_id = j.id
 LEFT JOIN runs u ON u.repository_id = j.repository_id AND u.run_id = j.run_id AND u.run_attempt = j.run_attempt
 WHERE `+where+` AND j.state = 'done' AND j.completed_at >= ? AND j.completed_at < ? AND `+classExpr+` IN `+failureClasses+`
 ORDER BY j.completed_at DESC LIMIT 1`, arg, f, t)
	if err != nil {
		return nil, fmt.Errorf("job stats last failure: %w", err)
	}
	defer func() { _ = fail.Close() }()
	if fail.Next() {
		var lf JobFailureRef
		var at string
		if err := fail.Scan(&lf.JobID, &lf.Repository, &lf.Name, &lf.RunID, &lf.Class, &at, &lf.JobURL, &lf.RunURL); err != nil {
			return nil, err
		}
		lf.At = parseTime(at)
		return &lf, nil
	}
	return nil, fail.Err()
}
