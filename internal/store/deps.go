package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
)

// DepsJobStore keeps the dependency setup jobs (migration 00044, CRSP248). It
// is the depsetup.Repo of the controller.
type DepsJobStore struct{ s *Store }

var _ depsetup.Repo = (*DepsJobStore)(nil)

// DepsJobs returns the dependency job store.
func (s *Store) DepsJobs() *DepsJobStore { return &DepsJobStore{s} }

type depsExtra struct {
	AcceptLicence bool     `json:"accept_licence,omitempty"`
	ManualSteps   []string `json:"manual_steps,omitempty"`
	RestartSent   bool     `json:"restart_sent,omitempty"`
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// Put inserts or replaces a job.
func (d *DepsJobStore) Put(ctx context.Context, j depsetup.Job) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	last := ""
	if j.Last != nil {
		last = mustJSON(j.Last)
	}
	log := j.Log
	if len(log) > depsetup.LogCap {
		log = log[len(log)-depsetup.LogCap:]
	}
	if log == nil {
		log = []depsetup.Event{}
	}
	added := j.Added
	if added == nil {
		added = []depsetup.Added{}
	}
	items := j.ItemIDs
	if items == nil {
		items = []string{}
	}
	_, err := d.s.db.ExecContext(ctx, `INSERT INTO dependency_jobs
(id, machine_id, state, choice_id, item_ids, runner_os, runner_kind, actor, created_at, updated_at, error, engine_version, added, last_event, log, extra)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET state = excluded.state, item_ids = excluded.item_ids, updated_at = excluded.updated_at,
  error = excluded.error, engine_version = excluded.engine_version, added = excluded.added, last_event = excluded.last_event,
  log = excluded.log, extra = excluded.extra`,
		j.ID, j.MachineID, j.State, j.ChoiceID, mustJSON(items), j.RunnerOS, j.RunnerKind, j.Actor, fmtTime(j.CreatedAt), fmtTime(j.UpdatedAt),
		j.Error, j.EngineVersion, mustJSON(added), last, mustJSON(log),
		mustJSON(depsExtra{AcceptLicence: j.AcceptLicence, ManualSteps: j.ManualSteps, RestartSent: j.RestartSent}))
	if err != nil {
		return fmt.Errorf("put dependency job: %w", err)
	}
	return nil
}

const depsCols = `id, machine_id, state, choice_id, item_ids, runner_os, runner_kind, actor, created_at, updated_at, error, engine_version, added, last_event, log, extra`

func scanDepsJob(r rowScanner) (depsetup.Job, error) {
	var j depsetup.Job
	var items, created, updated, added, last, log, extra string
	if err := r.Scan(&j.ID, &j.MachineID, &j.State, &j.ChoiceID, &items, &j.RunnerOS, &j.RunnerKind, &j.Actor, &created, &updated,
		&j.Error, &j.EngineVersion, &added, &last, &log, &extra); err != nil {
		return j, err
	}
	j.CreatedAt, j.UpdatedAt = parseTime(created), parseTime(updated)
	_ = json.Unmarshal([]byte(items), &j.ItemIDs)
	_ = json.Unmarshal([]byte(added), &j.Added)
	_ = json.Unmarshal([]byte(log), &j.Log)
	if last != "" {
		var e depsetup.Event
		if json.Unmarshal([]byte(last), &e) == nil {
			j.Last = &e
		}
	}
	var x depsExtra
	_ = json.Unmarshal([]byte(extra), &x)
	j.AcceptLicence, j.ManualSteps, j.RestartSent = x.AcceptLicence, x.ManualSteps, x.RestartSent
	if j.Added == nil {
		j.Added = []depsetup.Added{}
	}
	if j.Log == nil {
		j.Log = []depsetup.Event{}
	}
	return j, nil
}

// Get returns one job of a machine.
func (d *DepsJobStore) Get(ctx context.Context, machineID, id string) (depsetup.Job, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	j, err := scanDepsJob(d.s.db.QueryRowContext(ctx, `SELECT `+depsCols+` FROM dependency_jobs WHERE id = ? AND machine_id = ?`, id, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return depsetup.Job{}, false, nil
	}
	return j, err == nil, err
}

func (d *DepsJobStore) query(ctx context.Context, q string, args ...any) ([]depsetup.Job, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := d.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []depsetup.Job
	for rows.Next() {
		j, err := scanDepsJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// List returns the jobs of a machine, newest first.
func (d *DepsJobStore) List(ctx context.Context, machineID string, limit int) ([]depsetup.Job, error) {
	if limit <= 0 {
		limit = depsetup.MaxList
	}
	return d.query(ctx, `SELECT `+depsCols+` FROM dependency_jobs WHERE machine_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, machineID, limit)
}

// Unfinished returns the jobs of a machine that hold it.
func (d *DepsJobStore) Unfinished(ctx context.Context, machineID string) ([]depsetup.Job, error) {
	return d.query(ctx, `SELECT `+depsCols+` FROM dependency_jobs WHERE machine_id = ?
		AND state IN ('consented', 'installing', 'paused_restart', 'verifying', 'preparing', 'undoing') ORDER BY created_at`, machineID)
}
