package assign

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/admit"
)

// Actor is the name the controller writes in the audit trail.
const Actor = "assign"

// The times of the flow.
const (
	// UnusedGrace is added to a runner's start-up time before a runner nobody
	// used is deleted (runners R5.112).
	UnusedGrace = 2 * time.Minute
	// AlertAfter is how long a job may wait before the owner is told; GitHub
	// cancels a job queued for CancelAfter (runners R5.114).
	AlertAfter  = 12 * time.Hour
	CancelAfter = 24 * time.Hour
	// WarmMaxAge replaces a warm runner so it never runs from a stale image
	// (scheduling R5.66); BusyWindow is how long a label set stays busy.
	WarmMaxAge = 15 * time.Minute
	BusyWindow = time.Hour
	// retry is the first wait after a failed start; it doubles up to retryMax.
	retry    = 30 * time.Second
	retryMax = 10 * time.Minute
)

// Controller is the one central controller. Every method takes the same lock,
// so a webhook, the poller and a restart's first poll never plan at once; and
// the lease store is the compare-and-set that closes a race across restarts.
type Controller struct {
	Store   Store
	Env     Env
	Runners Runners
	Notify  Notifier
	// Gate is the listener gate of persistent runners on the machine itself;
	// nil makes every job use a JIT runner (runners R5.134).
	Gate Gate
	// Purge deletes a runner by name at GitHub when no record of it exists
	// (a start that crashed between the JIT call and the record). Nil skips it.
	Purge func(ctx context.Context, repository, name string) error
	// Forecast reads the forecast end of a job that runs or starts on a machine
	// (observability R5.40). Nil: no forecast.
	Forecast func(machineID string, l Lease, start time.Time) (time.Time, bool)
	Now      func() time.Time
	// NewID makes the suffix of a runner name; the default counts from the clock.
	NewID func() string

	mu  sync.Mutex
	seq int
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Controller) newName(prefix string, jobID int64) string {
	// The counter keeps names unique inside one process; the clock keeps them
	// unique across restarts, so a name GitHub still holds is never reused.
	c.seq++
	if c.NewID != nil {
		return fmt.Sprintf("%s%d-%s%d", prefix, jobID, c.NewID(), c.seq)
	}
	return fmt.Sprintf("%s%d-%d%d", prefix, jobID, c.now().Unix(), c.seq)
}

// OSOf reads the system a job asks for from its labels, "" when none is named.
func OSOf(labels []string) string {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "linux", "windows", "macos":
			return strings.ToLower(l)
		}
	}
	return ""
}

func routingLabelOf(labels []string) string {
	for _, l := range labels {
		if runners.IsRoutingLabel(l) {
			return strings.ToLower(l)
		}
	}
	return ""
}

func lower(ls []string) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, strings.ToLower(strings.TrimSpace(l)))
	}
	return out
}

// Queued handles a workflow_job event, action queued, or a queued job the poller
// listed (flow steps 1 and 2). A repeat of the same job id changes nothing.
func (c *Controller) Queued(ctx context.Context, j github.Job) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if j.Status != "" && j.Status != "queued" {
		return nil
	}
	if _, ok, err := c.Env.Repo(ctx, j.Repository); err != nil || !ok {
		return err // a repository whose pool is off is none of ours
	}
	now := c.now()
	req, class := c.Env.Requirement(j.Repository, j.Name)
	q := j.CreatedAt
	if q.IsZero() || q.After(now) {
		q = now
	}
	labels := lower(j.Labels)
	l := Lease{JobID: j.ID, RunID: j.RunID, RunAttempt: j.RunAttempt, Repository: j.Repository, JobName: j.Name,
		Labels: labels, JobLabel: routingLabelOf(labels), OS: OSOf(labels), State: Waiting,
		Req: req, Class: class, QueuedAt: q, UpdatedAt: now}
	if _, created, err := c.Store.Claim(ctx, l); err != nil {
		return err
	} else if !created {
		return nil // a repeated webhook, or a restart: the lease is there
	}
	return c.plan(ctx)
}

