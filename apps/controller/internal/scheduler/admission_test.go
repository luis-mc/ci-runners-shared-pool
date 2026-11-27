package scheduler_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// poolEnv is an env whose machine m1 has a pool of 8 CPUs and 16 GiB, with
// admission on, job sizes by name and a runner class by runner name.
func poolEnv(t *testing.T, opts ...scheduler.Option) *env {
	t.Helper()
	e := newEnv(t)
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 10 })
	ctx := context.Background()
	if err := e.st.SetPoolForTests(ctx, m1, 8, 16); err != nil {
		t.Fatal(err)
	}
	sizes := map[string][2]int{"big": {6, 12}, "mid": {4, 8}, "s1": {2, 4}, "s2": {2, 4}, "s3": {2, 4}, "s4": {2, 4}}
	var rows []jobsize.ScanRow
	for name, w := range sizes {
		rows = append(rows, jobsize.ScanRow{Repository: "o/r", Job: name, Weight: jobsize.Weight{CPU: w[0], Memory: w[1]},
			Class: jobsize.Normal, Confidence: jobsize.ConfHigh, Commit: "c", ScannedAt: e.now})
	}
	if err := e.st.JobSizes().ReplaceScan(ctx, "o/r", rows, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	all := append([]scheduler.Option{
		scheduler.WithClock(func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }),
		scheduler.WithRunnerClass(func(_, runner string) string {
			if runner == "r-small" {
				return "small"
			}
			return "large"
		}),
	}, opts...)
	e.eng = scheduler.NewEngine(e.st, all...)
	return e
}

func (e *env) release(g string, j scheduler.JobRef, d scheduler.Decision) {
	e.t.Helper()
	var claims []string
	for _, gr := range d.Go {
		claims = append(claims, gr.Claim)
	}
	if _, err := e.eng.Release(context.Background(), m1, j, claims); err != nil {
		e.t.Fatal(err)
	}
}

func TestAdmissionPoolRoomAndPassing(t *testing.T) {
	e := poolEnv(t)
	goes(t, e.gate(e.req("g-big", job("o/r", 1, "big"), 1, quiet)))
	d := e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "2 CPUs and 4 GiB free") {
		t.Fatalf("text %q", d.WaitText)
	}
	// A later job that fits passes the waiting one.
	d = e.gate(e.req("g-s1", job("o/r", 3, "s1"), 1, quiet))
	goes(t, d)
	if d.MemoryCapGiB != 16 || d.Class != "large" {
		t.Fatalf("cap %d class %q", d.MemoryCapGiB, d.Class)
	}
}

