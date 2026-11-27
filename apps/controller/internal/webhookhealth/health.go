// Package webhookhealth keeps, per repository, the last webhook delivery time
// and result, and the delivery mode (github-integration, RES14). When
// deliveries fail the repository switches to polling; the console says so and
// offers a switch back.
package webhookhealth

import (
	"strings"
	"sync"
	"time"
)

// Result is what happened to the last delivery.
type Result string

// Delivery results.
const (
	ResultOK     Result = "ok"
	ResultFailed Result = "failed" // a verified delivery the controller could not use
	ResultMissed Result = "missed" // polling saw a queued job that no delivery announced
)

// Mode is how the controller learns about a repository's jobs.
type Mode string

// Delivery modes.
const (
	ModeWebhook Mode = "webhook"
	ModePolling Mode = "polling"
)

// Tunables.
const (
	// FailuresToPoll is how many failures in a row switch a repository to polling.
	FailuresToPoll = 3
	// MissedGrace is how long a queued job may be seen by polling before a
	// missing delivery for it counts as missed.
	MissedGrace = 2 * time.Minute
	maxTracked  = 4096
)

// State is one repository's webhook health.
type State struct {
	LastAt   time.Time
	Last     Result
	Failures int // in a row
	Mode     Mode
	// SwitchedAt is when polling took over; zero in webhook mode.
	SwitchedAt time.Time
}

// Tracker is safe for concurrent use.
type Tracker struct {
	// Now is a seam for tests.
	Now func() time.Time
	// HooksOn says the GitHub App sends webhooks at all. Nil means yes. An
	// App made without a webhook URL (the default for a controller behind a
	// LAN address, github-integration R5.5) never announces a job, so the
	// poller finding every job first is no missed delivery (OPS22).
	HooksOn func() bool

	mu      sync.Mutex
	repos   map[string]*State
	hooked  map[int64]struct{}   // job ids a delivery announced
	pending map[int64]pendingJob // jobs polling saw first
}

type pendingJob struct {
	repo string
	at   time.Time
}

// New returns an empty tracker.
func New() *Tracker {
	return &Tracker{Now: time.Now, repos: map[string]*State{}, hooked: map[int64]struct{}{}, pending: map[int64]pendingJob{}}
}

func key(fullName string) string { return strings.ToLower(strings.TrimSpace(fullName)) }

func (t *Tracker) state(repo string) *State {
	s := t.repos[repo]
	if s == nil {
		if len(t.repos) >= maxTracked {
			return &State{Mode: ModeWebhook}
		}
		s = &State{Mode: ModeWebhook}
		t.repos[repo] = s
	}
	return s
}

// Record notes one delivery result for a repository (owner/name). The third
// failure in a row switches it to polling.
func (t *Tracker) Record(fullName string, r Result) {
	repo := key(fullName)
	if repo == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	s := t.state(repo)
	s.LastAt, s.Last = now, r
	if r == ResultOK {
		s.Failures = 0
		return
	}
	s.Failures++
	if s.Failures >= FailuresToPoll && s.Mode != ModePolling {
		s.Mode, s.SwitchedAt = ModePolling, now
	}
}

// Delivered notes that a delivery announced a job. It counts as an ok
// delivery and settles a job polling saw first.
func (t *Tracker) Delivered(fullName string, jobID int64) {
	t.mu.Lock()
	if len(t.hooked) >= maxTracked {
		t.hooked = map[int64]struct{}{}
	}
	t.hooked[jobID] = struct{}{}
	delete(t.pending, jobID)
	t.mu.Unlock()
	t.Record(fullName, ResultOK)
}

// PollSawQueued is called by the poller for every queued job it lists. A job
// that polling saw more than MissedGrace ago and no delivery announced counts
// as a missed delivery for its repository.
func (t *Tracker) PollSawQueued(fullName string, jobID int64) {
	if t.WebhooksOff() {
		return
	}
	repo := key(fullName)
	t.mu.Lock()
	now := t.Now()
	if _, ok := t.hooked[jobID]; ok {
		t.mu.Unlock()
		return
	}
	p, seen := t.pending[jobID]
	switch {
	case !seen:
		if len(t.pending) >= maxTracked {
			t.pending = map[int64]pendingJob{}
		}
		t.pending[jobID] = pendingJob{repo: repo, at: now}
		t.mu.Unlock()
		return
	case now.Sub(p.at) < MissedGrace:
		t.mu.Unlock()
		return
	}
	delete(t.pending, jobID)
	t.hooked[jobID] = struct{}{} // count one job once
	t.mu.Unlock()
	t.Record(fullName, ResultMissed)
}

// WebhooksOff reports that the App has no webhook, so polling is the only way
// jobs are learned and nothing can be missed.
func (t *Tracker) WebhooksOff() bool { return t.HooksOn != nil && !t.HooksOn() }

// Get returns a repository's state; a repository never seen is in webhook
// mode with no delivery.
func (t *Tracker) Get(fullName string) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.repos[key(fullName)]; s != nil {
		return *s
	}
	return State{Mode: ModeWebhook}
}

// Last returns the time and result of the newest delivery of any repository;
// zero and "" when none arrived (OPS25).
func (t *Tracker) Last() (time.Time, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var at time.Time
	var res Result
	for _, s := range t.repos {
		if s.LastAt.After(at) {
			at, res = s.LastAt, s.Last
		}
	}
	return at, string(res)
}

// SwitchBack returns a repository to webhook mode and forgets its failures.
func (t *Tracker) SwitchBack(fullName string) State {
	repo := key(fullName)
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.state(repo)
	s.Mode, s.Failures, s.SwitchedAt = ModeWebhook, 0, time.Time{}
	return *s
}