// InProgress handles the in_progress webhook: it names the job and the runner.
// The lease moves to the real job and the pool is recounted by it (flow step 4).
func (c *Controller) InProgress(ctx context.Context, j github.Job) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	own, haveOwn, err := c.Store.Get(ctx, j.ID)
	if err != nil {
		return err
	}
	held, haveHeld, err := c.Store.ByRunner(ctx, j.RunnerName)
	if err != nil {
		return err
	}
	// A warm runner that took a job becomes that job's lease.
	if !haveHeld {
		if w, ok, err := c.warmByRunner(ctx, j.RunnerName); err != nil {
			return err
		} else if ok {
			held = Lease{JobID: -1, MachineID: w.MachineID, RunnerName: w.RunnerName, RunnerRecord: w.RunnerRecord,
				GitHubRunner: w.GitHubRunner, StartedAt: w.StartedAt}
			haveHeld = true
			if err := c.Store.DeleteWarm(ctx, w.ID); err != nil {
				return err
			}
		}
	}
	switch {
	case haveOwn && haveHeld && held.JobID == own.JobID:
		own.State, own.Reason, own.ReasonCode, own.UpdatedAt = Running, "", CodeNone, now
		if err := c.Store.Save(ctx, own); err != nil {
			return err
		}
	case haveHeld:
		// The runner made for another job (or a warm one) took this one: runners
		// of one job name are interchangeable. This job's lease gets the runner
		// and is counted by what it really needs; the other job waits again.
		if !haveOwn {
			req, class := c.Env.Requirement(j.Repository, j.Name)
			labels := lower(j.Labels)
			own = Lease{JobID: j.ID, RunID: j.RunID, RunAttempt: j.RunAttempt, Repository: j.Repository, JobName: j.Name,
				Labels: labels, JobLabel: routingLabelOf(labels), OS: OSOf(labels), Req: req, Class: class, QueuedAt: j.CreatedAt}
			if own.QueuedAt.IsZero() {
				own.QueuedAt = now
			}
			if _, _, err := c.Store.Claim(ctx, own); err != nil {
				return err
			}
			if own, _, err = c.Store.Get(ctx, j.ID); err != nil {
				return err
			}
		} else if own.State == Waiting || own.State == Starting {
			own.Req, own.Class = c.Env.Requirement(j.Repository, j.Name)
		}
		taken := held // the runner that takes this job
		if held.JobID > 0 {
			if haveOwn && own.RunnerName != "" && own.State == Starting {
				// Each job holds the runner the other one made: swap them, so no
				// runner is lost and none is made twice.
				held.MachineID, held.RunnerName, held.RunnerRecord, held.GitHubRunner, held.StartedAt =
					own.MachineID, own.RunnerName, own.RunnerRecord, own.GitHubRunner, own.StartedAt
				held.State = Starting
			} else {
				held.State = Waiting
				held.MachineID, held.RunnerName, held.RunnerRecord, held.GitHubRunner = "", "", "", 0
				held.StartedAt = time.Time{}
			}
			held.Reason, held.ReasonCode, held.UpdatedAt = "", CodeNone, now
			if err := c.Store.Save(ctx, held); err != nil {
				return err
			}
		}
		own.State, own.MachineID, own.RunnerName = Running, taken.MachineID, taken.RunnerName
		own.RunnerRecord, own.GitHubRunner, own.StartedAt = taken.RunnerRecord, taken.GitHubRunner, taken.StartedAt
		own.Reason, own.ReasonCode, own.ReservedSince, own.UpdatedAt = "", CodeNone, time.Time{}, now
		if err := c.Store.Save(ctx, own); err != nil {
			return err
		}
	case haveOwn && own.State.Open():
		// Taken by a runner that is not ours: it holds no room of ours. A runner
		// we made for it that is still starting is left for the unused sweep.
		own.State, own.Reason, own.ReasonCode, own.UpdatedAt = Gone, "taken by another runner", CodeNone, now
		if err := c.Store.Save(ctx, own); err != nil {
			return err
		}
	}
	if haveOwn || haveHeld {
		_ = c.touchSet(ctx, own, now)
	}
	return c.plan(ctx)
}

