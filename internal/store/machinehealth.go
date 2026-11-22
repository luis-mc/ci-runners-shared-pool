package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// MachineHealthRow is the last health report of a machine and the OOM state
// the controller derived from it (machines R5.70).
type MachineHealthRow struct {
	MachineID  string
	ReceivedAt time.Time
	// Report is the protojson of the HealthReport; the store does not read it.
	Report    string
	OOMTotal  int64
	LastOOMAt time.Time
}

// DiskSample is the free space of one volume at one time.
type DiskSample struct {
	Role, Path       string
	At               time.Time
	FreeBytes, Total int64
}

// PutMachineHealth replaces the last health report of a machine.
func (s *Store) PutMachineHealth(ctx context.Context, r MachineHealthRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO machine_health (machine_id, received_at, report, oom_total, last_oom_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (machine_id) DO UPDATE SET received_at = excluded.received_at, report = excluded.report,
  oom_total = excluded.oom_total, last_oom_at = excluded.last_oom_at`,
		r.MachineID, fmtTime(r.ReceivedAt), r.Report, r.OOMTotal, fmtTimeOpt(r.LastOOMAt))
	if err != nil {
		return fmt.Errorf("put machine health: %w", err)
	}
	return nil
}

// GetMachineHealth returns the last health report of a machine; ok is false when there is none.
func (s *Store) GetMachineHealth(ctx context.Context, machineID string) (MachineHealthRow, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT machine_id, received_at, report, oom_total, last_oom_at FROM machine_health WHERE machine_id = ?`, machineID)
	r, err := scanHealth(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MachineHealthRow{}, false, nil
	}
	return r, err == nil, err
}

// ListMachineHealth returns the last health report of every machine.
func (s *Store) ListMachineHealth(ctx context.Context) ([]MachineHealthRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT machine_id, received_at, report, oom_total, last_oom_at FROM machine_health ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list machine health: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []MachineHealthRow
	for rows.Next() {
		r, err := scanHealth(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanHealth(sc scanner) (MachineHealthRow, error) {
	var r MachineHealthRow
	var at, oom string
	if err := sc.Scan(&r.MachineID, &at, &r.Report, &r.OOMTotal, &oom); err != nil {
		return MachineHealthRow{}, err
	}
	r.ReceivedAt, r.LastOOMAt = parseTime(at), parseTimeOpt(oom)
	return r, nil
}

// DeleteMachineHealth drops a machine's health rows (the machine was removed).
func (s *Store) DeleteMachineHealth(ctx context.Context, machineID string) error {
	return s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, _ *sqlcgen.Queries) error {
		for _, q := range []string{`DELETE FROM machine_health WHERE machine_id = ?`, `DELETE FROM machine_disk_samples WHERE machine_id = ?`} {
			if _, err := tx.ExecContext(ctx, q, machineID); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddDiskSamples stores the free space of the volumes of one machine at one time.
// A sample at a time that exists is replaced.
func (s *Store) AddDiskSamples(ctx context.Context, machineID string, samples []DiskSample) error {
	return s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, _ *sqlcgen.Queries) error {
		for _, d := range samples {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO machine_disk_samples (machine_id, role, path, sampled_at, free_bytes, total_bytes)
VALUES (?, ?, ?, ?, ?, ?)`, machineID, d.Role, d.Path, fmtTime(d.At), d.FreeBytes, d.Total); err != nil {
				return fmt.Errorf("add disk sample: %w", err)
			}
		}
		return nil
	})
}

// DiskSamples returns the samples of a machine since a time, oldest first.
func (s *Store) DiskSamples(ctx context.Context, machineID string, since time.Time) ([]DiskSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role, path, sampled_at, free_bytes, total_bytes FROM machine_disk_samples
WHERE machine_id = ? AND sampled_at >= ? ORDER BY role, path, sampled_at`, machineID, fmtTime(since))
	if err != nil {
		return nil, fmt.Errorf("disk samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DiskSample
	for rows.Next() {
		var d DiskSample
		var at string
		if err := rows.Scan(&d.Role, &d.Path, &at, &d.FreeBytes, &d.Total); err != nil {
			return nil, err
		}
		d.At = parseTime(at)
		out = append(out, d)
	}
	return out, rows.Err()
}

// PruneDiskSamples deletes the samples older than before and returns how many.
func (s *Store) PruneDiskSamples(ctx context.Context, before time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM machine_disk_samples WHERE sampled_at < ?`, fmtTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune disk samples: %w", err)
	}
	return r.RowsAffected()
}

// JobEnd reasons crsp records when it ends a job itself.
const (
	JobEndTimeout    = "timeout"     // the crsp job timer
	JobEndOverMemory = "over_memory" // the job went over its memory
	JobEndStopped    = "stopped"     // crsp stopped it (a drain, a Stop, a removal)
)

// RecordJobEndReason says why crsp ended a job. The first reason of a job stays.
func (s *Store) RecordJobEndReason(ctx context.Context, jobID int64, reason, detail string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO job_end_reasons (job_id, reason, detail, recorded_at) VALUES (?, ?, ?, ?)`,
		jobID, reason, detail, fmtTime(at))
	if err != nil {
		return fmt.Errorf("record job end reason: %w", err)
	}
	return nil
}
