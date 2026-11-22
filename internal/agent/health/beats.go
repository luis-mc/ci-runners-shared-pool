package health

import (
	"sync"
	"time"
)

// Beats keeps what the heartbeats and the session say about the link to the
// controller: the round trip, the last answer, the reconnects of 24 hours and
// the clock skew. The agent's link feeds it; the clock is injected.
type Beats struct {
	mu         sync.Mutex
	now        func() time.Time
	sentAt     time.Time
	rtt        time.Duration
	haveRTT    bool
	lastBeat   time.Time
	skew       time.Duration
	haveSkew   bool
	reconnects []time.Time
	opened     bool
}

// NewBeats makes a Beats; now nil is time.Now.
func NewBeats(now func() time.Time) *Beats {
	if now == nil {
		now = time.Now
	}
	return &Beats{now: now}
}

// Sent records that a heartbeat left.
func (b *Beats) Sent() {
	b.mu.Lock()
	b.sentAt = b.now()
	b.mu.Unlock()
}

// Replied records the controller's heartbeat answer; controllerMs is the
// controller's clock in the answer. The round trip is the time since Sent; the
// skew is the agent's clock at the middle of the trip minus the controller's.
func (b *Beats) Replied(controllerMs int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.lastBeat = now
	if b.sentAt.IsZero() {
		return
	}
	b.rtt, b.haveRTT = now.Sub(b.sentAt), true
	mid := b.sentAt.Add(b.rtt / 2)
	b.skew, b.haveSkew = mid.Sub(time.UnixMilli(controllerMs)), true
	b.sentAt = time.Time{}
}

// Reconnected records a session that opened after a lost one.
func (b *Beats) Reconnected() {
	b.mu.Lock()
	b.reconnects = append(b.reconnects, b.now())
	b.mu.Unlock()
}

// Opened records that a session opened; every one after the first counts as a reconnect.
func (b *Beats) Opened() {
	b.mu.Lock()
	again := b.opened
	b.opened = true
	b.mu.Unlock()
	if again {
		b.Reconnected()
	}
}

// BeatsView is a snapshot of Beats.
type BeatsView struct {
	RTT          time.Duration
	RTTKnown     bool
	LastBeat     time.Time
	Reconnects24 int
	Skew         time.Duration
	SkewKnown    bool
}

// View returns the figures and drops reconnects older than 24 hours.
func (b *Beats) View() BeatsView {
	b.mu.Lock()
	defer b.mu.Unlock()
	cut := b.now().Add(-24 * time.Hour)
	keep := b.reconnects[:0]
	for _, t := range b.reconnects {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	b.reconnects = keep
	return BeatsView{RTT: b.rtt, RTTKnown: b.haveRTT, LastBeat: b.lastBeat, Reconnects24: len(keep), Skew: b.skew, SkewKnown: b.haveSkew}
}