func (c *Controller) touchSet(ctx context.Context, l Lease, now time.Time) error {
	if l.Repository == "" {
		return nil
	}
	return c.Store.TouchSet(ctx, Set{Key: l.LabelKey(), Repository: l.Repository, OS: l.OS, Arch: l.Arch,
		Labels: l.Labels, Req: l.Req, LastBusy: now})
}

func (c *Controller) warmByRunner(ctx context.Context, name string) (Warm, bool, error) {
	ws, err := c.Store.ListWarm(ctx)
	if err != nil {
		return Warm{}, false, err
	}
	for _, w := range ws {
		if w.RunnerName == name {
			return w, true, nil
		}
	}
	return Warm{}, false, nil
}

// Completed handles the completed webhook: the JIT runner exits, the agent
// cleans up, the room is free and the queue is planned again (flow step 5).
func (c *Controller) Completed(ctx context.Context, j github.Job) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gateDone(ctx, j.ID)
	l, ok, err := c.Store.Get(ctx, j.ID)
	if err != nil {
		return err
	}
	if !ok || !l.State.Open() {
		return nil
	}
	now := c.now()
	if l.State == Running {
		_ = c.touchSet(ctx, l, now)
		if l.RunnerRecord != "" {
			if err := c.Runners.FinishJobRunner(ctx, l.RunnerRecord, Actor); err != nil {
				return fmt.Errorf("clean up the runner of job %d: %w", l.JobID, err)
			}
		}
		l.State, l.RunnerRecord = Done, ""
	} else {
		// It ended before it ran here (cancelled, taken elsewhere). A runner we
		// started for it stays until the unused sweep deletes it.
		l.State = Gone
	}
	l.Reason, l.ReasonCode, l.UpdatedAt = "", CodeNone, now
	if err := c.Store.Save(ctx, l); err != nil {
		return err
	}
	return c.plan(ctx)
}

// Replan plans the queue again; the poller and a restart's first round call it.
func (c *Controller) Replan(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plan(ctx)
}

// Vanished tells the controller that a complete listing of GitHub no longer
// holds the job: a waiting or starting lease is gone, a running one is done.
func (c *Controller) Vanished(ctx context.Context, jobID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gateDone(ctx, jobID)
	l, ok, err := c.Store.Get(ctx, jobID)
	if err != nil || !ok || !l.State.Open() {
		return err
	}
	if l.State == Running {
		l.State = Done
		if l.RunnerRecord != "" {
			if err := c.Runners.FinishJobRunner(ctx, l.RunnerRecord, Actor); err != nil {
				return err
			}
			l.RunnerRecord = ""
		}
	} else {
		l.State = Gone
	}
	l.Reason, l.ReasonCode, l.UpdatedAt = "", CodeNone, c.now()
	return c.Store.Save(ctx, l)
}

// machineView is one machine with what holds its room now.
type machineView struct {
	m    Machine
	held []scheduler.RoomHeld
}

