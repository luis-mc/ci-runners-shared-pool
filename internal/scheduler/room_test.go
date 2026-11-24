package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

var roomNow = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func roomIn(need admit.Req, held ...RoomHeld) RoomInput {
	return RoomInput{Machine: "M1", Req: need, Class: "large", Pool: admit.Pool{CPUs: 8, MemoryGiB: 16}, Held: held, LoadOK: true, JobKey: "9", Now: roomNow}
}

func TestCheckRoomFitsAndSaysWhyNot(t *testing.T) {
	if d := CheckRoom(roomIn(admit.Req{CPUs: 6, MemoryGiB: 4})); !d.OK {
		t.Fatalf("an empty pool holds it: %+v", d)
	}
	d := CheckRoom(roomIn(admit.Req{CPUs: 6, MemoryGiB: 4}, RoomHeld{Req: admit.Req{CPUs: 3, MemoryGiB: 2}}))
	if d.OK || d.Why != RoomPool || d.Text != "waiting for room on M1: needs 6 CPU, 5 free" {
		t.Fatalf("decision: %+v", d)
	}
	d = CheckRoom(roomIn(admit.Req{CPUs: 2, MemoryGiB: 12}, RoomHeld{Req: admit.Req{CPUs: 1, MemoryGiB: 8}}))
	if d.Text != "waiting for room on M1: needs 12 GiB, 8 free" {
		t.Fatalf("memory short: %q", d.Text)
	}
}

func TestCheckRoomNeverCeilingAndLoad(t *testing.T) {
	if d := CheckRoom(roomIn(admit.Req{CPUs: 16, MemoryGiB: 4})); d.Why != RoomNever || !strings.Contains(d.Text, "ever") {
		t.Fatalf("never: %+v", d)
	}
	in := roomIn(admit.Req{CPUs: 6, MemoryGiB: 4})
	in.Ceiling, in.CeilingKnown = admit.Req{CPUs: 4, MemoryGiB: 8}, true
	if d := CheckRoom(in); d.Why != RoomCeiling {
		t.Fatalf("ceiling: %+v", d)
	}
	in = roomIn(admit.Req{CPUs: 1, MemoryGiB: 1})
	in.LoadOK, in.LoadText = false, "memory is at 91%, the budget is 80%"
	if d := CheckRoom(in); d.Why != RoomLoad || !strings.Contains(d.Text, "91%") {
		t.Fatalf("load: %+v", d)
	}
}

func TestCheckRoomReleasesWarmRunnersFirst(t *testing.T) {
	in := roomIn(admit.Req{CPUs: 6, MemoryGiB: 4},
		RoomHeld{Req: admit.Req{CPUs: 2, MemoryGiB: 2}},
		RoomHeld{Req: admit.Req{CPUs: 2, MemoryGiB: 2}, Warm: true, ID: "w1"},
		RoomHeld{Req: admit.Req{CPUs: 2, MemoryGiB: 2}, Warm: true, ID: "w2"})
	d := CheckRoom(in)
	if !d.OK || len(d.Release) != 2 {
		t.Fatalf("both warm runners go: %+v", d)
	}
	in.Req = admit.Req{CPUs: 4, MemoryGiB: 4}
	if d := CheckRoom(in); !d.OK || len(d.Release) != 1 || d.Release[0] != "w1" {
		t.Fatalf("only what the job needs: %+v", d)
	}
	in.Held = in.Held[:1]
	in.Req = admit.Req{CPUs: 8, MemoryGiB: 4}
	if d := CheckRoom(in); d.OK {
		t.Fatalf("a real job's room is never released: %+v", d)
	}
}

func TestCheckRoomReservationAndBackfill(t *testing.T) {
	res := &admit.Reservation{MachineID: "m1", TicketID: "5", Req: admit.Req{CPUs: 6, MemoryGiB: 1}, Since: roomNow}
	in := roomIn(admit.Req{CPUs: 2, MemoryGiB: 1}, RoomHeld{Req: admit.Req{CPUs: 4, MemoryGiB: 1}})
	in.Reservation = res
	if d := CheckRoom(in); d.OK || d.Why != RoomReserved {
		t.Fatalf("a later job waits for the reserved one: %+v", d)
	}
	in.JobKey = "5"
	in.Req = res.Req
	in.Held = nil
	if d := CheckRoom(in); !d.OK {
		t.Fatalf("the reserved job itself goes: %+v", d)
	}
	// Backfill: it ends before the reserved job's expected start.
	res.Expected, res.ExpectedKnown = roomNow.Add(10*time.Minute), true
	in = roomIn(admit.Req{CPUs: 2, MemoryGiB: 1}, RoomHeld{Req: admit.Req{CPUs: 4, MemoryGiB: 1}})
	in.Reservation, in.End, in.EndKnown = res, roomNow.Add(5*time.Minute), true
	if d := CheckRoom(in); !d.OK {
		t.Fatalf("backfill: %+v", d)
	}
}
