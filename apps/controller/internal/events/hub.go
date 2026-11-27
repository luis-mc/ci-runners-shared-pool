package events

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Defaults of observability R6.2 and R6.3.
const (
	DefaultMaxStreams   = 50
	CeilingMaxStreams   = 200
	DefaultReplayEvents = 2000
	DefaultReplayAge    = 10 * time.Minute
	DefaultMaxBehind    = 500
	DefaultLoadEvery    = 2 * time.Second
)

// Options configure a MemHub. A zero field takes its default.
type Options struct {
	// Snapshotter builds the snapshot sent first (R5.2). Required.
	Snapshotter Snapshotter
	// MaxStreams caps open streams (R6.3); above CeilingMaxStreams it is lowered.
	MaxStreams int
	// ReplayEvents and ReplayAge bound the replay ring: whichever is less.
	ReplayEvents int
	ReplayAge    time.Duration
	// MaxBehind is how many undelivered events close a subscriber.
	MaxBehind int
	// LoadEvery is the least gap between two machine.load events of one machine.
	LoadEvery time.Duration
	// Redact runs over every payload before it is stored (R6.5). Nil keeps it.
	Redact func([]byte) []byte
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// MemHub is the in-memory Hub of the controller.
type MemHub struct {
	opt Options

	mu       sync.Mutex
	last     uint64
	ring     []Event
	subs     map[*sub]struct{}
	loadSeen map[string]time.Time
	stopped  bool
}

var _ Hub = (*MemHub)(nil)

// NewHub makes a hub.
func NewHub(o Options) *MemHub {
	if o.MaxStreams <= 0 {
		o.MaxStreams = DefaultMaxStreams
	}
	if o.MaxStreams > CeilingMaxStreams {
		o.MaxStreams = CeilingMaxStreams
	}
	if o.ReplayEvents <= 0 {
		o.ReplayEvents = DefaultReplayEvents
	}
	if o.ReplayAge <= 0 {
		o.ReplayAge = DefaultReplayAge
	}
	if o.MaxBehind <= 0 {
		o.MaxBehind = DefaultMaxBehind
	}
	if o.LoadEvery <= 0 {
		o.LoadEvery = DefaultLoadEvery
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &MemHub{opt: o, subs: map[*sub]struct{}{}, loadSeen: map[string]time.Time{}}
}

// LastID is the id of the newest event, 0 before the first.
func (h *MemHub) LastID() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// Streams is the number of open subscriptions.
func (h *MemHub) Streams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Publish implements Publisher. It never blocks: a subscriber whose queue is
// full is closed instead (R6.3).
func (h *MemHub) Publish(t Type, data []byte) {
	if h.opt.Redact != nil {
		data = h.opt.Redact(data)
	}
	now := h.opt.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return
	}
	if t == MachineLoad {
		key := machineKey(data)
		if seen, ok := h.loadSeen[key]; ok && now.Sub(seen) < h.opt.LoadEvery {
			return
		}
		h.loadSeen[key] = now
	}
	h.last++
	ev := Event{ID: h.last, Type: t, At: now.UTC(), Data: append([]byte(nil), data...)}
	h.ring = append(h.ring, ev)
	h.prune(now)
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			h.drop(s)
		}
	}
}

// machineKey reads machine_id from a machine.load payload.
func machineKey(data []byte) string {
	var v struct {
		MachineID string `json:"machine_id"`
	}
	_ = json.Unmarshal(data, &v)
	return v.MachineID
}

// prune drops ring entries past the count or age limits. Caller holds mu.
func (h *MemHub) prune(now time.Time) {
	cut := 0
	if n := len(h.ring) - h.opt.ReplayEvents; n > 0 {
		cut = n
	}
	for cut < len(h.ring) && now.Sub(h.ring[cut].At) > h.opt.ReplayAge {
		cut++
	}
	if cut > 0 {
		h.ring = append([]Event(nil), h.ring[cut:]...)
	}
}

// drop closes one subscriber. Caller holds mu.
func (h *MemHub) drop(s *sub) {
	if _, ok := h.subs[s]; !ok {
		return
	}
	delete(h.subs, s)
	close(s.ch)
}

// Stop closes every subscription and refuses new events.
func (h *MemHub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	for s := range h.subs {
		h.drop(s)
	}
}

type sub struct {
	h  *MemHub
	ch chan Event
}

func (s *sub) Events() <-chan Event { return s.ch }

func (s *sub) Close() {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	s.h.drop(s)
}

// Subscribe implements Hub (R5.2, R5.3, R6.3).
func (h *MemHub) Subscribe(ctx context.Context, lastEventID uint64) (Subscription, error) {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil, context.Canceled
	}
	if len(h.subs) >= h.opt.MaxStreams {
		h.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	h.prune(h.opt.Now())
	needSnapshot, gap := true, false
	switch {
	case lastEventID == 0:
	case lastEventID == h.last:
		needSnapshot = false
	case lastEventID < h.last && len(h.ring) > 0 && h.ring[0].ID <= lastEventID+1:
		needSnapshot = false
	default:
		gap = true // events missed are gone, or the id is from before a restart
	}
	snapID := h.last
	h.mu.Unlock()

	var snap Event
	if needSnapshot {
		data, err := h.opt.Snapshotter.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		snap = Event{ID: snapID, Type: Snapshot, At: h.opt.Now().UTC(), Data: data, ReplayGap: gap}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return nil, context.Canceled
	}
	if len(h.subs) >= h.opt.MaxStreams {
		return nil, ErrTooManyStreams
	}
	from := lastEventID
	if needSnapshot {
		from = snapID
	}
	var initial []Event
	if needSnapshot {
		initial = append(initial, snap)
	}
	for _, e := range h.ring {
		if e.ID > from {
			initial = append(initial, e)
		}
	}
	s := &sub{h: h, ch: make(chan Event, h.opt.MaxBehind+len(initial))}
	for _, e := range initial {
		s.ch <- e
	}
	h.subs[s] = struct{}{}
	return s, nil
}
