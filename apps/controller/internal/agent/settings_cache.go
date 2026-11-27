package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// cachedSettings is the last settings the agent got, kept on disk so the agent
// applies them while the link is down or after a restart (R5.24, R5.37).
type cachedSettings struct {
	Settings machines.Settings `json:"settings"`
	State    string            `json:"state"`
}

// SettingsCache keeps the last settings in a file. Settings hold no secret.
type SettingsCache struct{ Path string }

// Save writes the settings atomically.
func (c SettingsCache) Save(s machines.Settings, state string) error {
	if c.Path == "" {
		return nil
	}
	b, err := json.Marshal(cachedSettings{Settings: s, State: state})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

// Load reads the cached settings; ok is false when there are none.
func (c SettingsCache) Load() (s machines.Settings, state string, ok bool) {
	if c.Path == "" {
		return s, "", false
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return s, "", false
	}
	var cs cachedSettings
	if err := json.Unmarshal(b, &cs); err != nil {
		return s, "", false
	}
	return cs.Settings, cs.State, true
}

// ErrRemoved is returned by Run when the controller refused the key because the
// machine was removed (R5.39). The agent has stopped retrying and waits for a
// new join.
var ErrRemoved = errors.New("this machine was removed from the pool")
