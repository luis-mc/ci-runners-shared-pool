package mirror

import (
	"context"
	"fmt"
	"path/filepath"
)

// Settings are the pool's mirror switches (caches R5.66). Each switch is off
// until turned on.
type Settings struct {
	ActionsCache   bool `json:"actions_cache"`
	GitMirror      bool `json:"git_mirror"`
	RegistryMirror bool `json:"registry_mirror"`
	GitRefreshMin  int  `json:"git_refresh_minutes"`
	WarmthHours    int  `json:"warmth_hours"`
}

// Defaults of the numbers (R5.66, R5.11, R5.28).
const (
	DefaultRefreshMinutes = 15
	DefaultWarmthHours    = 24
)

// Normalize fills the defaults for a zero number.
func (s Settings) Normalize() Settings {
	if s.GitRefreshMin == 0 {
		s.GitRefreshMin = DefaultRefreshMinutes
	}
	if s.WarmthHours == 0 {
		s.WarmthHours = DefaultWarmthHours
	}
	return s
}

// Validate says what is wrong with a setting, in a sentence fit to show.
func (s Settings) Validate() error {
	if s.GitRefreshMin < 0 || s.GitRefreshMin > 1440 {
		return fmt.Errorf("git refresh must be 1 to 1440 minutes, not %d", s.GitRefreshMin)
	}
	if s.WarmthHours < 0 || s.WarmthHours > 720 {
		return fmt.Errorf("warmth must last 1 to 720 hours, not %d", s.WarmthHours)
	}
	return nil
}

// SettingsStore keeps the settings; the controller backs it with the settings
// table.
type SettingsStore interface {
	GetMirrorSettings(ctx context.Context) (Settings, error)
	SetMirrorSettings(ctx context.Context, s Settings) error
}

// Layout is the base folders of one cache holder machine (R5.65).
type Layout struct{ Base string }

// LayoutFor puts the base next to the tool cache root: its parent folder.
func LayoutFor(toolCacheRoot string) Layout { return Layout{Base: filepath.Dir(toolCacheRoot)} }

// Actions is the folder of the receiver blobs.
func (l Layout) Actions() string { return filepath.Join(l.Base, "actions") }

// Git is the folder of the bare mirrors.
func (l Layout) Git() string { return filepath.Join(l.Base, "git") }

// Registry is the folder of the registry mirror data.
func (l Layout) Registry() string { return filepath.Join(l.Base, "registry") }
