package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// The admission side of the scheduler store (migration 00037, scheduling R5.90
// to R5.94): the pool and the class table of a machine for the gate, the
// reserved job and the passes.

// ReserveDefaults are the settings of R5.93 when none is saved.
const (
	reserveDefaultPasses  = 3
	reserveDefaultMinutes = 10
)

func settingValue(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (string, bool) {
	var v string
	if err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// LoadClassSizes reads the saved class table; the built-in one when none.
func loadClassSizes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) runners.ClassSizes {
	c := runners.TableSizes()
	if v, ok := settingValue(ctx, q, slots.SettingSizeClasses); ok {
		var saved runners.ClassSizes
		if json.Unmarshal([]byte(v), &saved) == nil && saved.Validate() == nil {
			c = saved
		}
	}
	return c
}

func loadReserveLimits(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (passes, minutes int) {
	passes, minutes = reserveDefaultPasses, reserveDefaultMinutes
	if v, ok := settingValue(ctx, q, slots.SettingReservePasses); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			passes = n
		}
	}
	if v, ok := settingValue(ctx, q, slots.SettingReserveMinutes); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			minutes = n
		}
	}
	return passes, minutes
}

// loadAdmission fills the admission of a machine: on when its pool size is known.
func (t *schedTx) loadAdmission(machineID string) slots.Admission {
	var cpus int
	var gib float64
	err := t.tx.QueryRowContext(t.ctx, `SELECT pool_cpus, pool_memory_gib FROM machine_settings WHERE machine_id = ?`, machineID).Scan(&cpus, &gib)
	if err != nil || cpus < 1 || gib < 1 {
		return slots.Admission{}
	}
	a := slots.Admission{On: true, PoolCPUs: cpus, PoolMemoryGiB: int(gib)}
	for _, s := range loadClassSizes(t.ctx, t.tx).List() {
		a.Classes = append(a.Classes, slots.ClassCeiling{Class: string(s.Class), CPUs: s.CPUs, MemoryGiB: s.MemoryGiB})
	}
	a.AfterPasses, a.AfterMinutes = loadReserveLimits(t.ctx, t.tx)
	return a
}

func (t *schedTx) Reservation(machineID string) (slots.Reservation, bool, error) {
	var r slots.Reservation
	var since, exp string
	err := t.tx.QueryRowContext(t.ctx, `SELECT machine_id, ticket_id, cpus, mem_gib, class, since, expected
		FROM sched_reservations WHERE machine_id = ?`, machineID).Scan(&r.MachineID, &r.TicketID, &r.CPUs, &r.MemoryGiB, &r.Class, &since, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return slots.Reservation{}, false, nil
	}
	if err != nil {
		return slots.Reservation{}, false, fmt.Errorf("reservation: %w", err)
	}
	r.Since = parseTime(since)
	if exp != "" {
		r.Expected = parseTime(exp)
	}
	return r, true, nil
}

func (t *schedTx) SetReservation(r slots.Reservation) error {
	exp := ""
	if !r.Expected.IsZero() {
		exp = fmtTime(r.Expected)
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO sched_reservations (machine_id, ticket_id, cpus, mem_gib, class, since, expected)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(machine_id) DO UPDATE SET ticket_id = excluded.ticket_id, cpus = excluded.cpus,
		mem_gib = excluded.mem_gib, class = excluded.class, since = excluded.since, expected = excluded.expected`,
		r.MachineID, r.TicketID, r.CPUs, r.MemoryGiB, r.Class, fmtTime(r.Since), exp)
	return err
}

func (t *schedTx) ClearReservation(machineID string) error {
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM sched_reservations WHERE machine_id = ?`, machineID)
	return err
}

func (t *schedTx) Passes(machineID string) (map[string]int, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT ticket_id, passes FROM sched_passes WHERE machine_id = ?`, machineID)
	if err != nil {
		return nil, fmt.Errorf("passes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (t *schedTx) AddPass(machineID, ticketID string) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO sched_passes (ticket_id, machine_id, passes) VALUES (?, ?, 1)
		ON CONFLICT(ticket_id) DO UPDATE SET passes = passes + 1`, ticketID, machineID)
	return err
}

func (t *schedTx) DropPasses(ticketID string) error {
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM sched_passes WHERE ticket_id = ?`, ticketID)
	return err
}

// Reservations lists the reserved jobs of every machine, for the Doctor and
// the console (scheduling R5.93).
func (s *Store) Reservations(ctx context.Context) ([]slots.Reservation, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT machine_id, ticket_id, cpus, mem_gib, class, since, expected FROM sched_reservations ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("reservations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []slots.Reservation
	for rows.Next() {
		var r slots.Reservation
		var since, exp string
		if err := rows.Scan(&r.MachineID, &r.TicketID, &r.CPUs, &r.MemoryGiB, &r.Class, &since, &exp); err != nil {
			return nil, err
		}
		r.Since = parseTime(since)
		if exp != "" {
			r.Expected = parseTime(exp)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}


// SetPoolForTests sets the pool size of a machine that already has settings.
func (s *Store) SetPoolForTests(ctx context.Context, machineID string, cpus, gib int) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `UPDATE machine_settings SET pool_cpus = ?, pool_memory_gib = ? WHERE machine_id = ?`, cpus, gib, machineID)
	return err
}

// ClassSizes reads the saved class table of the runners; the built-in one when
// none is saved (scheduling R5.89).
func (s *Store) ClassSizes(ctx context.Context) runners.ClassSizes {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return loadClassSizes(ctx, s.db)
}

// SaveClassSizes saves the class table. The caller checked it (runners.CheckSave).
func (s *Store) SaveClassSizes(ctx context.Context, c runners.ClassSizes, by string) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.putSetting(ctx, slots.SettingSizeClasses, string(b), by)
}

// ReserveLimits reads the reservation settings: passes and minutes.
func (s *Store) ReserveLimits(ctx context.Context) (passes, minutes int) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return loadReserveLimits(ctx, s.db)
}

// SaveReserveLimits saves the reservation settings.
func (s *Store) SaveReserveLimits(ctx context.Context, passes, minutes int, by string) error {
	if err := s.putSetting(ctx, slots.SettingReservePasses, strconv.Itoa(passes), by); err != nil {
		return err
	}
	return s.putSetting(ctx, slots.SettingReserveMinutes, strconv.Itoa(minutes), by)
}

func (s *Store) putSetting(ctx context.Context, key, value, by string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if by == "" {
		by = "system"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_by) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_by = excluded.updated_by,
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, key, value, by)
	return err
}
