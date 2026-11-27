package metrics

import (
	"context"
	"sync"
	"time"
)

// JobFact is what the pool source needs to know about one finished job.
type JobFact struct {
	ID        int64
	Queued    time.Time // took a ticket in the queue (or GitHub created it)
	Started   time.Time
	Completed time.Time
	// Cache is "warm", "cold" or "".
	Cache string
	// Hosted is true when the job ran on a GitHub-hosted runner, so the pool fell back.
	Hosted bool
}

// MachineFact is what the pool source needs to know about one machine.
type MachineFact struct {
	Name   string
	OS     string
	Online bool
}

// Feeds are the reads the controller supplies. Each reads another area through
// its public interface; this package imports none of them.
type Feeds struct {
	// RunnerStates counts runners by state.
	RunnerStates func(ctx context.Context) (map[string]int, error)
	// FinishedJobs lists jobs that finished on or after since, newest last.
	FinishedJobs func(ctx context.Context, since time.Time) ([]JobFact, error)
	// Machines lists the machines.
	Machines func(ctx context.Context) ([]MachineFact, error)
}

// PoolSource is the Source the controller uses. Job numbers count the jobs
// that finished since the controller started, so each series only grows.
// A machine's uptime is the time since this controller saw it come online.
type PoolSource struct {
	Feeds   Feeds
	Started time.Time
	Now     func() time.Time

	mu    sync.Mutex
	since map[string]time.Time
}

func (p *PoolSource) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Snapshot implements Source. A feed that is nil or fails leaves its numbers
// empty; the other numbers still come out.
func (p *PoolSource) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	now := p.now()
	if f := p.Feeds.RunnerStates; f != nil {
		if m, err := f(ctx); err == nil {
			s.RunnersByState = m
		}
	}
	if f := p.Feeds.FinishedJobs; f != nil {
		if jobs, err := f(ctx, p.Started); err == nil {
			for _, j := range jobs {
				if !j.Queued.IsZero() && !j.Started.IsZero() && !j.Started.Before(j.Queued) {
					s.QueueWaitSeconds = append(s.QueueWaitSeconds, j.Started.Sub(j.Queued).Seconds())
				}
				if !j.Started.IsZero() && !j.Completed.Before(j.Started) && !j.Completed.IsZero() {
					s.JobDurationSeconds = append(s.JobDurationSeconds, j.Completed.Sub(j.Started).Seconds())
				}
				if j.Hosted {
					s.FallbackTotal++
				}
				switch j.Cache {
				case "warm":
					s.CacheHits++
				case "cold":
					s.CacheMisses++
				}
			}
		}
	}
	if f := p.Feeds.Machines; f != nil {
		if ms, err := f(ctx); err == nil {
			p.mu.Lock()
			if p.since == nil {
				p.since = map[string]time.Time{}
			}
			for _, m := range ms {
				u := MachineUptime{Name: m.Name, OS: m.OS, Online: m.Online}
				if m.Online {
					if _, ok := p.since[m.Name]; !ok {
						p.since[m.Name] = now
					}
					u.UptimeSeconds = now.Sub(p.since[m.Name]).Seconds()
				} else {
					delete(p.since, m.Name)
				}
				s.Machines = append(s.Machines, u)
			}
			p.mu.Unlock()
		}
	}
	return s, nil
}
