package slots

import (
	"strconv"
	"strings"
	"time"
)

// ProcessInfo is one row of a process table.
type ProcessInfo struct {
	Parent int64
	Name   string
}

// shells skipped between the hook and the runner worker on Windows (R5.31).
var shells = map[string]bool{"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true}

// HolderPID is the process whose death means the job is gone: the override
// value when it is a positive number, else the hook's parent, skipping a cmd,
// powershell or pwsh shell on Windows (R5.31; machine_slots.py holder_pid).
// lookup returns the process table row of a pid.
func HolderPID(override string, parent int64, windows bool, lookup func(pid int64) (ProcessInfo, bool)) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(override), 10, 64); err == nil && n > 0 {
		return n
	}
	if !windows || lookup == nil {
		return parent
	}
	self, ok := lookup(parent)
	if !ok || self.Parent == 0 {
		return parent
	}
	if shells[strings.ToLower(self.Name)] {
		return self.Parent
	}
	return parent
}

// TicketExpiry is the time a ticket must be looked at again: the earlier of
// its slot-maximum-age end and its beat limit (R5.34).
func TicketExpiry(queuedAt, beatAt time.Time, s Settings) time.Time {
	age := queuedAt.Add(time.Duration(s.SlotMaxAgeMin) * time.Minute)
	beat := beatAt.Add(BeatLimit(s))
	if beat.Before(age) {
		return beat
	}
	return age
}

// BeatLimit is the larger of 4 poll intervals and 120 s (R5.34; beat_limit_sec).
func BeatLimit(s Settings) time.Duration {
	d := time.Duration(4*s.GatePollSec) * time.Second
	if d < 120*time.Second {
		d = 120 * time.Second
	}
	return d
}

// SlotExpiry is when a slot taken at takenAt is stale by age (R5.33).
func SlotExpiry(takenAt time.Time, s Settings) time.Time {
	return takenAt.Add(time.Duration(s.SlotMaxAgeMin) * time.Minute)
}

// LeaseLimit is how long a slot lease lasts without a renewal: the same limit
// as a ticket's beat (R5.34, R5.70).
func LeaseLimit(s Settings) time.Duration { return BeatLimit(s) }

// LeaseExpiry is when a lease renewed at renewedAt ends (R5.70).
func LeaseExpiry(renewedAt time.Time, s Settings) time.Time {
	return renewedAt.Add(LeaseLimit(s))
}

// LeaseEnd is the end of a slot's lease. A slot with no lease end (a row from
// before leases) is judged by its age limit only, so nothing is freed on a guess.
func LeaseEnd(s Slot) time.Time {
	if s.LeaseExpiresAt.IsZero() {
		return s.ExpiresAt
	}
	return s.LeaseExpiresAt
}
