package admit

import (
	"fmt"
	"sort"
	"time"
)

// Limits are the reservation settings (R5.93): a waiting job becomes the
// machine's reserved job after Passes passes or after Wait, whichever first.
type Limits struct {
	Passes int
	Wait   time.Duration
}

// Defaults of the reservation settings.
const (
	DefaultPasses      = 3
	DefaultWaitMinutes = 10
)

// DefaultLimits is 3 passes or 10 minutes.
func DefaultLimits() Limits {
	return Limits{Passes: DefaultPasses, Wait: DefaultWaitMinutes * time.Minute}
}

// Reservation is the one reserved job of a machine.
type Reservation struct {
	MachineID string
	TicketID  string
	Req       Req
	Class     string
	Since     time.Time
	// Expected is the expected start; ExpectedKnown is false when a held job has
	// no forecast end and the start cannot be given.
	Expected      time.Time
	ExpectedKnown bool
}

// Text is the line a reserved job shows: "reserved since HH:MM, expected start
// HH:MM". Times are shown in zone (the owner's).
func (r Reservation) Text(zone *time.Location) string {
	if zone == nil {
		zone = time.UTC
	}
	since := r.Since.In(zone).Format("15:04")
	if !r.ExpectedKnown {
		return fmt.Sprintf("reserved since %s, expected start not known yet", since)
	}
	return fmt.Sprintf("reserved since %s, expected start %s", since, r.Expected.In(zone).Format("15:04"))
}

// ExpectedStart is when the job could start: now when the free pool already
// holds it, else the forecast end of the first held job whose end leaves room.
// known is false when a job that must end has no forecast.
func ExpectedStart(need Req, pool Pool, held []Held, now time.Time) (t time.Time, known bool) {
	if need.Fits(Free(pool, held)) {
		return now, true
	}
	ends := append([]Held(nil), held...)
	sort.SliceStable(ends, func(i, j int) bool {
		if ends[i].EndKnown != ends[j].EndKnown {
			return ends[i].EndKnown // known ends first
		}
		return ends[i].End.Before(ends[j].End)
	})
	room := Free(pool, held)
	for _, h := range ends {
		if !h.EndKnown {
			return time.Time{}, false
		}
		room.CPUs += h.Req.CPUs
		room.MemoryGiB += h.Req.MemoryGiB
		if need.Fits(room) {
			end := h.End
			if end.Before(now) {
				end = now
			}
			return end, true
		}
	}
	return time.Time{}, false
}

// Waiter is a waiting job of one machine, in the order of service.
type Waiter struct {
	ID       string
	Req      Req
	Class    string
	QueuedAt time.Time
	Passes   int
	// Blocked is true when the job fails a check now (it waits for room).
	Blocked bool
	// Could is true when it could run on this machine at all (CouldRunHere).
	Could bool
}

// Promote picks the waiter that becomes the machine's reserved job, or nil. It
// is nil while the machine already has a reservation (one at a time, R5.93).
// Waiters are in the order of service; only a blocked waiter that could run
// here is taken, the first that has Limits.Passes passes or has waited
// Limits.Wait.
func Promote(waiters []Waiter, have bool, lim Limits, now time.Time) *Waiter {
	if have {
		return nil
	}
	for i := range waiters {
		w := &waiters[i]
		if !w.Blocked || !w.Could {
			continue
		}
		if (lim.Passes > 0 && w.Passes >= lim.Passes) || (lim.Wait > 0 && now.Sub(w.QueuedAt) >= lim.Wait) {
			return w
		}
	}
	return nil
}

// Passed lists the waiters a job that starts now passes: those ahead of it in
// the order of service that are blocked and could run on this machine. Each of
// them gets one pass (R5.93).
func Passed(ahead []Waiter) []string {
	var ids []string
	for _, w := range ahead {
		if w.Blocked && w.Could {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

// Backfill says whether a job that is not the reserved one may start while the
// reservation holds (R5.94). It may when (a) its forecast end is before the
// reserved job's expected start, or (b) the pool still leaves room for the
// reserved job at that time. candEnd is the forecast end of the candidate
// (candEndKnown false: no forecast). A reservation whose start is not known
// allows only (b) against what is held now.
func Backfill(r Reservation, cand Req, candEnd time.Time, candEndKnown bool, pool Pool, held []Held) (bool, string) {
	if r.ExpectedKnown && candEndKnown && candEnd.Before(r.Expected) {
		return true, ""
	}
	used := cand
	for _, h := range held {
		if r.ExpectedKnown && h.EndKnown && !h.End.After(r.Expected) {
			continue // ends before the reserved job starts
		}
		used.CPUs += h.Req.CPUs
		used.MemoryGiB += h.Req.MemoryGiB
	}
	room := Req{pool.CPUs - used.CPUs, pool.MemoryGiB - used.MemoryGiB}
	if r.Req.Fits(room) {
		return true, ""
	}
	return false, "a job is reserved on this machine; " + r.Text(nil)
}

// CanEverRun is true when some runner of the machine could run the reserved
// job: its ceiling holds the job and the pool holds it. When it is false the
// Doctor says so, as it does for unserved size labels (scheduling R5.67).
func CanEverRun(need Req, ceilings []Ceiling, pool Pool) bool {
	if need.CPUs > pool.CPUs || need.MemoryGiB > pool.MemoryGiB {
		return false
	}
	for _, c := range ceilings {
		if need.Fits(c.Req) {
			return true
		}
	}
	return false
}

// UnservedText is the Doctor's line for a reserved job no runner can run.
func UnservedText(machine, class string, need Req) string {
	return fmt.Sprintf("A job on %s needs class %s (%d CPUs, %d GiB) and no runner there can run it. Add a runner of that class or raise the pool on the machine page.",
		machine, orNone(class), need.CPUs, need.MemoryGiB)
}
