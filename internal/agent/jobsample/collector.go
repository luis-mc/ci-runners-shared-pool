package jobsample

import (
	"context"
	"sync"
	"time"
)

// Collector drives sampling of the running jobs: Tick samples every target, and a batch goes out once Batch
// has passed since the last one; Finish sends the final batch. A sender error drops that batch and never
// fails the job (the controller answers nothing; a lost batch only leaves a gap, R5.19).
type Collector struct {
	Cgroup      Sampler
	ProcessTree Sampler
	// JobObject reads a Windows job; nil on other systems.
	JobObject Sampler
	Sender    Sender
	// Forget is called when a job ends, to drop sampler state; may be nil.
	Forget func(Target)
	// Enabled says whether the controller's HelloAck allowed version 4. Nil means enabled.
	Enabled func() bool

	mu   sync.Mutex
	jobs map[string]*jobState
}

type jobState struct {
	t       Target
	buf     []Sample
	sentAt  time.Time
	lastAt  time.Time // when the job was last read; zero before the first read
	unknown string
	// oom and peak keep what the readers said last, for the final batch.
	oom  bool
	peak int64
}

// note keeps the facts a reading carries.
func (js *jobState) note(s Sample) {
	js.oom = js.oom || s.OOMKilled
	if s.MemPeakBytes > js.peak {
		js.peak = s.MemPeakBytes
	}
}

func key(t Target) string { return t.RunnerID }

// Start begins sampling a job at now.
func (c *Collector) Start(t Target, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jobs == nil {
		c.jobs = map[string]*jobState{}
	}
	js := &jobState{t: t, sentAt: now}
	if c.sampler(t) == nil {
		js.unknown = "no reader for this system or backend"
	}
	c.jobs[key(t)] = js
}

func (c *Collector) enabled() bool { return c.Enabled == nil || c.Enabled() }

func (c *Collector) sampler(t Target) Sampler {
	switch {
	case t.CgroupPath != "":
		return c.Cgroup
	case t.JobObject != "":
		return c.JobObject
	}
	return c.ProcessTree
}

// Tick takes one sample of every job and sends the batches that are due.
func (c *Collector) Tick(ctx context.Context, now time.Time) {
	if !c.enabled() {
		return
	}
	type out struct {
		t       Target
		src     Source
		batch   []Sample
		unknown string
	}
	var due []out
	c.mu.Lock()
	for _, js := range c.jobs {
		s := c.sampler(js.t)
		if s == nil {
			continue
		}
		// A slower reader keeps its own period; a tick that comes early skips it. A tick may be a little
		// early, so a tenth of the period is slack.
		if p := s.Interval(); !js.lastAt.IsZero() && now.Sub(js.lastAt) < p-p/10 {
			continue
		}
		smp, reason, err := s.Sample(ctx, js.t, now)
		if err != nil {
			continue
		}
		js.lastAt = now
		js.note(smp)
		if !smp.Known && reason != "" {
			js.unknown = reason
		}
		js.buf = append(js.buf, smp)
		if now.Sub(js.sentAt) >= Batch {
			due = append(due, out{js.t, s.Source(), js.buf, js.unknown})
			js.buf, js.sentAt, js.unknown = nil, now, ""
		}
	}
	c.mu.Unlock()
	for _, o := range due {
		_ = c.Sender.Send(ctx, o.t, o.src, o.batch, false, o.unknown)
	}
}

// Finish takes a last sample, sends what is buffered as the final batch and forgets the job. cut says the
// job ended before it could be read to its end (the agent stops, the runner is gone): the controller then
// does not size from it (OPS66 R5.61).
func (c *Collector) Finish(ctx context.Context, t Target, now time.Time, cut bool) {
	c.mu.Lock()
	js := c.jobs[key(t)]
	delete(c.jobs, key(t))
	c.mu.Unlock()
	if js == nil {
		return
	}
	defer func() {
		if c.Forget != nil {
			c.Forget(js.t)
		}
	}()
	if !c.enabled() {
		return
	}
	s := c.sampler(js.t)
	if s == nil {
		// Nothing can read this job: say so, so the screen shows "not measured" with the reason.
		_ = c.Sender.Send(ctx, js.t, ProcessTree, nil, true, js.unknown)
		return
	}
	if smp, reason, err := s.Sample(ctx, js.t, now); err == nil {
		if !smp.Known && reason != "" {
			js.unknown = reason
		}
		js.buf = append(js.buf, smp)
		js.note(smp)
	}
	if n, ok := c.Sender.(OutcomeNoter); ok {
		n.NoteOutcome(js.t, Outcome{OOMKilled: js.oom, CutShort: cut, MemoryPeakBytes: js.peak})
	}
	_ = c.Sender.Send(ctx, js.t, s.Source(), js.buf, true, js.unknown)
}

// Run ticks on every value of ticks until ctx ends or ticks closes.
func (c *Collector) Run(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			c.Tick(ctx, now)
		}
	}
}
