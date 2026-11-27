package api

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// The slot timeline (LOOK58, observability R5.66). buildTimeline is pure: the handler reads the data, this turns it
// into rows and bars.
//
// Slots. A machine has Settings.JobSlots rows, the same count the queue simulation gives it (scheduler classes light
// and heavy are not told apart, as in slotCounts). The store keeps no slot number for a finished job, so a job holds
// the row the greedy rule gives it: jobs of a machine in order of start (then id), each in the lowest row whose last
// bar has ended. The rule runs over every job of the last 24 hours, so a job keeps its row from page to page. A
// machine whose jobs overlapped more than its slots count now (the setting changed) shows the extra rows too.

const (
	timelineHorizon = 24 * time.Hour // forecast and history never reach further than this
	timelineHours   = 6              // default page size in hours
	waitingSuffix   = "waiting"
)

// timelineMachine is a machine with its job slots.
type timelineMachine struct {
	ID, Name string
	Slots    int
}

// timelineInput is everything buildTimeline needs.
type timelineInput struct {
	Now       time.Time
	Machines  []timelineMachine
	Finished  []jobsdata.Job // done jobs, any order; those that ended before now - 24 h are ignored
	Open      []jobsdata.Job // queued and running jobs
	Forecasts map[int64]forecast.Forecast
	Flags     jobFlags   // reserved jobs and timers that ran out (LOOK65)
	Before    *time.Time // nil on the first call
	Hours     int
	MachineID string // "" for all machines
}

type tlMachine struct {
	timelineMachine
	rows   int // slot rows shown
	offset int // slots of the machines before it, for a job no machine took yet
}

type tlBuilder struct {
	in       timelineInput
	machines []*tlMachine
	byID     map[string]*tlMachine
	held     map[int64]int // job id -> slot row of a finished or running job
	bars     []gen.TimelineBar
	waiting  map[string]bool // machine ids with a waiting row in use
}

func machineOrder(a, b timelineMachine) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

func buildTimeline(in timelineInput) gen.Timeline {
	in.Now = in.Now.UTC()
	if in.Hours > 24 {
		in.Hours = 24
	} else if in.Hours < 1 {
		in.Hours = timelineHours
	}
	b := &tlBuilder{in: in, byID: map[string]*tlMachine{}, held: map[int64]int{}, waiting: map[string]bool{}}
	ms := append([]timelineMachine(nil), in.Machines...)
	sort.SliceStable(ms, func(i, j int) bool { return machineOrder(ms[i], ms[j]) })
	offset := 0
	for _, m := range ms {
		tm := &tlMachine{timelineMachine: m, rows: max(m.Slots, 0), offset: offset}
		offset += tm.rows
		b.machines = append(b.machines, tm)
		b.byID[m.ID] = tm
	}
	jobs := b.assignSlots()
	out := gen.Timeline{Now: in.Now, From: in.Now, To: in.Now}
	lower, more := b.pastBars(jobs, &out)
	out.MoreBefore = more
	if in.Before == nil {
		b.runningBars()
		b.forecastBars()
	}
	out.Rows, out.Bars = b.rowsAndBars()
	out.From = lower
	for _, bar := range out.Bars {
		if bar.Start.Before(out.From) {
			out.From = bar.Start
		}
		if bar.State == "forecast" && bar.End != nil && bar.End.After(out.To) {
			out.To = *bar.End
		}
	}
	return out
}

// slotJob is a finished or running job with its time span.
type slotJob struct {
	job        jobsdata.Job
	start, end time.Time // end is zero while running
}

