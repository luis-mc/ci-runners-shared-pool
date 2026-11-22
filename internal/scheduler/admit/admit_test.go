package admit

import (
	"strings"
	"testing"
	"time"
)

var (
	t0   = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	pool = Pool{CPUs: 8, MemoryGiB: 16}
	med  = Ceiling{"medium", Req{4, 8}}
	lrg  = Ceiling{"large", Req{8, 16}}
)

func TestCheckCeilingThenPool(t *testing.T) {
	if v := Check("medium", Req{4, 8}, med, true, pool, nil); !v.OK() {
		t.Fatalf("fits: %+v", v)
	}
	if v := Check("large", Req{6, 8}, med, true, pool, nil); v.Why != WhyCeiling {
		t.Fatalf("ceiling: %+v", v)
	}
	held := []Held{{Req: Req{6, 4}}}
	if v := Check("medium", Req{4, 4}, med, true, pool, held); v.Why != WhyPool || !strings.Contains(v.Text, "2 CPUs and 12 GiB free") {
		t.Fatalf("cpu short: %+v", v)
	}
	held = []Held{{Req: Req{1, 14}}}
	if v := Check("medium", Req{2, 4}, med, true, pool, held); v.Why != WhyPool {
		t.Fatalf("memory short: %+v", v)
	}
	if v := Check("", Req{12, 4}, lrg, true, pool, nil); v.Why != WhyNever {
		t.Fatalf("never: %+v", v)
	}
	if v := Check("large", Req{6, 8}, Ceiling{}, false, pool, nil); !v.OK() {
		t.Fatal("unknown runner class is not judged by the ceiling")
	}
}

func TestFreeNeverBelowZero(t *testing.T) {
	f := Free(Pool{2, 2}, []Held{{Req: Req{3, 1}}, {Req: Req{1, 5}}})
	if f != (Req{0, 0}) {
		t.Fatalf("got %+v", f)
	}
}

func TestPromoteByPassesOrByTime(t *testing.T) {
	lim := DefaultLimits()
	w := []Waiter{
		{ID: "a", Blocked: false, Could: true, Passes: 9},
		{ID: "b", Blocked: true, Could: false, Passes: 9},
		{ID: "c", Blocked: true, Could: true, Passes: 2, QueuedAt: t0},
		{ID: "d", Blocked: true, Could: true, Passes: 3, QueuedAt: t0},
	}
	if p := Promote(w, false, lim, t0.Add(time.Minute)); p == nil || p.ID != "d" {
		t.Fatalf("3 passes: %+v", p)
	}
	if p := Promote(w, false, lim, t0.Add(10*time.Minute)); p == nil || p.ID != "c" {
		t.Fatalf("10 minutes, first in order: %+v", p)
	}
	if p := Promote(w, false, lim, t0.Add(9*time.Minute+59*time.Second)); p == nil || p.ID != "d" {
		t.Fatal("under 10 minutes only passes count")
	}
	if Promote(w, true, lim, t0.Add(time.Hour)) != nil {
		t.Fatal("one reservation at a time")
	}
	w[2].Passes, w[3].Passes = 0, 0
	if Promote(w, false, lim, t0.Add(time.Minute)) != nil {
		t.Fatal("nothing due yet")
	}
}

func TestPassedCountsOnlyBlockedJobsThatCouldRun(t *testing.T) {
	ids := Passed([]Waiter{{ID: "x", Blocked: true, Could: true}, {ID: "y", Blocked: true}, {ID: "z", Could: true}})
	if len(ids) != 1 || ids[0] != "x" {
		t.Fatalf("got %v", ids)
	}
}