func TestAdmissionMemoryAloneHoldsAJob(t *testing.T) {
	e := poolEnv(t)
	// 2 CPUs of 8 used but 14 of 16 GiB would be taken by a job asking 4 CPUs and 12 GiB.
	if err := e.st.JobSizes().ReplaceScan(context.Background(), "o/r", []jobsize.ScanRow{
		{Repository: "o/r", Job: "ram", Weight: jobsize.Weight{CPU: 1, Memory: 12}, Class: jobsize.Heavy, Confidence: jobsize.ConfHigh, Commit: "c", ScannedAt: e.now},
		{Repository: "o/r", Job: "s1", Weight: jobsize.Weight{CPU: 2, Memory: 6}, Class: jobsize.Normal, Confidence: jobsize.ConfHigh, Commit: "c", ScannedAt: e.now},
	}, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	goes(t, e.gate(e.req("g1", job("o/r", 1, "ram"), 1, quiet)))
	d := e.gate(e.req("g2", job("o/r", 2, "s1"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "4 GiB free") {
		t.Fatalf("text %q", d.WaitText)
	}
}

func TestAdmissionCeilingOfTheRunnerClass(t *testing.T) {
	e := poolEnv(t)
	j := job("o/r", 1, "mid")
	j.RunnerName = "r-small"
	d := e.gate(e.req("g1", j, 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "this runner is small (2 CPUs, 4 GiB)") {
		t.Fatalf("text %q", d.WaitText)
	}
}

// Three passes make the waiting job the machine's reserved job; then a later
// job that does not fit the backfill rules waits (R5.93, R5.94).
func TestThreePassesReserveAndBlockLaterJobs(t *testing.T) {
	e := poolEnv(t)
	goes(t, e.gate(e.req("g-big", job("o/r", 1, "big"), 1, quiet)))
	waits(t, e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet)), scheduler.WaitFreeSlot)
	for i, name := range []string{"s1", "s2", "s3"} {
		j := job("o/r", int64(10+i), name)
		d := e.gate(e.req("g-"+name, j, 1, quiet))
		goes(t, d)
		e.release("g-"+name, j, d)
		want := scheduler.WaitFreeSlot
		if i == 2 {
			want = scheduler.WaitReserved // the third pass makes it the reserved job
		}
		waits(t, e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet)), want)
	}
	res, err := e.st.Reservations(context.Background())
	if err != nil || len(res) != 1 || res[0].TicketID != "g-mid" {
		t.Fatalf("reservations %+v %v", res, err)
	}
	d := e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet))
	// LOOK65: the reserved job itself waits as "reserved"; the jobs behind it wait for a slot.
	waits(t, d, scheduler.WaitReserved)
	if !strings.Contains(d.WaitText, "reserved since 10:00") {
		t.Fatalf("text %q", d.WaitText)
	}
	d = e.gate(e.req("g-s4", job("o/r", 20, "s4"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "a job is reserved on this machine") {
		t.Fatalf("text %q", d.WaitText)
	}
}

func TestTenMinutesReserveWithoutPasses(t *testing.T) {
	e := poolEnv(t)
	goes(t, e.gate(e.req("g-big", job("o/r", 1, "big"), 1, quiet)))
	mid := job("o/r", 2, "mid")
	waits(t, e.gate(e.req("g-mid", mid, 1, quiet)), scheduler.WaitFreeSlot)
	for i := 0; i < 9; i++ {
		e.advanceRenewing(time.Minute, job("o/r", 1, "big"))
		waits(t, e.gate(e.req("g-mid", mid, 1, quiet)), scheduler.WaitFreeSlot)
	}
	if res, _ := e.st.Reservations(context.Background()); len(res) != 0 {
		t.Fatal("reserved before 10 minutes")
	}
	e.advanceRenewing(time.Minute, job("o/r", 1, "big"))
	d := e.gate(e.req("g-mid", mid, 1, quiet))
	if !strings.Contains(d.WaitText, "reserved since 10:10") {
		t.Fatalf("text %q", d.WaitText)
	}
}

// A job whose forecast end is before the reserved job's expected start may
// start (backfill, R5.94 a); the reserved job goes when room opens and the
// reservation ends.
func TestBackfillByForecastAndReservedJobStarts(t *testing.T) {
	var e *env
	forecast := func(_ string, j slots.Job, start time.Time) (time.Time, bool) {
		if j.Job == "big" {
			return start.Add(30 * time.Minute), true
		}
		return start.Add(5 * time.Minute), true
	}
	e = poolEnv(t, scheduler.WithForecast(forecast))
	bigJ := job("o/r", 1, "big")
	bigD := e.gate(e.req("g-big", bigJ, 1, quiet))
	goes(t, bigD)
	waits(t, e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet)), scheduler.WaitFreeSlot)
	var j scheduler.JobRef
	for i, name := range []string{"s1", "s2", "s3"} {
		j = job("o/r", int64(10+i), name)
		d := e.gate(e.req("g-"+name, j, 1, quiet))
		goes(t, d)
		e.release("g-"+name, j, d)
	}
	d := e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet))
	if !strings.Contains(d.WaitText, "reserved since 10:00, expected start 10:30") {
		t.Fatalf("text %q", d.WaitText)
	}
	// 5 minutes is before the expected start at 10:30: backfill.
	s4 := e.gate(e.req("g-s4", job("o/r", 20, "s4"), 1, quiet))
	goes(t, s4)
	e.release("g-s4", job("o/r", 20, "s4"), s4)
	e.release("g-big", bigJ, bigD)
	goes(t, e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet)))
	if res, _ := e.st.Reservations(context.Background()); len(res) != 0 {
		t.Fatalf("reservation stays: %+v", res)
	}
}

func TestReservationEndsWhenItsTicketIsCancelled(t *testing.T) {
	e := poolEnv(t)
	goes(t, e.gate(e.req("g-big", job("o/r", 1, "big"), 1, quiet)))
	waits(t, e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet)), scheduler.WaitFreeSlot)
	for i := 0; i < 10; i++ {
		e.advanceRenewing(time.Minute, job("o/r", 1, "big"))
		e.gate(e.req("g-mid", job("o/r", 2, "mid"), 1, quiet))
	}
	if res, _ := e.st.Reservations(context.Background()); len(res) != 1 {
		t.Fatalf("want one reservation, got %+v", res)
	}
	if err := e.eng.Cancel(context.Background(), m1, "g-mid"); err != nil {
		t.Fatal(err)
	}
	goes(t, e.gate(e.req("g-s2", job("o/r", 4, "s2"), 1, quiet)))
	if res, _ := e.st.Reservations(context.Background()); len(res) != 0 {
		t.Fatalf("reservation stays: %+v", res)
	}
}

func TestOverMemoryNeverLowersTheMeasuredSize(t *testing.T) {
	if scheduler.CountsForMeasured(true) || !scheduler.CountsForMeasured(false) {
		t.Fatal("a run killed for memory must not count")
	}
}

func TestOverMemoryIsMarkedAndNamesTheClassToRaise(t *testing.T) {
	e := poolEnv(t)
	var got jobsdata.Facts
	e.eng.SetFacts(func(_ scheduler.JobRef, f jobsdata.Facts) { got = f })
	e.eng.ReportOverMemory(m1, job("o/r", 1, "big"), "large", 16, "xl")
	if got.WaitReason != "over_memory" || !strings.Contains(got.WaitText, "Raise the class of this runner to xl") {
		t.Fatalf("facts %+v", got)
	}
	if w := scheduler.WaitWords(scheduler.WaitOverMemory, got.WaitText); !strings.HasPrefix(w, "Stopped over memory.") {
		t.Fatal(w)
	}
}
