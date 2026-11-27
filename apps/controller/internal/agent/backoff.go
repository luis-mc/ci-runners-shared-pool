package agent

import (
	"math/rand/v2"
	"time"
)

// Backoff is the reconnect delay of R5.36: 1 second, doubled each time, capped
// at 60 seconds, with up to 20% random jitter. A successful session resets it.
type Backoff struct {
	next time.Duration
	// Rand returns a number in [0,1); rand.Float64 when nil.
	Rand func() float64
}

// Backoff limits.
const (
	BackoffStart  = time.Second
	BackoffMax    = 60 * time.Second
	BackoffJitter = 0.2
)

// Next returns the delay before the next try and doubles the base.
func (b *Backoff) Next() time.Duration {
	if b.next == 0 {
		b.next = BackoffStart
	}
	base := b.next
	b.next *= 2
	if b.next > BackoffMax {
		b.next = BackoffMax
	}
	r := rand.Float64
	if b.Rand != nil {
		r = b.Rand
	}
	return base + time.Duration(float64(base)*BackoffJitter*r())
}

// Reset starts again at one second; call it after a successful session.
func (b *Backoff) Reset() { b.next = 0 }
