package store

import (
	"context"
)

// The clocks of crsp's job and run timers (migration 00039, scheduling R5.99 to
// R5.106). The tables job_clocks and run_clocks are keyed like jobs and runs and are
// independent of them, so the console joins them by id.

// SettingTimers is the key of the timer settings in the settings table (JSON).
const SettingTimers = "timers"

// JobClockRow is the clock of one job.
type JobClockRow struct {
	JobID                  int64
	RepoID                 int64
	Repo                   string
	RunID                  int64
	RunAttempt             int
	StartedAt              string
	LimitSec               int
	Extended               bool
	State                  string
	Deadline               string
	Note                   string
	MachineID, RunnerRef   string
	QueuedMs, StartupMs    int64
	HoldMs, RunMs          int64
	JobTimerMs, RunTimerMs int64
}

// RunClockRow is the clock of one run attempt.
type RunClockRow struct {
	RepoID     int64
	Repo       string
	RunID      int64
	RunAttempt int
	StartedAt  string
	LimitSec   int
	Extended   bool
	State      string
	Deadline   string
	Note       string
	TimerMs    int64
}

// TimerSettingsJSON reads the saved timer settings; ok is false when none.
func (s *Store) TimerSettingsJSON(ctx context.Context) (string, bool) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return settingValue(ctx, s.db, SettingTimers)
}

// SaveTimerSettings saves the timer settings as JSON.
func (s *Store) SaveTimerSettings(ctx context.Context, jsonValue, by string) error {
	return s.putSetting(ctx, SettingTimers, jsonValue, by)
}

// SaveJobClock writes the clock of a job.
func (s *Store) SaveJobClock(ctx context.Context, r JobClockRow) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_clocks (job_id, repository_id, repository, run_id, run_attempt,
		started_at, limit_sec, extended, state, deadline, note,
		machine_id, runner_ref, queued_ms, startup_ms, hold_ms, run_ms, job_timer_ms, run_timer_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
		started_at = excluded.started_at, limit_sec = excluded.limit_sec,
		extended = excluded.extended, state = excluded.state,
		deadline = excluded.deadline, note = excluded.note,
		machine_id = excluded.machine_id, runner_ref = excluded.runner_ref,
		queued_ms = excluded.queued_ms, startup_ms = excluded.startup_ms, hold_ms = excluded.hold_ms,
		run_ms = excluded.run_ms, job_timer_ms = excluded.job_timer_ms, run_timer_ms = excluded.run_timer_ms`,
		r.JobID, r.RepoID, r.Repo, r.RunID, r.RunAttempt,
		r.StartedAt, r.LimitSec, b2i(r.Extended), r.State, r.Deadline, r.Note,
		r.MachineID, r.RunnerRef, r.QueuedMs, r.StartupMs, r.HoldMs, r.RunMs, r.JobTimerMs, r.RunTimerMs)
	return err
}

// SaveRunClock writes the clock of a run attempt.
func (s *Store) SaveRunClock(ctx context.Context, r RunClockRow) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_clocks (repository_id, run_id, run_attempt, repository,
		started_at, limit_sec, extended, state, deadline, note, run_timer_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository_id, run_id, run_attempt) DO UPDATE SET
		started_at = excluded.started_at, limit_sec = excluded.limit_sec,
		extended = excluded.extended, state = excluded.state,
		deadline = excluded.deadline, note = excluded.note,
		run_timer_ms = excluded.run_timer_ms`,
		r.RepoID, r.RunID, r.RunAttempt, r.Repo,
		r.StartedAt, r.LimitSec, b2i(r.Extended), r.State, r.Deadline, r.Note, r.TimerMs)
	return err
}

// JobClock reads the clock of one job; ok is false when it has none.
func (s *Store) JobClock(ctx context.Context, jobID int64) (JobClockRow, bool, error) {
	rows, err := s.jobClocks(ctx, `WHERE job_id = ?`, jobID)
	if err != nil || len(rows) == 0 {
		return JobClockRow{}, false, err
	}
	return rows[0], true, nil
}

// OpenJobClocks lists the jobs whose clock is running or in its grace.
func (s *Store) OpenJobClocks(ctx context.Context) ([]JobClockRow, error) {
	return s.jobClocks(ctx, `WHERE state IN ('running', 'grace')`)
}

func (s *Store) jobClocks(ctx context.Context, where string, args ...any) ([]JobClockRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, repository_id, repository, run_id, run_attempt,
		started_at, limit_sec, extended, state, deadline, note,
		machine_id, runner_ref, queued_ms, startup_ms, hold_ms, run_ms, job_timer_ms, run_timer_ms
		FROM job_clocks `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []JobClockRow
	for rows.Next() {
		var r JobClockRow
		var ext int
		if err := rows.Scan(&r.JobID, &r.RepoID, &r.Repo, &r.RunID, &r.RunAttempt, &r.StartedAt, &r.LimitSec, &ext,
			&r.State, &r.Deadline, &r.Note, &r.MachineID, &r.RunnerRef, &r.QueuedMs, &r.StartupMs, &r.HoldMs,
			&r.RunMs, &r.JobTimerMs, &r.RunTimerMs); err != nil {
			return nil, err
		}
		r.Extended = ext != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenRunClocks lists the runs whose clock is running or in its grace.
func (s *Store) OpenRunClocks(ctx context.Context) ([]RunClockRow, error) {
	return s.runClocks(ctx, `WHERE state IN ('running', 'grace')`)
}

// RunClock reads the clock of one run attempt; ok is false when it has none.
func (s *Store) RunClock(ctx context.Context, repoID, runID int64, attempt int) (RunClockRow, bool, error) {
	rows, err := s.runClocks(ctx, `WHERE repository_id = ? AND run_id = ? AND run_attempt = ?`, repoID, runID, attempt)
	if err != nil || len(rows) == 0 {
		return RunClockRow{}, false, err
	}
	return rows[0], true, nil
}

func (s *Store) runClocks(ctx context.Context, where string, args ...any) ([]RunClockRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id, repository, run_id, run_attempt,
		started_at, limit_sec, extended, state, deadline, note, run_timer_ms
		FROM run_clocks `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunClockRow
	for rows.Next() {
		var r RunClockRow
		var ext int
		if err := rows.Scan(&r.RepoID, &r.Repo, &r.RunID, &r.RunAttempt, &r.StartedAt, &r.LimitSec, &ext,
			&r.State, &r.Deadline, &r.Note, &r.TimerMs); err != nil {
			return nil, err
		}
		r.Extended = ext != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
