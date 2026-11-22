package listenergate

import (
	"context"
	"sync"
	"time"
)

// SettingsSource reads and saves the gate settings (SettingsStore).
type SettingsSource interface {
	Get(ctx context.Context) Settings
	Save(ctx context.Context, next Settings, by string) error
}

// Control is what the API and the doctor call: the setting and the console view.
type Control struct {
	G        *Gate
	Settings SettingsSource

	mu  sync.Mutex
	cur Settings
	set bool
}

// Current returns the settings in force; the gate reads it on every call. The
// first call reads the store, Save refreshes it.
func (s *Control) Current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		s.cur = s.Settings.Get(context.Background())
		s.set = true
	}
	return s.cur
}

// GateEnforced says whether the gate acts; false is observe mode. The doctor
// reads it.
func (s *Control) GateEnforced(context.Context) (bool, error) { return s.Current().Enforce, nil }

// Save saves the enforce setting.
func (s *Control) Save(ctx context.Context, enforce bool, by string) (Settings, error) {
	next := Settings{Enforce: enforce}
	if err := s.Settings.Save(ctx, next, by); err != nil {
		return s.Current(), err
	}
	s.mu.Lock()
	s.cur, s.set = next, true
	s.mu.Unlock()
	return next, nil
}

// View is what the console shows.
type View struct {
	Enforce   bool
	WakeEvery time.Duration
	Runners   []RunnerStatus
}

// View returns the setting and every gated runner.
func (s *Control) View(ctx context.Context) (View, error) {
	rs, err := s.G.Status(ctx)
	return View{Enforce: s.Current().Enforce, WakeEvery: WakeEvery, Runners: rs}, err
}