func TestExpectedStartFromForecastEnds(t *testing.T) {
	held := []Held{
		{Req: Req{4, 8}, End: t0.Add(20 * time.Minute), EndKnown: true},
		{Req: Req{4, 8}, End: t0.Add(5 * time.Minute), EndKnown: true},
	}
	need := Req{6, 12}
	got, ok := ExpectedStart(need, pool, held, t0)
	if !ok || !got.Equal(t0.Add(20*time.Minute)) {
		t.Fatalf("both must end: %v %v", got, ok)
	}
	got, ok = ExpectedStart(Req{4, 8}, pool, held, t0)
	if !ok || !got.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("one must end: %v %v", got, ok)
	}
	if got, ok = ExpectedStart(Req{1, 1}, pool, nil, t0); !ok || !got.Equal(t0) {
		t.Fatal("fits now")
	}
	held[0].EndKnown = false
	if _, ok = ExpectedStart(need, pool, held, t0); ok {
		t.Fatal("no forecast, no start")
	}
}

func TestReservationText(t *testing.T) {
	r := Reservation{Since: t0, Expected: t0.Add(25 * time.Minute), ExpectedKnown: true}
	if got := r.Text(nil); got != "reserved since 10:00, expected start 10:25" {
		t.Fatal(got)
	}
	r.ExpectedKnown = false
	if got := r.Text(time.UTC); !strings.HasPrefix(got, "reserved since 10:00, expected start") {
		t.Fatal(got)
	}
	zone := time.FixedZone("x", 3600)
	r.ExpectedKnown, r.Expected = true, t0.Add(30*time.Minute)
	if got := r.Text(zone); got != "reserved since 11:00, expected start 11:30" {
		t.Fatal(got)
	}
}

func TestBackfillEndsBeforeOrLeavesRoom(t *testing.T) {
	held := []Held{{Req: Req{6, 12}, End: t0.Add(30 * time.Minute), EndKnown: true}}
	r := Reservation{Req: Req{8, 16}, Expected: t0.Add(30 * time.Minute), ExpectedKnown: true}
	if ok, _ := Backfill(r, Req{2, 4}, t0.Add(10*time.Minute), true, pool, held); !ok {
		t.Fatal("(a) ends before the reserved start")
	}
	if ok, why := Backfill(r, Req{2, 4}, t0.Add(40*time.Minute), true, pool, held); ok || !strings.Contains(why, "reserved") {
		t.Fatalf("runs past the start: %v %q", ok, why)
	}
	if ok, _ := Backfill(r, Req{2, 4}, time.Time{}, false, pool, held); ok {
		t.Fatal("no forecast and no room")
	}
	// (b) the held job ends before the start and the candidate fits beside the reserved one.
	r2 := Reservation{Req: Req{4, 8}, Expected: t0.Add(30 * time.Minute), ExpectedKnown: true}
	if ok, _ := Backfill(r2, Req{4, 8}, t0.Add(90*time.Minute), true, pool, held); !ok {
		t.Fatal("(b) room for both then")
	}
}

func TestBackfillUnknownStartUsesWhatIsHeldNow(t *testing.T) {
	r := Reservation{Req: Req{4, 8}}
	held := []Held{{Req: Req{4, 8}}}
	if ok, _ := Backfill(r, Req{4, 8}, time.Time{}, false, pool, held); ok {
		t.Fatal("no room")
	}
	if ok, _ := Backfill(r, Req{2, 4}, time.Time{}, false, pool, nil); !ok {
		t.Fatal("room for both")
	}
}

func TestCanEverRunAndDoctorLine(t *testing.T) {
	if CanEverRun(Req{8, 16}, []Ceiling{med}, pool) {
		t.Fatal("medium runner cannot")
	}
	if !CanEverRun(Req{8, 16}, []Ceiling{med, lrg}, pool) {
		t.Fatal("large runner can")
	}
	if CanEverRun(Req{8, 16}, []Ceiling{lrg}, Pool{6, 9}) {
		t.Fatal("pool too small")
	}
	if !strings.Contains(UnservedText("pc", "large", Req{8, 16}), "no runner there can run it") {
		t.Fatal("text")
	}
	if !CouldRunHere(Req{4, 4}, med, true, pool) || CouldRunHere(Req{6, 4}, med, true, pool) || CouldRunHere(Req{9, 4}, Ceiling{}, false, pool) {
		t.Fatal("CouldRunHere")
	}
}
