package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Presence states of a machine (machines R5.83).
const (
	PresenceTaking    = "taking"     // online and takes new jobs
	PresenceNotTaking = "not_taking" // online, takes none; the reason says why
	PresenceOffline   = "offline"    // no heartbeat; the reason says why
)

// PresenceRow is the start of one period of a machine: it lasts until the next row.
type PresenceRow struct {
	MachineID string
	At        time.Time
	State     string
	Reason    string
}

// AddPresence stores the start of a period. A row at the same time is replaced.
func (s *Store) AddPresence(ctx context.Context, r PresenceRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO machine_presence (machine_id, at, state, reason) VALUES (?, ?, ?, ?)`,
		r.MachineID, fmtTime(r.At), r.State, r.Reason)
	if err != nil {
		return fmt.Errorf("add presence: %w", err)
	}
	return nil
}

// LastPresence returns the newest row of a machine; ok is false when there is none.
func (s *Store) LastPresence(ctx context.Context, machineID string) (PresenceRow, bool, error) {
	var r PresenceRow
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT machine_id, at, state, reason FROM machine_presence WHERE machine_id = ? ORDER BY at DESC LIMIT 1`,
		machineID).Scan(&r.MachineID, &at, &r.State, &r.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return PresenceRow{}, false, nil
	}
	if err != nil {
		return PresenceRow{}, false, fmt.Errorf("last presence: %w", err)
	}
	r.At = parseTime(at)
	return r, true, nil
}

// PresenceSince returns the rows that cover a window starting at since, oldest first:
// the row in force at since (when there is one) and every row after it.
func (s *Store) PresenceSince(ctx context.Context, machineID string, since time.Time) ([]PresenceRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT machine_id, at, state, reason FROM machine_presence
WHERE machine_id = ? AND at >= COALESCE((SELECT MAX(at) FROM machine_presence WHERE machine_id = ? AND at <= ?), '')
ORDER BY at`, machineID, machineID, fmtTime(since))
	if err != nil {
		return nil, fmt.Errorf("presence since: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PresenceRow
	for rows.Next() {
		var r PresenceRow
		var at string
		if err := rows.Scan(&r.MachineID, &at, &r.State, &r.Reason); err != nil {
			return nil, err
		}
		r.At = parseTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PrunePresence deletes the rows that ended before a time: a row is kept when it is the newest
// row at or before that time (it is still in force) or later. It returns how many were deleted.
func (s *Store) PrunePresence(ctx context.Context, before time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM machine_presence WHERE at < COALESCE(
  (SELECT MAX(p.at) FROM machine_presence p WHERE p.machine_id = machine_presence.machine_id AND p.at <= ?), '')`, fmtTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune presence: %w", err)
	}
	return r.RowsAffected()
}