// assignSlots gives every finished or running job of the last 24 hours a row (greedy by start) and returns them.
func (b *tlBuilder) assignSlots() []slotJob {
	floor := b.in.Now.Add(-timelineHorizon)
	perMachine := map[string][]slotJob{}
	add := func(j jobsdata.Job, running bool) {
		if b.byID[j.MachineID] == nil {
			return
		}
		start := firstTime(j.StartedAt, j.SlotAt)
		if start.IsZero() || start.After(b.in.Now) {
			return
		}
		sj := slotJob{job: j, start: start}
		if !running {
			if j.CompletedAt.IsZero() || j.CompletedAt.Before(start) || j.CompletedAt.Before(floor) {
				return
			}
			sj.end = j.CompletedAt
		}
		perMachine[j.MachineID] = append(perMachine[j.MachineID], sj)
	}
	for _, j := range b.in.Finished {
		add(j, false)
	}
	for _, j := range b.in.Open {
		if j.State == jobsdata.Running {
			add(j, true)
		}
	}
	var all []slotJob
	for _, m := range b.machines {
		list := perMachine[m.ID]
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].start.Equal(list[j].start) {
				return list[i].start.Before(list[j].start)
			}
			return list[i].job.ID < list[j].job.ID
		})
		var lastEnd []time.Time // per row; zero time with open=true means held by a running job
		var open []bool
		for _, sj := range list {
			row := -1
			for r := range lastEnd {
				if !open[r] && !lastEnd[r].After(sj.start) {
					row = r
					break
				}
			}
			if row < 0 {
				lastEnd, open = append(lastEnd, time.Time{}), append(open, false)
				row = len(lastEnd) - 1
			}
			lastEnd[row], open[row] = sj.end, sj.end.IsZero()
			b.held[sj.job.ID] = row + 1
			all = append(all, sj)
		}
		m.rows = max(m.rows, len(lastEnd))
	}
	return all
}

// pastBars adds the bars of finished jobs inside the window and says where the window starts and whether older
// jobs (within 24 hours) are left.
func (b *tlBuilder) pastBars(jobs []slotJob, out *gen.Timeline) (lower time.Time, more bool) {
	floor := b.in.Now.Add(-timelineHorizon)
	upper := b.in.Now
	if b.in.Before != nil && b.in.Before.Before(upper) {
		upper = b.in.Before.UTC()
	}
	lower = upper.Add(-time.Duration(b.in.Hours) * time.Hour)
	if lower.Before(floor) {
		lower = floor
	}
	for _, sj := range jobs {
		if sj.end.IsZero() || (b.in.MachineID != "" && sj.job.MachineID != b.in.MachineID) {
			continue
		}
		inWindow := !sj.end.Before(lower) && (b.in.Before == nil || sj.end.Before(upper))
		switch {
		case inWindow:
			b.bars = append(b.bars, b.finishedBar(sj))
		case sj.end.Before(lower):
			more = true
		}
	}
	return lower, more
}

func (b *tlBuilder) rowKey(machineID string, slot int) string {
	return machineID + ":" + strconv.Itoa(slot)
}

func (b *tlBuilder) finishedBar(sj slotJob) gen.TimelineBar {
	j, state := sj.job, gen.TimelineBarState("done")
	took := humanSpan(sj.end.Sub(sj.start))
	detail := j.Name + " ran " + took
	switch j.Result {
	case jobsdata.TimedOut:
		state, detail = "timer_ran_out", j.Name+" ran out of time after "+took
	case jobsdata.Failure, jobsdata.Lost:
		state, detail = "failed", j.Name+" failed after "+took
	case jobsdata.Cancelled:
		detail = j.Name + " was cancelled after " + took
	}
	bar := b.bar(j, state, b.rowKey(j.MachineID, b.held[j.ID]), sj.start, detail)
	end := sj.end
	bar.End = &end
	return bar
}

func (b *tlBuilder) bar(j jobsdata.Job, state gen.TimelineBarState, row string, start time.Time, detail string) gen.TimelineBar {
	id := j.ID
	return gen.TimelineBar{
		Id: "job-" + strconv.FormatInt(id, 10), JobId: &id, RowId: row, State: state, Label: j.Name,
		Repository: ptr(j.Repository), Start: start, Detail: ptr(detail),
	}
}

func (b *tlBuilder) runningBars() {
	for _, j := range b.in.Open {
		if j.State != jobsdata.Running || b.held[j.ID] == 0 {
			continue
		}
		start := firstTime(j.StartedAt, j.SlotAt)
		elapsed := b.in.Now.Sub(start)
		detail := j.Name + " running for " + humanSpan(elapsed)
		if fc := b.in.Forecasts[j.ID]; fc.Basis != forecast.BasisNone {
			if left := forecast.RemainingAfter(fc, elapsed.Seconds()); left.P50 > 0 {
				detail += ", ends in " + humanSpan(secondsDur(left.P50))
			}
		}
		state := gen.TimelineBarState("running")
		var expires *time.Time
		if d, ok := b.in.Flags.grace[j.ID]; ok {
			state, expires = "timer_ran_out", tptr(d)
			detail = j.Name + " ran out of time after " + humanSpan(elapsed) + "; crsp stops it " + startWords(d.Sub(b.in.Now))
		}
		bar := b.bar(j, state, b.rowKey(j.MachineID, b.held[j.ID]), start, detail)
		bar.ExpiresAt = expires
		b.bars = append(b.bars, bar)
	}
}

