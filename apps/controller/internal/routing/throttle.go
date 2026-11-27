package routing

import (
	"context"
	"sync"
	"time"
)

// DefaultGap is the least time between two writes to GitHub. GitHub asks for
// at least one second between mutating requests; faster bursts meet the
// secondary rate limit (OPS65).
const DefaultGap = time.Second

// Throttle spaces writes. One Throttle serves every repository of a poller, so
// a bulk write over many repositories keeps the gap as a whole. The clock and
// the sleep are injected: a test never waits.
type Throttle struct {
	Gap   time.Duration
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	next time.Time
}

func (t *Throttle) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Wait returns when the next write may go out.
func (t *Throttle) Wait(ctx context.Context) error {
	gap := t.Gap
	if gap <= 0 {
		gap = DefaultGap
	}
	t.mu.Lock()
	now := t.now()
	at := t.next
	if at.Before(now) {
		at = now
	}
	t.next = at.Add(gap)
	d := at.Sub(now)
	t.mu.Unlock()
	if d <= 0 {
		return ctx.Err()
	}
	if t.Sleep != nil {
		return t.Sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Hold pushes the next write to until, after a secondary rate limit.
func (t *Throttle) Hold(until time.Time) {
	t.mu.Lock()
	if until.After(t.next) {
		t.next = until
	}
	t.mu.Unlock()
}
