package slots

import "sort"

// Ordered is a ticket with its place in the order of service.
type Ordered struct {
	Ticket Ticket
	// Place is 1 for first in the order of service, counting passed-over tickets.
	Place int
	// PassedOver is true when the ticket's repository is at its parallel limit (R5.10).
	PassedOver bool
}

// Order sorts the tickets of one machine into the order of service: higher
// repository priority first, then lower event class, then fair share (the
// repository with the fewest jobs running in the pool first), then the earlier
// queued time, then the random suffix (R5.8, R5.9). A ticket whose repository
// already runs its maximum parallel jobs is marked passed over and keeps its
// place (R5.10). running is jobs running per repository in the pool; limits is
// the maximum parallel jobs per repository (absent or 0: no limit).
func Order(tickets []Ticket, running map[string]int, limits map[string]int) []Ordered {
	ts := append([]Ticket(nil), tickets...)
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i], ts[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.EventClass != b.EventClass {
			return a.EventClass < b.EventClass
		}
		if ra, rb := running[a.Job.Repository], running[b.Job.Repository]; ra != rb {
			return ra < rb
		}
		if a.QueuedAtNs != b.QueuedAtNs {
			return a.QueuedAtNs < b.QueuedAtNs
		}
		return a.Random < b.Random
	})
	out := make([]Ordered, len(ts))
	for i, t := range ts {
		lim := limits[t.Job.Repository]
		out[i] = Ordered{Ticket: t, Place: i + 1, PassedOver: lim > 0 && running[t.Job.Repository] >= lim}
	}
	return out
}

// FirstInLine is the first ticket of the order of service that is not passed
// over (R5.11). ok is false when there is none.
func FirstInLine(o []Ordered) (t Ticket, ok bool) {
	for _, x := range o {
		if !x.PassedOver {
			return x.Ticket, true
		}
	}
	return Ticket{}, false
}

// Ahead counts the tickets before id that are not passed over, for "N ahead in the queue".
func Ahead(o []Ordered, id string) int {
	n := 0
	for _, x := range o {
		if x.Ticket.ID == id {
			return n
		}
		if !x.PassedOver {
			n++
		}
	}
	return n
}
