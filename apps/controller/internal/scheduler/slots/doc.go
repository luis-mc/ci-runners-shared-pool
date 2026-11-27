// Package slots is the pure slot and ticket logic of the scheduler: settings
// and their validation, the order of service, light and heavy job matching,
// the holder process rule, stale rules and the slot-log row. It has no
// network and no database; internal/store implements Store, and
// internal/scheduler runs the gate on top of it.
//
// Ported from dev-standards claude/plugins/delivery/scripts/machine_slots.py
// (scheduling R5.1 to R5.11, R5.24 to R5.26, R5.31 to R5.34, R5.38, R5.39).
package slots