func secondsDur(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// humanSpan is a span in plain words: "less than a minute", "about 12 min", "about 2 h 5 min".
func humanSpan(d time.Duration) string {
	if d < 45*time.Second {
		return "less than a minute"
	}
	mins := int((d + 30*time.Second) / time.Minute)
	if mins < 90 {
		return fmt.Sprintf("about %d min", mins)
	}
	if mins%60 == 0 {
		return fmt.Sprintf("about %d h", mins/60)
	}
	return fmt.Sprintf("about %d h %d min", mins/60, mins%60)
}

// startWords is "now" or "in about 12 min".
func startWords(d time.Duration) string {
	if d < 30*time.Second {
		return "now"
	}
	return "in " + humanSpan(d)
}

// waitingBar is a bar in the waiting row of a machine: it starts now and has no end; wait_from is when it queued.
func (b *tlBuilder) waitingBar(j jobsdata.Job, machineID string, state gen.TimelineBarState, detail string) {
	b.waiting[machineID] = true
	bar := b.bar(j, state, machineID+":"+waitingSuffix, b.in.Now, detail)
	bar.WaitFrom = tptr(firstTime(j.QueuedAt, j.CreatedAt))
	b.bars = append(b.bars, bar)
}

// forecastBars places the queued jobs: on a slot row by the queue simulation, else in the waiting row.
func (b *tlBuilder) forecastBars() {
	if len(b.machines) == 0 {
		return
	}
	var queued []jobsdata.Job
	for _, j := range b.in.Open {
		if j.State == jobsdata.Queued {
			queued = append(queued, j)
		}
	}
	sort.SliceStable(queued, func(x, y int) bool {
		tx, ty := firstTime(queued[x].QueuedAt, queued[x].CreatedAt), firstTime(queued[y].QueuedAt, queued[y].CreatedAt)
		if !tx.Equal(ty) {
			return tx.Before(ty)
		}
		return queued[x].ID < queued[y].ID
	})
	groups := map[string][]jobsdata.Job{}
	for _, j := range queued {
		if j.MachineID != "" && b.byID[j.MachineID] == nil {
			continue // a machine that is gone has no row
		}
		if since, ok := b.in.Flags.reserved[j.ID]; ok {
			m := b.byID[b.homeMachine(j)]
			b.waitingBar(j, m.ID, "reserved", "is reserved: "+m.Name+" keeps room free for it since "+since.Format("15:04")+" UTC")
			b.bars[len(b.bars)-1].ReservedSince = tptr(since)
			continue
		}
		switch j.WaitReason {
		case "no_runner_with_labels":
			b.waitingBar(j, b.homeMachine(j), "no_runner_fits", "no runner has the labels it asks for")
		case "load_over_budget":
			b.waitingBar(j, b.homeMachine(j), "waiting_room", "waits because the machine is over its load budget")
		default:
			groups[j.MachineID] = append(groups[j.MachineID], j)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.placeGroup(k, groups[k])
	}
}

// homeMachine is the machine of a queued job; a job no machine took yet is shown under the first machine.
func (b *tlBuilder) homeMachine(j jobsdata.Job) string {
	if j.MachineID != "" {
		return j.MachineID
	}
	return b.machines[0].ID
}

// placeGroup simulates the queued jobs of one machine ("" is the whole pool, its slots numbered machine after machine).
func (b *tlBuilder) placeGroup(machineID string, jobs []jobsdata.Job) {
	slots := 0
	if machineID == "" {
		for _, m := range b.machines {
			slots += m.Slots
		}
	} else {
		slots = b.byID[machineID].Slots
	}
	var running []forecast.SlotJob
	for _, r := range b.in.Open {
		if r.State != jobsdata.Running || (machineID != "" && r.MachineID != machineID) || b.byID[r.MachineID] == nil {
			continue
		}
		slot, m := b.held[r.ID], b.byID[r.MachineID]
		if slot > m.Slots {
			slot = 0
		} else if machineID == "" && slot > 0 {
			slot += m.offset
		}
		elapsed := 0.0
		if start := firstTime(r.StartedAt, r.SlotAt); !start.IsZero() && b.in.Now.After(start) {
			elapsed = b.in.Now.Sub(start).Seconds()
		}
		running = append(running, forecast.SlotJob{Remaining: forecast.RemainingAfter(b.in.Forecasts[r.ID], elapsed), Slot: slot})
	}
	wait := make([]forecast.SlotJob, len(jobs))
	for i, j := range jobs {
		wait[i] = forecast.SlotJob{Remaining: b.in.Forecasts[j.ID]}
	}
	etas, known, slotOf := forecast.SimulateQueueSlots(slots, running, wait)
	for i, j := range jobs {
		fc := b.in.Forecasts[j.ID]
		home := b.homeMachine(j)
		if !known[i] || fc.Basis == forecast.BasisNone || slotOf[i] < 1 {
			detail := "waits for room on " + b.byID[home].Name + "; the jobs ahead of it are not placed yet"
			if fc.Basis == forecast.BasisNone {
				detail = "not enough history yet to place it"
			}
			b.placeless(j, home, detail)
			continue
		}
		m, slot := b.byID[home], slotOf[i]
		if machineID == "" { // a pool slot number: find its machine
			m = b.machineOfPoolSlot(slot)
			slot -= m.offset
		}
		b.placed(j, m, slot, etas[i], fc)
	}
}

func (b *tlBuilder) placeless(j jobsdata.Job, machineID, detail string) {
	b.waitingBar(j, machineID, "waiting_runner", detail)
}

func (b *tlBuilder) machineOfPoolSlot(n int) *tlMachine {
	for _, m := range b.machines {
		if n <= m.offset+m.Slots {
			return m
		}
	}
	return b.machines[len(b.machines)-1]
}

// placed adds the forecast bar of a queued job, clipped at now + 24 h.
func (b *tlBuilder) placed(j jobsdata.Job, m *tlMachine, slot int, eta forecast.Eta, fc forecast.Forecast) {
	now, horizon := b.in.Now, b.in.Now.Add(timelineHorizon)
	at := func(s float64) time.Time { return now.Add(secondsDur(s)) }
	start, end := at(eta.P50), at(eta.P50+fc.P50)
	if !start.Before(horizon) {
		b.placeless(j, m.ID, "starts more than 24 hours from now")
		return
	}
	clip := func(t time.Time) time.Time {
		if t.After(horizon) {
			return horizon
		}
		return t
	}
	end, lo, hi := clip(end), clip(at(eta.Lo+fc.Lo)), clip(at(eta.Hi+fc.Hi))
	detail := fmt.Sprintf("waits for room on %s, starts %s, runs %s", m.Name, startWords(start.Sub(now)), humanSpan(secondsDur(fc.P50)))
	bar := b.bar(j, "forecast", b.rowKey(m.ID, slot), start, detail)
	bar.End, bar.EndLo, bar.EndHi = &end, &lo, &hi
	bar.WaitFrom = tptr(firstTime(j.QueuedAt, j.CreatedAt))
	b.bars = append(b.bars, bar)
}

// rowsAndBars lists the rows (slot rows of every machine, waiting rows in use) and the bars, filtered to one machine.
func (b *tlBuilder) rowsAndBars() ([]gen.TimelineRow, []gen.TimelineBar) {
	rows := []gen.TimelineRow{}
	for _, m := range b.machines {
		if b.in.MachineID != "" && m.ID != b.in.MachineID {
			continue
		}
		for n := 1; n <= m.rows; n++ {
			rows = append(rows, gen.TimelineRow{Key: b.rowKey(m.ID, n), MachineId: m.ID, MachineName: m.Name, Slot: ptr(n), Label: m.Name + " slot " + strconv.Itoa(n)})
		}
		if b.waiting[m.ID] {
			rows = append(rows, gen.TimelineRow{Key: m.ID + ":" + waitingSuffix, MachineId: m.ID, MachineName: m.Name, Label: m.Name + " waiting"})
		}
	}
	keep := map[string]bool{}
	for _, r := range rows {
		keep[r.Key] = true
	}
	bars := []gen.TimelineBar{}
	for _, bar := range b.bars {
		if keep[bar.RowId] {
			bars = append(bars, bar)
		}
	}
	sort.SliceStable(bars, func(x, y int) bool {
		if !bars[x].Start.Equal(bars[y].Start) {
			return bars[x].Start.Before(bars[y].Start)
		}
		return bars[x].Id < bars[y].Id
	})
	return rows, bars
}
