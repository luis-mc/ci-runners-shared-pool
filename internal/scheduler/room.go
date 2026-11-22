package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

// The admission entry point of OPS80 (scheduling R5.90 to R5.94, runners
// R5.110). The assign controller asks it before it makes a runner for a queued
// job: is there room on this machine now? It is pure: it reads no clock and no
// store, so the controller keeps one lease table and this answers from what it
// is given. The gate inside the machine's queue (admission.go) still holds the
// hook's own check as a safety net.

// RoomWhy is why a job has no room.
type RoomWhy string

// The reasons of a RoomDecision.
const (
	RoomNone     RoomWhy = ""
	RoomPool     RoomWhy = "pool"     // pool CPU or memory does not cover the job
	RoomNever    RoomWhy = "never"    // the pool as a whole is too small
	RoomCeiling  RoomWhy = "ceiling"  // the machine's largest class is too small
	RoomLoad     RoomWhy = "load"     // the machine's load is over its budget, or not measured
	RoomReserved RoomWhy = "reserved" // another job holds the machine's reservation
)

// RoomHeld is one thing that holds pool room: a running job, a runner that is
// starting for a job, or a warm runner. Warm ones may be released for a real
// job (R5.66); the others never.
type RoomHeld struct {
	Req      admit.Req
	End      time.Time
	EndKnown bool
	Warm     bool
	// ID names a warm runner so the caller can release it.
	ID string
}

// RoomInput is everything CheckRoom reads.
type RoomInput struct {
	Machine string // the name the console shows, "M1"
	// Req is what the job needs; Class is the smallest class that holds it.
	Req   admit.Req
	Class string
	Pool  admit.Pool
	// Ceiling is the largest class a runner of the machine can be; zero means
	// no ceiling is known.
	Ceiling      admit.Req
	CeilingKnown bool
	Held         []RoomHeld
	// LoadOK is true when the machine's load is measured and under its budget;
	// LoadText says why not, in words.
	LoadOK   bool
	LoadText string
	// Reservation is the machine's reserved job, nil when none. JobKey is the
	// key of the job asked about; the reserved job itself is not held back.
	Reservation *admit.Reservation
	JobKey      string
	// End is the forecast end of this job (EndKnown false: no forecast).
	End      time.Time
	EndKnown bool
	Now      time.Time
}

// RoomDecision is the answer of CheckRoom.
type RoomDecision struct {
	OK  bool
	Why RoomWhy
	// Text is the console reason: "waiting for room on M1: needs 6 CPU, 5 free".
	Text string
	// Release lists the warm runners to release before the runner is made; they
	// are the ones the job needs room from, oldest first as given.
	Release []string
}

// CheckRoom decides whether a runner for the job may be made now (OPS80 flow
// step 2). The order is: the pool as a whole, the class ceiling, the load
// budget, the room in the pool (warm runners released first), then the
// reservation and backfill (R5.93, R5.94).
func CheckRoom(in RoomInput) RoomDecision {
	pool, need := in.Pool, in.Req
	if need.CPUs > pool.CPUs || need.MemoryGiB > pool.MemoryGiB {
		return RoomDecision{Why: RoomNever, Text: fmt.Sprintf("no room on %s ever: needs %s, the pool is %d CPU and %d GiB",
			in.Machine, needText(need, admit.Req{}, true), pool.CPUs, pool.MemoryGiB)}
	}
	if in.CeilingKnown && !need.Fits(in.Ceiling) {
		return RoomDecision{Why: RoomCeiling, Text: fmt.Sprintf("no runner on %s is big enough: needs class %s, the largest there holds %d CPU and %d GiB",
			in.Machine, classOrAbove(in.Class), in.Ceiling.CPUs, in.Ceiling.MemoryGiB)}
	}
	if !in.LoadOK {
		t := in.LoadText
		if t == "" {
			t = "load not measured"
		}
		return RoomDecision{Why: RoomLoad, Text: fmt.Sprintf("waiting for room on %s: %s", in.Machine, t)}
	}

	var hard, warm []admit.Held
	var warmIDs []string
	for _, h := range in.Held {
		ah := admit.Held{Req: h.Req, End: h.End, EndKnown: h.EndKnown}
		if h.Warm {
			warm = append(warm, ah)
			warmIDs = append(warmIDs, h.ID)
		} else {
			hard = append(hard, ah)
		}
	}
	all := append(append([]admit.Held(nil), hard...), warm...)
	free := admit.Free(pool, all)
	var release []string
	if !need.Fits(free) {
		// Release warm runners one at a time until the job fits.
		room := free
		for i, w := range warm {
			room.CPUs += w.Req.CPUs
			room.MemoryGiB += w.Req.MemoryGiB
			release = append(release, warmIDs[i])
			if need.Fits(room) {
				break
			}
		}
		if !need.Fits(room) {
			return RoomDecision{Why: RoomPool, Text: fmt.Sprintf("waiting for room on %s: needs %s", in.Machine, needText(need, free, false))}
		}
		// The job fits only with the warm runners gone: what remains held.
		free = room
	}

	// A reserved job keeps the room for itself; any other job starts only as
	// backfill (R5.94). The warm runners are released for the reserved job, so
	// the backfill check reads the hard holds only.
	if r := in.Reservation; r != nil && r.TicketID != in.JobKey {
		ok, _ := admit.Backfill(*r, need, in.End, in.EndKnown, pool, hard)
		if !ok {
			return RoomDecision{Why: RoomReserved, Text: fmt.Sprintf("waiting for room on %s: a job is reserved there; %s", in.Machine, r.Text(nil))}
		}
	}
	return RoomDecision{OK: true, Release: release}
}

// needText says what is short: "6 CPU, 5 free" or "12 GiB, 8 free", or both.
// all is true for the whole need with no free amount.
func needText(need, free admit.Req, all bool) string {
	if all {
		return fmt.Sprintf("%d CPU and %d GiB", need.CPUs, need.MemoryGiB)
	}
	var parts []string
	if need.CPUs > free.CPUs {
		parts = append(parts, fmt.Sprintf("%d CPU, %d free", need.CPUs, free.CPUs))
	}
	if need.MemoryGiB > free.MemoryGiB {
		parts = append(parts, fmt.Sprintf("%d GiB, %d free", need.MemoryGiB, free.MemoryGiB))
	}
	return strings.Join(parts, "; ")
}

func classOrAbove(c string) string {
	if c == "" {
		return "above xl"
	}
	return c
}
