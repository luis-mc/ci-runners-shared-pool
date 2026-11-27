package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/assign"
)

// Assign is the database side of assign.Store (migration 00038, OPS80): the
// lease of each job, the warm runners and the busy label sets. The job id is
// the primary key, so Claim and Begin are single statements that two callers
// can never both win.
func (s *Store) Assign() *AssignStore { return &AssignStore{s} }

// AssignStore keeps the leases of the assign controller.
type AssignStore struct{ s *Store }

var _ assign.Store = (*AssignStore)(nil)

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTS(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

const leaseCols = `job_id, run_id, run_attempt, repository, job_name, labels, job_label, os, arch, state, machine_id,
	runner_name, runner_record, github_runner, cpus, mem_gib, class, queued_at, started_at, passes, reserved_since,
	reason, reason_code, fails, next_try, alerted, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanLease(r rowScanner) (assign.Lease, error) {
	var l assign.Lease
	var labels, state, queued, started, reserved, next, updated string
	var alerted int
	if err := r.Scan(&l.JobID, &l.RunID, &l.RunAttempt, &l.Repository, &l.JobName, &labels, &l.JobLabel, &l.OS, &l.Arch, &state,
		&l.MachineID, &l.RunnerName, &l.RunnerRecord, &l.GitHubRunner, &l.Req.CPUs, &l.Req.MemoryGiB, &l.Class, &queued, &started,
		&l.Passes, &reserved, &l.Reason, &l.ReasonCode, &l.Fails, &next, &alerted, &updated); err != nil {
		return l, err
	}
	_ = json.Unmarshal([]byte(labels), &l.Labels)
	l.State = assign.State(state)
	l.QueuedAt, l.StartedAt, l.ReservedSince = parseTS(queued), parseTS(started), parseTS(reserved)
	l.NextTry, l.UpdatedAt, l.Alerted = parseTS(next), parseTS(updated), alerted == 1
	return l, nil
}

func leaseArgs(l assign.Lease) []any {
	labels, _ := json.Marshal(l.Labels)
	if l.Labels == nil {
		labels = []byte("[]")
	}
	cpus, mem := max(l.Req.CPUs, 1), max(l.Req.MemoryGiB, 1)
	alerted := 0
	if l.Alerted {
		alerted = 1
	}
	if l.RunAttempt < 1 {
		l.RunAttempt = 1
	}
	return []any{l.JobID, l.RunID, l.RunAttempt, l.Repository, l.JobName, string(labels), l.JobLabel, l.OS, l.Arch, string(l.State),
		l.MachineID, l.RunnerName, l.RunnerRecord, l.GitHubRunner, cpus, mem, l.Class, ts(l.QueuedAt), ts(l.StartedAt), l.Passes,
		ts(l.ReservedSince), l.Reason, l.ReasonCode, l.Fails, ts(l.NextTry), alerted, ts(l.UpdatedAt)}
}

// Claim implements assign.Store: INSERT OR IGNORE, then read what is stored.
func (a *AssignStore) Claim(ctx context.Context, l assign.Lease) (assign.Lease, bool, error) {
	res, err := a.s.db.ExecContext(ctx, `INSERT OR IGNORE INTO assign_leases (`+leaseCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, leaseArgs(l)...)
	if err != nil {
		return l, false, err
	}
	n, _ := res.RowsAffected()
	cur, _, err := a.Get(ctx, l.JobID)
	return cur, n == 1, err
}

// Get implements assign.Store.
func (a *AssignStore) Get(ctx context.Context, id int64) (assign.Lease, bool, error) {
	l, err := scanLease(a.s.db.QueryRowContext(ctx, `SELECT `+leaseCols+` FROM assign_leases WHERE job_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return assign.Lease{}, false, nil
	}
	return l, err == nil, err
}

// Save implements assign.Store.
func (a *AssignStore) Save(ctx context.Context, l assign.Lease) error {
	_, err := a.s.db.ExecContext(ctx, `INSERT OR REPLACE INTO assign_leases (`+leaseCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, leaseArgs(l)...)
	return err
}

