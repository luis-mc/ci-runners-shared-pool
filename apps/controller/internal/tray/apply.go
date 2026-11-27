package tray

import (
	"context"
	"sync"
	"time"
)

// Surface is the live tray: the icon, the tooltip and the menu items. The real one wraps
// fyne systray; a test puts a fake in its place (TRAY6).
type Surface interface {
	SetTitle(id, title string)
	SetVisible(id string, visible bool)
	SetChecked(id string, checked bool)
	SetTooltip(text string)
	SetIcon(s State)
}

// FlushEvery is how often a queued change is tried again while the menu is open.
const FlushEvery = 250 * time.Millisecond

// Applier puts each status on the Surface without closing the menu the user is in (R5.45).
// It never rebuilds the menu: it changes an item only when its text, visibility or tick
// differs from what is shown, and while the menu is open it queues the newest status and
// applies it when the menu closes.
type Applier struct {
	Surf  Surface
	Build func(Status) []MenuItem
	// Open says whether the user has the menu open now. Nil means never (Linux and macOS
	// draw the menu in the desktop host, which updates items in place).
	Open func() bool

	mu      sync.Mutex
	shown   map[string]MenuItem
	tip     string
	state   State
	haveTip bool
	pending *Status
}

// Seed records the menu as first built, so the next status changes only what differs.
func (a *Applier) Seed(items []MenuItem, s Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.shown = map[string]MenuItem{}
	for _, it := range items {
		a.shown[it.ID] = it
	}
	a.tip, a.state, a.haveTip = s.Tooltip(), s.State, true
}

// Update applies s now, or queues it (newest wins) while the menu is open.
func (a *Applier) Update(s Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Open != nil && a.Open() {
		a.pending = &s
		return
	}
	a.pending = nil
	a.apply(s)
}

// Flush applies the queued status once the menu is closed. It reports whether it did.
func (a *Applier) Flush() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil || (a.Open != nil && a.Open()) {
		return false
	}
	s := *a.pending
	a.pending = nil
	a.apply(s)
	return true
}

// Pending says whether a status waits for the menu to close.
func (a *Applier) Pending() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending != nil
}

// Run flushes the queue until ctx ends.
func (a *Applier) Run(ctx context.Context) {
	t := time.NewTicker(FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Flush()
		}
	}
}

// apply writes the differences. The caller holds a.mu.
func (a *Applier) apply(s Status) {
	for _, it := range a.Build(s) {
		if it.Separator {
			continue
		}
		old, known := a.shown[it.ID]
		if known && old.Label != it.Label {
			a.Surf.SetTitle(it.ID, it.Label)
		}
		// Visibility is asserted from every status, not only on a change: a dropped call
		// must not leave an item (the Update line) shown or hidden for good. It is idempotent.
		if known {
			a.Surf.SetVisible(it.ID, !it.Hidden)
		}
		// Start at login is the user's own tick, not the service's: the status never changes it.
		if known && it.ID != IDLogin && (it.Switch || it.Radio) && old.Checked != it.Checked {
			a.Surf.SetChecked(it.ID, it.Checked)
		}
		if known && it.ID == IDLogin {
			it.Checked = old.Checked
		}
		a.shown[it.ID] = it
	}
	if tip := s.Tooltip(); !a.haveTip || tip != a.tip {
		a.Surf.SetTooltip(tip)
		a.tip = tip
	}
	if !a.haveTip || s.State != a.state {
		a.Surf.SetIcon(s.State)
		a.state = s.State
	}
	a.haveTip = true
}
