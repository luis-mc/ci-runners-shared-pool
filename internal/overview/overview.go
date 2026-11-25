// Package overview builds the live snapshot the Overview screen and the event
// stream start from: machines with their load bars, the queue, running jobs
// and minutes saved today (observability R5.1, R5.15; web-console 4.2).
//
// Data comes through Source, which the controller implements over the store
// and the scheduler; this package never reads the database itself.
package overview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
)

// OSMinutes is the minutes saved on one OS.
type OSMinutes struct {
	OS      string
	Minutes int
}

// Source is what the overview reads.
type Source interface {
	// Machines lists machines with settings, load and slots used. Load carries
	// Stale already (older than 20 s, machines R6.7).
	Machines(ctx context.Context) ([]gen.Machine, error)
	// QueuedJobs lists jobs that wait, each with its wait reason (R5.5).
	QueuedJobs(ctx context.Context) ([]gen.Job, error)
	// RunningJobs lists jobs that run now.
	RunningJobs(ctx context.Context) ([]gen.Job, error)
	// MinutesSavedSince sums, per OS, the run time of jobs that finished on or
	// after since, each rounded up to the next whole minute (R5.15).
	MinutesSavedSince(ctx context.Context, since time.Time) ([]OSMinutes, error)
}

// QueuePager is an optional Source extension: it returns at most limit queued jobs, in queue order, and the
// exact number of queued jobs, both from SQL (LIMIT plus a count query), so the snapshot never loads the
// whole queue. A Source without it is read through QueuedJobs and capped here.
type QueuePager interface {
	QueuedJobsPage(ctx context.Context, limit int) (jobs []gen.Job, total int, err error)
}

// readConcurrency bounds the parallel reads of one snapshot (one per Source method).
const readConcurrency = 4

// LastIDer gives the id of the newest event, so a client opens the stream
// with it as Last-Event-ID. The events hub implements it.
type LastIDer interface {
	LastID() uint64
}

// MaxQueuedListed caps the queued jobs in the snapshot; queued_count is exact.
const MaxQueuedListed = 200

// Service builds snapshots.
type Service struct {
	Source Source
	Events LastIDer
	// Now and Loc decide where "today" starts; defaults: time.Now, time.Local.
	Now func() time.Time
	Loc *time.Location
}

// RoundUpMinutes is how GitHub bills a hosted job: the next whole minute. A
// zero or negative run is zero (observability R5.9, R5.15).
func RoundUpMinutes(run time.Duration) int {
	if run <= 0 {
		return 0
	}
	return int((run + time.Minute - 1) / time.Minute)
}

// StartOfDay is local midnight of the day that holds now.
func StartOfDay(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// Get builds the Overview (operation getOverview).
func (s *Service) Get(ctx context.Context) (gen.Overview, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	// Read the event id first: any change after it is replayed by the stream.
	var last uint64
	if s.Events != nil {
		last = s.Events.LastID()
	}
	// The four reads are independent: run them together, at most readConcurrency at once.
	var (
		machines []gen.Machine
		queued   []gen.Job
		count    int
		running  []gen.Job
		saved    []OSMinutes
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(readConcurrency)
	g.Go(func() (err error) {
		if machines, err = s.Source.Machines(gctx); err != nil {
			return fmt.Errorf("overview machines: %w", err)
		}
		return nil
	})
	g.Go(func() (err error) {
		if p, ok := s.Source.(QueuePager); ok {
			// The source caps the rows and counts the rest in SQL.
			if queued, count, err = p.QueuedJobsPage(gctx, MaxQueuedListed); err != nil {
				return fmt.Errorf("overview queue: %w", err)
			}
			return nil
		}
		if queued, err = s.Source.QueuedJobs(gctx); err != nil {
			return fmt.Errorf("overview queue: %w", err)
		}
		count = len(queued)
		return nil
	})
	g.Go(func() (err error) {
		if running, err = s.Source.RunningJobs(gctx); err != nil {
			return fmt.Errorf("overview running: %w", err)
		}
		return nil
	})
	g.Go(func() (err error) {
		if saved, err = s.Source.MinutesSavedSince(gctx, StartOfDay(now(), s.Loc)); err != nil {
			return fmt.Errorf("overview minutes saved: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return gen.Overview{}, err
	}

	sort.SliceStable(machines, func(i, j int) bool {
		if machines[i].IsController != machines[j].IsController {
			return machines[i].IsController
		}
		return machines[i].Name < machines[j].Name
	})
	if len(queued) > MaxQueuedListed {
		queued = queued[:MaxQueuedListed]
	}
	if running == nil {
		running = []gen.Job{}
	}
	if queued == nil {
		queued = []gen.Job{}
	}
	waiting := Waiting(queued)
	out := gen.Overview{
		Machines:    machines,
		QueuedCount: count,
		Queued:      &queued,
		Waiting:     &waiting,
		Running:     running,
		LastEventId: strconv.FormatUint(last, 10),
	}
	if machines == nil {
		out.Machines = []gen.Machine{}
	}
	minutes := make([]struct {
		Minutes int    `json:"minutes"`
		Os      gen.OS `json:"os"`
	}, 0, len(saved))
	for _, m := range saved {
		if m.OS == "" || m.Minutes <= 0 {
			continue
		}
		minutes = append(minutes, struct {
			Minutes int    `json:"minutes"`
			Os      gen.OS `json:"os"`
		}{Minutes: m.Minutes, Os: gen.OS(m.OS)})
	}
	sort.Slice(minutes, func(i, j int) bool { return minutes[i].Os < minutes[j].Os })
	out.MinutesSavedToday = &minutes
	return out, nil
}

// Waiting lists, for each queued or held job, the first reason it waits, in
// words (scheduling R5.48). It reads the gate's WaitReason and WaitText from the
// job, so the console and the gate say the same thing (one source of truth,
// UX-34). A job with no reason yet reads "reason not known yet", never blank.
func Waiting(queued []gen.Job) []gen.WaitingJob {
	out := make([]gen.WaitingJob, 0, len(queued))
	for _, j := range queued {
		reason := gen.WaitReasonUnknown
		if j.WaitReason != nil {
			reason = *j.WaitReason
		}
		text := ""
		if j.WaitText != nil {
			text = *j.WaitText
		}
		w := gen.WaitingJob{JobId: j.Id, Repository: j.Repository, JobName: j.JobName, Reason: reason,
			Words: scheduler.WaitWords(scheduler.WaitReason(reason), text), MachineId: j.MachineId}
		out = append(out, w)
	}
	return out
}

// Snapshot implements events.Snapshotter: the Overview as JSON.
func (s *Service) Snapshot(ctx context.Context) ([]byte, error) {
	o, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(o)
}

// EmptySource has no machines, jobs or savings. The controller uses it until
// the store adapter is wired, so the stream and the overview answer.
type EmptySource struct{}

// Machines implements Source.
func (EmptySource) Machines(context.Context) ([]gen.Machine, error) { return nil, nil }

// QueuedJobsPage implements QueuePager.
func (EmptySource) QueuedJobsPage(context.Context, int) ([]gen.Job, int, error) { return nil, 0, nil }

// QueuedJobs implements Source.
func (EmptySource) QueuedJobs(context.Context) ([]gen.Job, error) { return nil, nil }

// RunningJobs implements Source.
func (EmptySource) RunningJobs(context.Context) ([]gen.Job, error) { return nil, nil }

// MinutesSavedSince implements Source.
func (EmptySource) MinutesSavedSince(context.Context, time.Time) ([]OSMinutes, error) {
	return nil, nil
}
