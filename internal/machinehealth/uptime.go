package machinehealth

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Why a machine was offline (machines R5.83). The reasons of a machine that is online but takes no
// job are the ones of machines.ComputeAvailability (R5.62).
const (
	OfflineHeartbeatLost     = "heartbeat_lost"     // no heartbeat for 30 seconds
	OfflineClockSkew         = "clock_skew"         // the last heartbeat was dated ahead of the controller clock
	OfflineControllerStopped = "controller_stopped" // the controller was not running, so nothing was seen
)

// PresenceRetention is how long the periods are kept; the longest window is 30 days.
const PresenceRetention = 31 * 24 * time.Hour

// UptimeWindows are the windows the view reports, shortest first.
var UptimeWindows = []struct {
	Name string
	Span time.Duration
}{{"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}}

// MaxPeriods is how many periods without jobs one view lists (the newest).
const MaxPeriods = 50

// UptimeWindow is the uptime of one machine over one window. It counts only the time the controller
// has watched the machine: Covered is shorter than the window for a machine that is newer than it.
type UptimeWindow struct {
	Name    string
	Covered time.Duration
	Online  time.Duration
	Taking  time.Duration
	// UptimePct is the share of Covered the machine was online (heartbeat); nil when nothing was watched.
	UptimePct *float64
	// TakingPct is the share of Covered it was online and took new jobs; nil when nothing was watched.
	TakingPct *float64
}

// Period is one stretch in which a machine took no new job.
type Period struct {
	Start time.Time
	// End is zero while the period goes on.
	End     time.Time
	Offline bool
	Reason  string
}

// Uptime is the uptime view of one machine.
type Uptime struct {
	MachineID string
	// Watched is false when the controller has no period of this machine yet.
	Watched bool
	Since   time.Time
	Windows []UptimeWindow
	// Periods holds the periods of the last 30 days in which the machine took no job, newest first.
	Periods []Period
}

// ComputeUptime reads the periods (oldest first; the first one may start before the window) at now.
func ComputeUptime(machineID string, rows []store.PresenceRow, now time.Time) Uptime {
	u := Uptime{MachineID: machineID}
	if len(rows) == 0 {
		for _, w := range UptimeWindows {
			u.Windows = append(u.Windows, UptimeWindow{Name: w.Name})
		}
		return u
	}
	u.Watched, u.Since = true, rows[0].At
	end := func(i int) time.Time {
		if i+1 < len(rows) {
			return rows[i+1].At
		}
		return now
	}
	for _, w := range UptimeWindows {
		from := now.Add(-w.Span)
		if rows[0].At.After(from) {
			from = rows[0].At
		}
		uw := UptimeWindow{Name: w.Name}
		for i, r := range rows {
			s, e := r.At, end(i)
			if s.Before(from) {
				s = from
			}
			if !e.After(s) {
				continue
			}
			d := e.Sub(s)
			uw.Covered += d
			if r.State != store.PresenceOffline {
				uw.Online += d
			}
			if r.State == store.PresenceTaking {
				uw.Taking += d
			}
		}
		if uw.Covered > 0 {
			up, tk := 100*float64(uw.Online)/float64(uw.Covered), 100*float64(uw.Taking)/float64(uw.Covered)
			uw.UptimePct, uw.TakingPct = &up, &tk
		}
		u.Windows = append(u.Windows, uw)
	}
	cut := now.Add(-UptimeWindows[len(UptimeWindows)-1].Span)
	for i, r := range rows {
		if r.State == store.PresenceTaking || !end(i).After(cut) {
			continue
		}
		p := Period{Start: r.At, Offline: r.State == store.PresenceOffline, Reason: r.Reason}
		if p.Start.Before(cut) {
			p.Start = cut
		}
		if i+1 < len(rows) {
			p.End = rows[i+1].At
		}
		u.Periods = append(u.Periods, p)
	}
	sort.SliceStable(u.Periods, func(a, b int) bool { return u.Periods[a].Start.After(u.Periods[b].Start) })
	if len(u.Periods) > MaxPeriods {
		u.Periods = u.Periods[:MaxPeriods]
	}
	return u
}

// Uptime builds the uptime view of one machine from its stored periods.
func (s *Service) Uptime(ctx context.Context, machineID string) (Uptime, error) {
	now := s.now()
	rows, err := s.Store.PresenceSince(ctx, machineID, now.Add(-UptimeWindows[len(UptimeWindows)-1].Span))
	if err != nil {
		return Uptime{}, err
	}
	return ComputeUptime(machineID, rows, now), nil
}

type presenceKey struct{ state, reason string }

// presenceMem remembers the last period written of each machine, so a steady machine writes nothing.
type presenceMem struct {
	mu        sync.Mutex
	last      map[string]presenceKey
	lastPrune time.Time
}

// Observe is called by the machines service with what it sees of a machine: whether it is online,
// the reason it takes no job (empty while it takes jobs; for an offline machine why it went offline),
// and its last heartbeat. It writes a row only when the state, or for a machine that is online the
// reason, changed since the last row. The first sight after the controller started closes the time the
// controller was not watching: a machine last seen taking is written offline (controller_stopped)
// from its last heartbeat.
func (s *Service) Observe(ctx context.Context, machineID string, online bool, reason string, lastSeen, at time.Time) {
	key := presenceKey{state: store.PresenceTaking}
	switch {
	case !online:
		key = presenceKey{state: store.PresenceOffline, reason: reason}
	case reason != "":
		key = presenceKey{state: store.PresenceNotTaking, reason: reason}
	}
	s.pres.mu.Lock()
	defer s.pres.mu.Unlock()
	if s.pres.last == nil {
		s.pres.last = map[string]presenceKey{}
	}
	prev, known := s.pres.last[machineID]
	if !known {
		row, ok, err := s.Store.LastPresence(ctx, machineID)
		if err != nil {
			return
		}
		if ok {
			prev = presenceKey{state: row.State, reason: row.Reason}
			if row.State != store.PresenceOffline {
				from := lastSeen
				if from.Before(row.At) || from.After(at) {
					from = row.At
				}
				if s.Store.AddPresence(ctx, store.PresenceRow{MachineID: machineID, At: from.Add(time.Millisecond), State: store.PresenceOffline, Reason: OfflineControllerStopped}) != nil {
					return
				}
				prev = presenceKey{state: store.PresenceOffline, reason: OfflineControllerStopped}
			}
		} else {
			prev = presenceKey{}
		}
	}
	same := prev.state == key.state && (key.state == store.PresenceOffline || prev.reason == key.reason)
	if !same {
		if s.Store.AddPresence(ctx, store.PresenceRow{MachineID: machineID, At: at, State: key.state, Reason: key.reason}) != nil {
			return
		}
	} else {
		key = prev
	}
	s.pres.last[machineID] = key
	if at.Sub(s.pres.lastPrune) >= time.Hour {
		s.pres.lastPrune = at
		_, _ = s.Store.PrunePresence(ctx, at.Add(-PresenceRetention))
	}
}
