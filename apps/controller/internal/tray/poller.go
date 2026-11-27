package tray

import (
	"context"
	"time"
)

// StatusSource reads the local service over the channel.
type StatusSource interface {
	Status(ctx context.Context) (Status, error)
}

// StoppedSource reads the stop state from the OS service manager (R5.12).
type StoppedSource func(ctx context.Context) (Status, bool)

const (
	// PollInterval is how often the tray asks the service (R5.3).
	PollInterval = 5 * time.Second
	// StaleAfter is how long the last state is kept when the service is slow (spec section 4).
	StaleAfter = 15 * time.Second
)

// Poller asks the service every PollInterval and reports each reading.
type Poller struct {
	Source   StatusSource
	Manager  StoppedSource // optional
	Interval time.Duration
	Now      func() time.Time
	OnStatus func(Status)
}

// Poll reads once. It never blocks longer than the source's own timeout.
func (p *Poller) Poll(ctx context.Context, last Status, lastGood time.Time) (Status, time.Time) {
	now := p.now()
	st, err := p.Source.Status(ctx)
	if err == nil {
		return st, now
	}
	// No answer: keep the last good state for up to StaleAfter, then ask the OS.
	if !lastGood.IsZero() && now.Sub(lastGood) < StaleAfter {
		return last, lastGood
	}
	if p.Manager != nil {
		if ms, ok := p.Manager(ctx); ok {
			return ms, lastGood
		}
	}
	return Status{State: StateStarting}, lastGood
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	iv := p.Interval
	if iv <= 0 {
		iv = PollInterval
	}
	cur := Status{State: StateStarting}
	var good time.Time
	for {
		cur, good = p.Poll(ctx, cur, good)
		if p.OnStatus != nil {
			p.OnStatus(cur)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		}
	}
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
