package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// This file is the SQLite side of the scheduler's slots.Store (scheduling
// D1: slots, tickets and the slot log live in the controller's tables, and a
// take is one transaction). It is a separate file so the scheduler work and
// the store work do not touch the same lines.

var _ slots.Store = (*Store)(nil)

const timeLayout = "2006-01-02T15:04:05.000Z"

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC()
}

// Atomically runs fn in one transaction: all or nothing (scheduling R5.14).
func (s *Store) Atomically(ctx context.Context, fn func(slots.Tx) error) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(&schedTx{ctx: ctx, tx: sqlTx, q: s.q.WithTx(sqlTx)}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

type schedTx struct {
	ctx context.Context
	tx  *sql.Tx
	q   *sqlcgen.Queries
}

func (t *schedTx) Settings(machineID string) (slots.Settings, error) {
	s := slots.Defaults()
	row := t.tx.QueryRowContext(t.ctx, `SELECT budget_memory_pct, budget_cpu_pct, job_slots, light_slots, heavy_slots,
		slot_max_age_minutes, gate_max_wait_minutes, gate_poll_seconds, gate_on_timeout, light_jobs, heavy_jobs
		FROM machine_settings WHERE machine_id = ?`, machineID)
	var light, heavy string
	err := row.Scan(&s.BudgetMemoryPct, &s.BudgetCPUPct, &s.JobSlots, &s.LightSlots, &s.HeavySlots,
		&s.SlotMaxAgeMin, &s.GateMaxWaitMin, &s.GatePollSec, &s.OnTimeout, &light, &heavy)
	if errors.Is(err, sql.ErrNoRows) {
		s = slots.Defaults()
	} else if err != nil {
		return s, fmt.Errorf("machine settings: %w", err)
	}
	// R5.97: the sizes of the scan, the measuring and the overrides. A read
	// that fails leaves the name classes, so a job is never held for it.
	if sizes, serr := loadSizes(t.ctx, t.tx); serr == nil {
		s.Sizes = sizes
	}
	s.Admission = t.loadAdmission(machineID)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	// An empty list column keeps the defaults of the list.
	def := slots.Defaults()
	s.LightJobs, s.HeavyJobs = def.LightJobs, def.HeavyJobs
	var l []string
	if json.Unmarshal([]byte(light), &l) == nil && len(l) > 0 {
		s.LightJobs = l
	}
	l = nil
	if json.Unmarshal([]byte(heavy), &l) == nil {
		s.HeavyJobs = l
	}
	return s, nil
}

func (t *schedTx) Policy(repository string) (slots.Policy, bool, error) {
	var p slots.Policy
	var mem, cpu sql.NullInt64
	err := t.tx.QueryRowContext(t.ctx, `SELECT p.priority, p.max_parallel_jobs, p.budget_memory_pct, p.budget_cpu_pct
		FROM repo_policies p JOIN repositories r ON r.id = p.repository_id WHERE r.full_name = ?`, repository).
		Scan(&p.Priority, &p.MaxParallel, &mem, &cpu)
	if errors.Is(err, sql.ErrNoRows) {
		return slots.Policy{}, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("repo policy: %w", err)
	}
	p.BudgetMemoryPct, p.BudgetCPUPct = int(mem.Int64), int(cpu.Int64)
	return p, true, nil
}

const ticketCols = `id, machine_id, repository, COALESCE(repository_id, 0), run_id, run_attempt, job,
	COALESCE(github_job_id, 0), shard, runner_name, kind, repo_priority, event_class, queued_at_ns, random_suffix,
	beat_at, expires_at, holder_machine, holder_pid, gate_pid, COALESCE(wait_reason, '')`

func scanTicket(sc interface{ Scan(...any) error }) (slots.Ticket, error) {
	var t slots.Ticket
	var beat, exp, kind string
	err := sc.Scan(&t.ID, &t.MachineID, &t.Job.Repository, &t.RepositoryID, &t.Job.RunID, &t.Job.RunAttempt, &t.Job.Job,
		&t.Job.GitHubJobID, &t.Job.Shard, &t.Job.RunnerName, &kind, &t.Priority, &t.EventClass, &t.QueuedAtNs, &t.Random,
		&beat, &exp, &t.Holder.Machine, &t.Holder.PID, &t.Holder.GatePID, &t.WaitReason)
	t.Kind = slots.Kind(kind)
	t.BeatAt, t.ExpiresAt = parseTime(beat), parseTime(exp)
	return t, err
}

func (t *schedTx) listTickets(where string, args ...any) ([]slots.Ticket, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+ticketCols+` FROM slot_tickets `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("tickets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []slots.Ticket
	for rows.Next() {
		tk, err := scanTicket(rows)
		if err != nil {
			return nil, fmt.Errorf("tickets: %w", err)
		}
		out = append(out, tk)
	}
	return out, rows.Err()
}

func (t *schedTx) Tickets(machineID string) ([]slots.Ticket, error) {
	return t.listTickets(`WHERE machine_id = ? ORDER BY repo_priority DESC, event_class, queued_at_ns, random_suffix`, machineID)
}

func (t *schedTx) AllTickets() ([]slots.Ticket, error) {
	return t.listTickets(`ORDER BY machine_id, queued_at_ns`)
}

func (t *schedTx) Ticket(id string) (slots.Ticket, bool, error) {
	l, err := t.listTickets(`WHERE id = ?`, id)
	if err != nil || len(l) == 0 {
		return slots.Ticket{}, false, err
	}
	return l[0], true, nil
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }

func (t *schedTx) InsertTicket(k slots.Ticket) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO slot_tickets (id, machine_id, repository, repository_id, run_id, run_attempt,
		job, github_job_id, shard, runner_name, kind, repo_priority, event_class, queued_at_ns, random_suffix, beat_at,
		expires_at, holder_machine, holder_pid, gate_pid) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.MachineID, k.Job.Repository, nullInt(k.RepositoryID), k.Job.RunID, k.Job.RunAttempt, k.Job.Job,
		nullInt(k.Job.GitHubJobID), k.Job.Shard, k.Job.RunnerName, string(k.Kind), k.Priority, k.EventClass, k.QueuedAtNs,
		k.Random, fmtTime(k.BeatAt), fmtTime(k.ExpiresAt), k.Holder.Machine, k.Holder.PID, k.Holder.GatePID)
	if err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

func (t *schedTx) BeatTicket(id string, beatAt, expiresAt time.Time) (bool, error) {
	r, err := t.tx.ExecContext(t.ctx, `UPDATE slot_tickets SET beat_at = ?, expires_at = ? WHERE id = ?`,
		fmtTime(beatAt), fmtTime(expiresAt), id)
	if err != nil {
		return false, fmt.Errorf("beat ticket: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

func (t *schedTx) SetTicketWait(id, reason string) error {
	_, err := t.tx.ExecContext(t.ctx, `UPDATE slot_tickets SET wait_reason = ? WHERE id = ?`, reason, id)
	return err
}

func (t *schedTx) DeleteTicket(id string) (bool, error) {
	r, err := t.tx.ExecContext(t.ctx, `DELETE FROM slot_tickets WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete ticket: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

const slotCols = `machine_id, kind, number, claim, repository, COALESCE(repository_id, 0), run_id, run_attempt, job,
	COALESCE(github_job_id, 0), shard, runner_name, holder_machine, holder_pid, gate_pid, queued_at, taken_at, expires_at, holder_id, lease_expires_at`

func (t *schedTx) listSlots(where string, args ...any) ([]slots.Slot, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+slotCols+` FROM slots `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("slots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []slots.Slot
	for rows.Next() {
		var s slots.Slot
		var kind, queued, taken, exp, lease string
		if err := rows.Scan(&s.MachineID, &kind, &s.Number, &s.Claim, &s.Job.Repository, &s.RepositoryID, &s.Job.RunID,
			&s.Job.RunAttempt, &s.Job.Job, &s.Job.GitHubJobID, &s.Job.Shard, &s.Job.RunnerName, &s.Holder.Machine,
			&s.Holder.PID, &s.Holder.GatePID, &queued, &taken, &exp, &s.HolderID, &lease); err != nil {
			return nil, fmt.Errorf("slots: %w", err)
		}
		s.Kind = slots.Kind(kind)
		s.QueuedAt, s.TakenAt, s.ExpiresAt = parseTime(queued), parseTime(taken), parseTime(exp)
		s.LeaseExpiresAt = parseTime(lease)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *schedTx) Slots(machineID string) ([]slots.Slot, error) {
	return t.listSlots(`WHERE machine_id = ? ORDER BY kind, number`, machineID)
}

func (t *schedTx) AllSlots() ([]slots.Slot, error) {
	return t.listSlots(`ORDER BY machine_id, kind, number`)
}

// InsertSlot refuses a second take of the same slot key with slots.ErrSlotTaken.
func (t *schedTx) InsertSlot(s slots.Slot) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO slots (machine_id, kind, number, claim, repository, repository_id, run_id,
		run_attempt, job, github_job_id, shard, runner_name, holder_machine, holder_pid, gate_pid, queued_at, taken_at, expires_at, holder_id, lease_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.MachineID, string(s.Kind), s.Number, s.Claim, s.Job.Repository, nullInt(s.RepositoryID), s.Job.RunID,
		s.Job.RunAttempt, s.Job.Job, nullInt(s.Job.GitHubJobID), s.Job.Shard, s.Job.RunnerName, s.Holder.Machine,
		s.Holder.PID, s.Holder.GatePID, fmtTime(s.QueuedAt), fmtTime(s.TakenAt), fmtTime(s.ExpiresAt),
		s.HolderID, fmtTime(s.LeaseExpiresAt))
	var se *sqlite.Error
	if errors.As(err, &se) && (se.Code() == 1555 || se.Code() == 2067) { // PRIMARY KEY, UNIQUE
		return slots.ErrSlotTaken
	}
	if err != nil {
		return fmt.Errorf("insert slot: %w", err)
	}
	return nil
}

func (t *schedTx) FreeSlot(machineID string, kind slots.Kind, number int, claim string) (bool, error) {
	q := `DELETE FROM slots WHERE machine_id = ? AND kind = ? AND number = ?`
	args := []any{machineID, string(kind), number}
	if claim != "" {
		q += ` AND claim = ?`
		args = append(args, claim)
	}
	r, err := t.tx.ExecContext(t.ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("free slot: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// RenewSlot moves the lease end of the slot only while it still holds the
// holder id and claim, so a slot taken again by another job is never extended.
func (t *schedTx) RenewSlot(machineID string, kind slots.Kind, number int, holderID, claim string, leaseExpiresAt time.Time) (bool, error) {
	r, err := t.tx.ExecContext(t.ctx, `UPDATE slots SET lease_expires_at = ? WHERE machine_id = ? AND kind = ? AND number = ?
		AND holder_id = ? AND claim = ?`, fmtTime(leaseExpiresAt), machineID, string(kind), number, holderID, claim)
	if err != nil {
		return false, fmt.Errorf("renew slot: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

func (t *schedTx) RunningByRepository() (map[string]int, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT repository, count(DISTINCT run_id || '/' || run_attempt || '/' || job)
		FROM slots WHERE kind IN ('ci', 'heavy') GROUP BY repository`)
	if err != nil {
		return nil, fmt.Errorf("running: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var repo string
		var n int
		if err := rows.Scan(&repo, &n); err != nil {
			return nil, err
		}
		out[repo] = n
	}
	return out, rows.Err()
}

func (t *schedTx) AppendLog(r slots.LogRow) error {
	kind := string(r.Kind)
	if kind == "" {
		kind = string(slots.KindCI)
	}
	opt := func(x time.Time) sql.NullString {
		if x.IsZero() {
			return sql.NullString{}
		}
		return sql.NullString{String: fmtTime(x), Valid: true}
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO slot_log (machine_id, at, event, kind, number, repository, run_id, run_attempt,
		job, shard, runner_name, queued_at, slot_at, released_at, reason) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MachineID, fmtTime(r.At), r.Event, kind, nullInt(int64(r.Number)), r.Job.Repository, r.Job.RunID, r.Job.RunAttempt,
		r.Job.Job, r.Job.Shard, r.Job.RunnerName, opt(r.QueuedAt), opt(r.SlotAt), opt(r.ReleasedAt), r.Reason)
	if err != nil {
		return fmt.Errorf("slot log: %w", err)
	}
	var cutoff int64
	err = t.tx.QueryRowContext(t.ctx, `SELECT id FROM slot_log WHERE machine_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?`,
		r.MachineID, slotLogKeep.Load()-1).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("slot log trim: %w", err)
	}
	_, err = t.tx.ExecContext(t.ctx, `DELETE FROM slot_log WHERE machine_id = ? AND id < ?`, r.MachineID, cutoff)
	return err
}

func (t *schedTx) Enqueue(kind, payload string) error {
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}
	_, err := enqueue(t.ctx, t.q, kind, payload, "")
	return err
}

// SeedMachine adds a bare machine row (and no settings row, so the defaults
// apply) for tests of packages that may not use database/sql. A real machine
// joins through the machines feature.
func (s *Store) SeedMachine(ctx context.Context, id, os string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO machines (id, name, os, arch) VALUES (?, ?, ?, 'x64')
		ON CONFLICT (id) DO NOTHING`, id, id, os)
	return err
}

// SetMachineSettingsForTests writes the scheduling columns of a machine's settings row, for tests.
func (s *Store) SetMachineSettingsForTests(ctx context.Context, machineID string, set slots.Settings) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	light, _ := json.Marshal(set.LightJobs)
	heavy, _ := json.Marshal(set.HeavyJobs)
	_, err := s.db.ExecContext(ctx, `INSERT INTO machine_settings (machine_id, budget_memory_pct, budget_cpu_pct, job_slots,
		light_slots, heavy_slots, slot_max_age_minutes, gate_poll_seconds, gate_max_wait_minutes, gate_on_timeout, light_jobs, heavy_jobs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (machine_id) DO UPDATE SET budget_memory_pct = excluded.budget_memory_pct,
		budget_cpu_pct = excluded.budget_cpu_pct, job_slots = excluded.job_slots, light_slots = excluded.light_slots,
		heavy_slots = excluded.heavy_slots, slot_max_age_minutes = excluded.slot_max_age_minutes,
		gate_poll_seconds = excluded.gate_poll_seconds, gate_max_wait_minutes = excluded.gate_max_wait_minutes,
		gate_on_timeout = excluded.gate_on_timeout, light_jobs = excluded.light_jobs, heavy_jobs = excluded.heavy_jobs`,
		machineID, set.BudgetMemoryPct, set.BudgetCPUPct, set.JobSlots, set.LightSlots, set.HeavySlots, set.SlotMaxAgeMin,
		set.GatePollSec, set.GateMaxWaitMin, set.OnTimeout, string(light), string(heavy))
	return err
}

// SetRepoPolicyForTests writes a repository and its policy, for tests.
func (s *Store) SetRepoPolicyForTests(ctx context.Context, repoID int64, fullName string, p slots.Policy) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO installations (id, account_login, account_type) VALUES (1, 'o', 'User')
		ON CONFLICT (id) DO NOTHING`); err != nil {
		return err
	}
	owner, name, _ := strings.Cut(fullName, "/")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO repositories (id, installation_id, full_name, owner, name) VALUES (?, 1, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`, repoID, fullName, owner, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO repo_policies (repository_id, priority, max_parallel_jobs) VALUES (?, ?, ?)
		ON CONFLICT (repository_id) DO UPDATE SET priority = excluded.priority, max_parallel_jobs = excluded.max_parallel_jobs`,
		repoID, p.Priority, p.MaxParallel)
	return err
}

// slotLogKeep is how many slot-log rows a machine keeps. Production keeps
// slots.LogKeep (R5.39); a test lowers it so the trim is reached with a few
// rows instead of thousands (CRSP201).
var slotLogKeep atomic.Int64

func init() { slotLogKeep.Store(slots.LogKeep) }

// SlotLogKeep returns the slot-log keep size in force.
func SlotLogKeep() int { return int(slotLogKeep.Load()) }

// SetSlotLogKeepForTests sets the slot-log keep size and returns a function
// that restores the previous one. For tests only; production never calls it.
func SetSlotLogKeepForTests(n int) (restore func()) {
	prev := slotLogKeep.Swap(int64(n))
	return func() { slotLogKeep.Store(prev) }
}

// SlotLogForTests returns the row count and event order of a machine's slot log, for tests.
func (s *Store) SlotLogForTests(ctx context.Context, machineID string) (int, []string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT event FROM slot_log WHERE machine_id = ? ORDER BY id`, machineID)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	var ev []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return 0, nil, err
		}
		ev = append(ev, e)
	}
	return len(ev), ev, rows.Err()
}

// ---------------------------------------------------------------- jobs for the API

var _ slots.Jobs = (*Store)(nil)

const jobCols = `j.id, COALESCE(r.full_name, ''), j.repository_id, j.run_id, j.run_attempt, j.workflow, j.job_name, j.head_sha,
	j.event, j.labels, j.status, COALESCE(j.conclusion, ''),
	COALESCE((SELECT t.wait_reason FROM slot_tickets t WHERE t.repository = r.full_name AND t.run_id = j.run_id
		AND t.run_attempt = j.run_attempt AND t.job = j.job_name LIMIT 1), j.wait_reason, ''),
	COALESCE(j.wait_text, ''), COALESCE(j.placed_machine_id, ''), COALESCE(j.runner_name, ''),
	COALESCE((SELECT x.backend FROM runners x WHERE x.machine_id = j.placed_machine_id AND x.name = j.runner_name), ''),
	COALESCE((SELECT x.isolation FROM runners x WHERE x.machine_id = j.placed_machine_id AND x.name = j.runner_name), ''),
	COALESCE(j.created_at_github, ''), COALESCE(j.started_at_github, ''), COALESCE(j.completed_at_github, ''),
	COALESCE((SELECT l.queued_at FROM slot_log l WHERE l.event = 'taken' AND l.repository = r.full_name AND l.run_id = j.run_id
		AND l.run_attempt = j.run_attempt AND l.job = j.job_name ORDER BY l.id DESC LIMIT 1), ''),
	COALESCE((SELECT l.slot_at FROM slot_log l WHERE l.event = 'taken' AND l.repository = r.full_name AND l.run_id = j.run_id
		AND l.run_attempt = j.run_attempt AND l.job = j.job_name ORDER BY l.id DESC LIMIT 1), '')`

const jobFrom = ` FROM gh_jobs j LEFT JOIN repositories r ON r.id = j.repository_id `

func scanJob(sc interface{ Scan(...any) error }) (slots.JobRecord, error) {
	var j slots.JobRecord
	var labels, status, created, started, completed, queued, slot string
	if err := sc.Scan(&j.ID, &j.Repository, &j.RepositoryID, &j.RunID, &j.RunAttempt, &j.Workflow, &j.JobName, &j.HeadSHA,
		&j.Trigger, &labels, &status, &j.Conclusion, &j.WaitReason, &j.WaitText, &j.MachineID, &j.RunnerName, &j.Backend, &j.Isolation,
		&created, &started, &completed, &queued, &slot); err != nil {
		return j, err
	}
	_ = json.Unmarshal([]byte(labels), &j.Labels)
	if j.Labels == nil {
		j.Labels = []string{}
	}
	switch status {
	case "completed":
		j.State = slots.JobDone
	case "in_progress":
		j.State = slots.JobRunning
	default:
		j.State = slots.JobQueued
	}
	opt := func(s string) time.Time {
		if s == "" {
			return time.Time{}
		}
		return parseTime(s)
	}
	j.CreatedAt, j.StartedAt, j.CompletedAt, j.QueuedAt, j.SlotAt = opt(created), opt(started), opt(completed), opt(queued), opt(slot)
	return j, nil
}

// ListJobs returns up to Limit+1 jobs, newest first (by GitHub job id, which grows with time).
func (s *Store) ListJobs(ctx context.Context, f slots.JobFilter) ([]slots.JobRecord, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var where []string
	var args []any
	switch f.State {
	case slots.JobQueued:
		where = append(where, `j.status = 'queued'`)
	case slots.JobRunning:
		where = append(where, `j.status = 'in_progress'`)
	case slots.JobDone:
		where = append(where, `j.status = 'completed'`)
	}
	if f.RepositoryID != 0 {
		where, args = append(where, `j.repository_id = ?`), append(args, f.RepositoryID)
	}
	if f.MachineID != "" {
		where, args = append(where, `j.placed_machine_id = ?`), append(args, f.MachineID)
	}
	if f.Backend != "" {
		where, args = append(where, `EXISTS (SELECT 1 FROM runners x WHERE x.machine_id = j.placed_machine_id AND x.name = j.runner_name AND x.backend = ?)`), append(args, f.Backend)
	}
	if f.Result != "" {
		where, args = append(where, `j.conclusion = ?`), append(args, f.Result)
	}
	if !f.From.IsZero() {
		where, args = append(where, `j.created_at_github >= ?`), append(args, fmtTime(f.From))
	}
	if !f.To.IsZero() {
		where, args = append(where, `j.created_at_github <= ?`), append(args, fmtTime(f.To))
	}
	if f.After != 0 {
		where, args = append(where, `j.id < ?`), append(args, f.After)
	}
	q := `SELECT ` + jobCols + jobFrom
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	limit := f.Limit
	if limit < 1 {
		limit = 50
	}
	q += ` ORDER BY j.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []slots.JobRecord
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("list jobs: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetJob returns one job attempt.
func (s *Store) GetJob(ctx context.Context, id int64) (slots.JobRecord, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE j.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return slots.JobRecord{}, false, nil
	}
	if err != nil {
		return slots.JobRecord{}, false, fmt.Errorf("get job: %w", err)
	}
	return j, true, nil
}

// MachineExists says whether the machine id is known and not removed.
func (s *Store) MachineExists(ctx context.Context, id string) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM machines WHERE id = ? AND removed_at IS NULL`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("machine exists: %w", err)
	}
	return n > 0, nil
}

// UpsertJobForTests writes a GitHub job row for tests of packages that may not use database/sql.
func (s *Store) UpsertJobForTests(ctx context.Context, j slots.JobRecord, status string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	labels, _ := json.Marshal(j.Labels)
	ts := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return fmtTime(t)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO gh_jobs (id, repository_id, run_id, run_attempt, workflow, job_name, head_sha, event, labels,
		status, conclusion, runner_name, placed_machine_id, wait_reason, wait_text, created_at_github, started_at_github, completed_at_github)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, conclusion = excluded.conclusion`,
		j.ID, j.RepositoryID, j.RunID, j.RunAttempt, j.Workflow, j.JobName, j.HeadSHA, j.Trigger, string(labels), status,
		nullStr(j.Conclusion), nullStr(j.RunnerName), nullStr(j.MachineID), nullStr(j.WaitReason), nullStr(j.WaitText),
		ts(j.CreatedAt), ts(j.StartedAt), ts(j.CompletedAt))
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