func (c *Controller) views(ctx context.Context, open []Lease, warm []Warm) ([]*machineView, error) {
	ms, err := c.Env.Machines(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	var out []*machineView
	byID := map[string]*machineView{}
	for _, m := range ms {
		v := &machineView{m: m}
		out = append(out, v)
		byID[m.ID] = v
	}
	for _, l := range open {
		if l.State != Starting && l.State != Running {
			continue
		}
		v := byID[l.MachineID]
		if v == nil {
			continue
		}
		h := scheduler.RoomHeld{Req: l.Req}
		if c.Forecast != nil && l.State == Running {
			h.End, h.EndKnown = c.Forecast(v.m.ID, l, l.StartedAt)
		}
		v.held = append(v.held, h)
	}
	for _, w := range warm {
		if v := byID[w.MachineID]; v != nil {
			v.held = append(v.held, scheduler.RoomHeld{Req: w.Req, Warm: true, ID: w.ID})
		}
	}
	return out, nil
}

// reservationOf is the machine's reserved job: the waiting lease reserved there.
func reservationOf(v *machineView, waiting []Lease) *admit.Reservation {
	var best *Lease
	for i := range waiting {
		l := &waiting[i]
		if l.State == Waiting && l.MachineID == v.m.ID && !l.ReservedSince.IsZero() {
			if best == nil || l.ReservedSince.Before(best.ReservedSince) {
				best = l
			}
		}
	}
	if best == nil {
		return nil
	}
	return &admit.Reservation{MachineID: v.m.ID, TicketID: fmt.Sprint(best.JobID), Req: best.Req, Class: best.Class, Since: best.ReservedSince}
}

func (v *machineView) roomInput(l Lease, now time.Time, res *admit.Reservation, fc func(string, Lease, time.Time) (time.Time, bool)) scheduler.RoomInput {
	in := scheduler.RoomInput{Machine: v.m.Name, Req: l.Req, Class: l.Class, Pool: v.m.Pool, Ceiling: v.m.Ceiling,
		CeilingKnown: v.m.CeilingKnown, Held: v.held, LoadOK: v.m.LoadOK, LoadText: v.m.LoadText,
		Reservation: res, JobKey: fmt.Sprint(l.JobID), Now: now}
	if fc != nil {
		in.End, in.EndKnown = fc(v.m.ID, l, now)
	}
	if in.Machine == "" {
		in.Machine = v.m.ID
	}
	return in
}

func reasonCode(w scheduler.RoomWhy) string {
	switch w {
	case scheduler.RoomPool:
		return CodePool
	case scheduler.RoomLoad:
		return CodeLoad
	case scheduler.RoomReserved:
		return CodeReserved
	case scheduler.RoomNever, scheduler.RoomCeiling:
		return CodeNever
	}
	return CodeNone
}

// plan walks the waiting leases in the order of service and makes a runner for
// each one that has room (flow step 2 and 3). The caller holds the lock.
func (c *Controller) plan(ctx context.Context) error {
	now := c.now()
	open, err := c.Store.List(ctx, Waiting, Starting, Running)
	if err != nil {
		return err
	}
	warm, err := c.Store.ListWarm(ctx)
	if err != nil {
		return err
	}
	vs, err := c.views(ctx, open, warm)
	if err != nil {
		return err
	}
	var waiting []Lease
	for _, l := range open {
		if l.State == Waiting {
			waiting = append(waiting, l)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool {
		if !waiting[i].QueuedAt.Equal(waiting[j].QueuedAt) {
			return waiting[i].QueuedAt.Before(waiting[j].QueuedAt)
		}
		return waiting[i].JobID < waiting[j].JobID
	})
	reservations := map[string]*admit.Reservation{}
	for _, v := range vs {
		reservations[v.m.ID] = reservationOf(v, waiting)
	}

	var blockedBefore []int
	var firstErr error
	for i := range waiting {
		l := &waiting[i]
		before := *l
		started, err := c.tryStart(ctx, l, vs, reservations, now, &blockedBefore, waiting)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if started {
			continue
		}
		blockedBefore = append(blockedBefore, i)
		if !sameLease(before, *l) {
			l.UpdatedAt = now
			if err := c.Store.Save(ctx, *l); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	// A blocked job that was passed too often, or waited too long, becomes the
	// machine's reserved job (scheduling R5.93).
	for _, v := range vs {
		if reservations[v.m.ID] != nil {
			continue
		}
		var ws []admit.Waiter
		for _, i := range blockedBefore {
			l := waiting[i]
			if l.State != Waiting || !v.m.Serves(l.OS, l.Labels) {
				continue
			}
			ceil, known := v.m.Ceiling, v.m.CeilingKnown
			ws = append(ws, admit.Waiter{ID: fmt.Sprint(l.JobID), Req: l.Req, Class: l.Class, QueuedAt: l.QueuedAt, Passes: l.Passes,
				Blocked: true, Could: admit.CouldRunHere(l.Req, admit.Ceiling{Req: ceil}, known, v.m.Pool)})
		}
		p := admit.Promote(ws, false, admit.DefaultLimits(), now)
		if p == nil {
			continue
		}
		for i := range waiting {
			if fmt.Sprint(waiting[i].JobID) != p.ID {
				continue
			}
			waiting[i].MachineID, waiting[i].ReservedSince, waiting[i].UpdatedAt = v.m.ID, now, now
			if err := c.Store.Save(ctx, waiting[i]); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := c.ensureWarm(ctx, vs, waiting, now); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func sameLease(a, b Lease) bool {
	return a.Reason == b.Reason && a.ReasonCode == b.ReasonCode && a.MachineID == b.MachineID &&
		a.Passes == b.Passes && a.Fails == b.Fails && a.NextTry.Equal(b.NextTry) && a.ReservedSince.Equal(b.ReservedSince)
}

// tryStart asks each machine that serves the job; the first with room gets a
// runner. It returns true when a runner was started (or a warm one taken).
func (c *Controller) tryStart(ctx context.Context, l *Lease, vs []*machineView, res map[string]*admit.Reservation,
	now time.Time, blockedBefore *[]int, all []Lease) (bool, error) {
	if !l.NextTry.IsZero() && now.Before(l.NextTry) {
		return false, nil // the reason of the failed start stays
	}
	var cands []*machineView
	for _, v := range vs {
		if v.m.Serves(l.OS, l.Labels) {
			cands = append(cands, v)
		}
	}
	if len(cands) == 0 {
		c.setReason(ctx, l, CodeNoMachine, noMachineText(*l))
		return false, nil
	}
	var firstWhy *scheduler.RoomDecision
	var firstText string
	for _, v := range cands {
		d := scheduler.CheckRoom(v.roomInput(*l, now, res[v.m.ID], c.Forecast))
		if !d.OK {
			if firstWhy == nil || (firstWhy.Why == scheduler.RoomNever || firstWhy.Why == scheduler.RoomCeiling) && d.Why != scheduler.RoomNever && d.Why != scheduler.RoomCeiling {
				dd := d
				firstWhy, firstText = &dd, d.Text
			}
			continue
		}
		ok, err := c.launch(ctx, l, v, d, now)
		if err != nil || !ok {
			return false, err
		}
		// A job that starts passes the blocked jobs ahead of it that could run here.
		for _, bi := range *blockedBefore {
			b := &all[bi]
			if b.State == Waiting && v.m.Serves(b.OS, b.Labels) &&
				admit.CouldRunHere(b.Req, admit.Ceiling{Req: v.m.Ceiling}, v.m.CeilingKnown, v.m.Pool) {
				b.Passes++
				b.UpdatedAt = now
				if err := c.Store.Save(ctx, *b); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}
	if firstWhy != nil {
		c.setReason(ctx, l, reasonCode(firstWhy.Why), firstText)
	}
	return false, nil
}

func noMachineText(l Lease) string {
	what := "a runner of its labels"
	if l.OS != "" {
		what = "a " + l.OS + " runner with its labels"
	}
	return "no machine can serve this job: there is no online machine with " + what
}

// setReason sets the reason of a waiting lease in memory; plan saves it when
// it changed, and the owner is told once when a job can never be served.
func (c *Controller) setReason(ctx context.Context, l *Lease, code, text string) {
	if code == CodeNever || code == CodeNoMachine {
		if l.ReasonCode != code && c.Notify != nil {
			c.Notify.Notify(ctx, *l, text)
		}
	}
	l.ReasonCode, l.Reason = code, text
}

// launch releases the warm runners the decision names, then makes the runner of
// the job: a warm one of its label set when there is one, else a new JIT runner.
func (c *Controller) launch(ctx context.Context, l *Lease, v *machineView, d scheduler.RoomDecision, now time.Time) (bool, error) {
	repo, ok, err := c.Env.Repo(ctx, l.Repository)
	if err != nil || !ok {
		return false, err
	}
	// A gated persistent runner of the machine takes the job when the gate enforces.
	if taken, stop, err := c.gateAdmit(ctx, l, v, now); stop || err != nil {
		return taken, err
	}
	// A warm runner of this label set on this machine takes the job as it is.
	warm, err := c.Store.ListWarm(ctx)
	if err != nil {
		return false, err
	}
	releasing := map[string]bool{}
	for _, id := range d.Release {
		releasing[id] = true
	}
	for _, w := range warm {
		if w.LabelKey == l.LabelKey() && w.MachineID == v.m.ID && !releasing[w.ID] {
			begun, err := c.Store.Begin(ctx, l.JobID, v.m.ID, w.RunnerName, now)
			if err != nil || !begun {
				return false, err
			}
			l.State, l.MachineID, l.RunnerName, l.RunnerRecord, l.GitHubRunner = Starting, v.m.ID, w.RunnerName, w.RunnerRecord, w.GitHubRunner
			l.StartedAt, l.ReservedSince = w.StartedAt, time.Time{}
			l.Reason, l.ReasonCode, l.UpdatedAt = "using the ready runner", CodeStarting, now
			if err := c.Store.Save(ctx, *l); err != nil {
				return false, err
			}
			return true, c.Store.DeleteWarm(ctx, w.ID)
		}
	}
	for _, id := range d.Release {
		if err := c.releaseWarm(ctx, id); err != nil {
			if errors.Is(err, runners.ErrRunnerBusy) {
				return false, nil // it took a job after all; its webhook follows
			}
			return false, err
		}
	}
	name := c.newName("crsp-job-", l.JobID)
	begun, err := c.Store.Begin(ctx, l.JobID, v.m.ID, name, now)
	if err != nil || !begun {
		return false, err // another caller began it: never a second runner
	}
	l.State, l.MachineID, l.RunnerName, l.StartedAt = Starting, v.m.ID, name, now
	rec, err := c.Runners.StartJobRunner(ctx, runners.JobRunner{Name: name, MachineID: v.m.ID, Arch: v.m.Arch, OS: v.m.OS,
		Labels: withLabel(l.Labels, l.JobLabel), Scope: repo.Scope, ExtraPackages: repo.ExtraPackages}, Actor)
	if err != nil {
		l.State, l.RunnerName, l.MachineID, l.StartedAt = Waiting, "", "", time.Time{}
		l.Fails++
		l.NextTry = now.Add(backoff(l.Fails))
		l.ReasonCode = CodeBackoff
		l.Reason = fmt.Sprintf("could not start a runner on %s: %v; trying again at %s", v.m.Name, err, l.NextTry.Format("15:04:05"))
		l.UpdatedAt = now
		if serr := c.Store.Save(ctx, *l); serr != nil {
			return false, serr
		}
		return false, nil
	}
	l.RunnerRecord, l.GitHubRunner, l.ReservedSince, l.NextTry, l.Fails = rec.ID, rec.GitHubRunnerID, time.Time{}, time.Time{}, 0
	l.Reason, l.ReasonCode, l.UpdatedAt = fmt.Sprintf("starting a runner on %s, ready in about %d s", v.m.Name, int(runners.StartupAllowance(v.m.OS).Seconds())), CodeStarting, now
	if err := c.Store.Save(ctx, *l); err != nil {
		return false, err
	}
	// Count what the runner holds from now on, within this round.
	v.held = append(v.held, scheduler.RoomHeld{Req: l.Req})
	if len(d.Release) > 0 {
		var keep []scheduler.RoomHeld
		for _, h := range v.held {
			if !h.Warm || !releasing[h.ID] {
				keep = append(keep, h)
			}
		}
		v.held = keep
	}
	return true, nil
}

func backoff(fails int) time.Duration {
	d := retry
	for i := 1; i < fails && d < retryMax; i++ {
		d *= 2
	}
	if d > retryMax {
		d = retryMax
	}
	return d
}

func withLabel(labels []string, extra string) []string {
	out := append([]string(nil), labels...)
	if extra == "" {
		return out
	}
	for _, l := range out {
		if l == extra {
			return out
		}
	}
	return append(out, extra)
}

func (c *Controller) releaseWarm(ctx context.Context, id string) error {
	ws, err := c.Store.ListWarm(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if w.ID != id {
			continue
		}
		if w.RunnerRecord != "" {
			if err := c.Runners.RemoveIdleRunner(ctx, w.RunnerRecord, Actor); err != nil {
				return err
			}
		}
		return c.Store.DeleteWarm(ctx, id)
	}
	return nil
}