// Begin implements assign.Store: the one UPDATE that only a waiting lease takes.
func (a *AssignStore) Begin(ctx context.Context, id int64, machineID, runnerName string, now time.Time) (bool, error) {
	res, err := a.s.db.ExecContext(ctx, `UPDATE assign_leases SET state = 'starting', machine_id = ?, runner_name = ?, started_at = ?, updated_at = ?
		WHERE job_id = ? AND state = 'waiting'`, machineID, runnerName, ts(now), ts(now), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// List implements assign.Store.
func (a *AssignStore) List(ctx context.Context, states ...assign.State) ([]assign.Lease, error) {
	q := `SELECT ` + leaseCols + ` FROM assign_leases`
	var args []any
	if len(states) > 0 {
		q += ` WHERE state IN (` + strings.TrimSuffix(strings.Repeat("?,", len(states)), ",") + `)`
		for _, s := range states {
			args = append(args, string(s))
		}
	}
	q += ` ORDER BY queued_at, job_id`
	rows, err := a.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []assign.Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ByRunner implements assign.Store.
func (a *AssignStore) ByRunner(ctx context.Context, name string) (assign.Lease, bool, error) {
	if name == "" {
		return assign.Lease{}, false, nil
	}
	l, err := scanLease(a.s.db.QueryRowContext(ctx, `SELECT `+leaseCols+` FROM assign_leases
		WHERE runner_name = ? AND state IN ('waiting','starting','running') ORDER BY job_id LIMIT 1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return assign.Lease{}, false, nil
	}
	return l, err == nil, err
}

// ListWarm implements assign.Store.
func (a *AssignStore) ListWarm(ctx context.Context) ([]assign.Warm, error) {
	rows, err := a.s.db.QueryContext(ctx, `SELECT id, machine_id, label_key, repository, os, labels, runner_name, runner_record,
		github_runner, cpus, mem_gib, started_at FROM assign_warm ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []assign.Warm
	for rows.Next() {
		var w assign.Warm
		var labels, started string
		if err := rows.Scan(&w.ID, &w.MachineID, &w.LabelKey, &w.Repository, &w.OS, &labels, &w.RunnerName, &w.RunnerRecord,
			&w.GitHubRunner, &w.Req.CPUs, &w.Req.MemoryGiB, &started); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &w.Labels)
		w.StartedAt = parseTS(started)
		out = append(out, w)
	}
	return out, rows.Err()
}

// PutWarm implements assign.Store.
func (a *AssignStore) PutWarm(ctx context.Context, w assign.Warm) error {
	labels, _ := json.Marshal(w.Labels)
	_, err := a.s.db.ExecContext(ctx, `INSERT OR REPLACE INTO assign_warm (id, machine_id, label_key, repository, os, labels, runner_name,
		runner_record, github_runner, cpus, mem_gib, started_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.ID, w.MachineID, w.LabelKey, w.Repository, w.OS, string(labels), w.RunnerName, w.RunnerRecord, w.GitHubRunner,
		max(w.Req.CPUs, 1), max(w.Req.MemoryGiB, 1), ts(w.StartedAt))
	return err
}

// DeleteWarm implements assign.Store.
func (a *AssignStore) DeleteWarm(ctx context.Context, id string) error {
	_, err := a.s.db.ExecContext(ctx, `DELETE FROM assign_warm WHERE id = ?`, id)
	return err
}

// TouchSet implements assign.Store.
func (a *AssignStore) TouchSet(ctx context.Context, s assign.Set) error {
	labels, _ := json.Marshal(s.Labels)
	_, err := a.s.db.ExecContext(ctx, `INSERT OR REPLACE INTO assign_labelsets (label_key, repository, os, labels, cpus, mem_gib, last_busy)
		VALUES (?,?,?,?,?,?,?)`, s.Key, s.Repository, s.OS, string(labels), max(s.Req.CPUs, 1), max(s.Req.MemoryGiB, 1), ts(s.LastBusy))
	return err
}

// ListSets implements assign.Store.
func (a *AssignStore) ListSets(ctx context.Context) ([]assign.Set, error) {
	rows, err := a.s.db.QueryContext(ctx, `SELECT label_key, repository, os, labels, cpus, mem_gib, last_busy FROM assign_labelsets ORDER BY label_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []assign.Set
	for rows.Next() {
		var s assign.Set
		var labels, busy string
		if err := rows.Scan(&s.Key, &s.Repository, &s.OS, &labels, &s.Req.CPUs, &s.Req.MemoryGiB, &busy); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &s.Labels)
		s.LastBusy = parseTS(busy)
		out = append(out, s)
	}
	return out, rows.Err()
}
