package tray

import (
	"sync"
	"time"
)

// NoticeWindow is the time one notice per event stays quiet: one notice per event per 24 hours (LC-22).
const NoticeWindow = 24 * time.Hour

// Notices lets one notice per event through in each NoticeWindow. The tray only notifies.
type Notices struct {
	Now  func() time.Time // nil: time.Now
	mu   sync.Mutex
	last map[string]time.Time
}

// Allow says whether the notice for event may show now, and records it when it may.
func (n *Notices) Allow(event string) bool {
	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	t := now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if at, ok := n.last[event]; ok && t.Sub(at) < NoticeWindow {
		return false
	}
	if n.last == nil {
		n.last = map[string]time.Time{}
	}
	n.last[event] = t
	return true
}
