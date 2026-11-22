package store

import (
	"context"
	"database/sql"
	"errors"
)

// The listener gate's rows (migration 00040, runners R5.134 to R5.140): one lease
// per GitHub job id and one wake row per runner.

// SettingListenerGate is the key of the gate settings in the settings table (JSON).
const SettingListenerGate = "listener_gate"

// ListenerLeaseRow is one lease; Body is the lease as JSON.
type ListenerLeaseRow struct {
	JobID     int64
	RunnerID  string
	State     string
	Body      string
	UpdatedAt string
}

// ListenerGateJSON reads the saved gate settings; ok is false when none.
func (s *Store) ListenerGateJSON(ctx context.Context) (string, bool) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return settingValue(ctx, s.db, SettingListenerGate)
}

// SaveListenerGate saves the gate settings as JSON.
func (s *Store) SaveListenerGate(ctx context.Context, jsonValue, by string) error {
	return s.putSetting(ctx, SettingListenerGate, jsonValue, by)
}

// ClaimListenerLease inserts r when no lease of its job exists. It returns the
// stored row and whether it was just made.
func (s *Store) ClaimListenerLease(ctx context.Context, r ListenerLeaseRow) (ListenerLeaseRow, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO listener_leases (job_id, runner_id, state, body, updated_at)
		VALUES (?, ?, ?, ?, ?)`, r.JobID, r.RunnerID, r.State, r.Body, r.UpdatedAt)
	if err != nil {
		return r, false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return r, true, nil
	}
	got, ok, err := s.listenerLease(ctx, r.JobID)
	if err != nil {
		return r, false, err
	}
	if !ok {
		return r, false, errors.New("listener lease vanished")
	}
	return got, false, nil
}

func (s *Store) listenerLease(ctx context.Context, id int64) (ListenerLeaseRow, bool, error) {
	var r ListenerLeaseRow
	err := s.db.QueryRowContext(ctx, `SELECT job_id, runner_id, state, body, updated_at FROM listener_leases WHERE job_id = ?`, id).
		Scan(&r.JobID, &r.RunnerID, &r.State, &r.Body, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// ListenerLease reads one lease.
func (s *Store) ListenerLease(ctx context.Context, id int64) (ListenerLeaseRow, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.listenerLease(ctx, id)
}

// SaveListenerLease replaces a lease.
func (s *Store) SaveListenerLease(ctx context.Context, r ListenerLeaseRow) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO listener_leases (job_id, runner_id, state, body, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET runner_id = excluded.runner_id, state = excluded.state,
		body = excluded.body, updated_at = excluded.updated_at`, r.JobID, r.RunnerID, r.State, r.Body, r.UpdatedAt)
	return err
}

// ListenerLeases lists the leases of a state, of one runner when runnerID is not empty.
func (s *Store) ListenerLeases(ctx context.Context, state, runnerID string) ([]ListenerLeaseRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	q := `SELECT job_id, runner_id, state, body, updated_at FROM listener_leases WHERE state = ?`
	args := []any{state}
	if runnerID != "" {
		q += ` AND runner_id = ?`
		args = append(args, runnerID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY job_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ListenerLeaseRow
	for rows.Next() {
		var r ListenerLeaseRow
		if err := rows.Scan(&r.JobID, &r.RunnerID, &r.State, &r.Body, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListenerWake is the wake row of one runner (RFC3339 text, "" for none).
type ListenerWake struct {
	RunnerID    string
	LastWake    string
	WakingSince string
}

// ListenerWakeOf reads a runner's wake row; the zero row when none.
func (s *Store) ListenerWakeOf(ctx context.Context, runnerID string) (ListenerWake, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	w := ListenerWake{RunnerID: runnerID}
	err := s.db.QueryRowContext(ctx, `SELECT last_wake, waking_since FROM listener_wakes WHERE runner_id = ?`, runnerID).Scan(&w.LastWake, &w.WakingSince)
	if errors.Is(err, sql.ErrNoRows) {
		return w, nil
	}
	return w, err
}

// SaveListenerWake writes a runner's wake row.
func (s *Store) SaveListenerWake(ctx context.Context, w ListenerWake) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO listener_wakes (runner_id, last_wake, waking_since) VALUES (?, ?, ?)
		ON CONFLICT(runner_id) DO UPDATE SET last_wake = excluded.last_wake, waking_since = excluded.waking_since`,
		w.RunnerID, w.LastWake, w.WakingSince)
	return err
}

// ListenerWaking lists the wake rows whose wake began and has not ended.
func (s *Store) ListenerWaking(ctx context.Context) ([]ListenerWake, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT runner_id, last_wake, waking_since FROM listener_wakes WHERE waking_since <> ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ListenerWake
	for rows.Next() {
		var w ListenerWake
		if err := rows.Scan(&w.RunnerID, &w.LastWake, &w.WakingSince); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
