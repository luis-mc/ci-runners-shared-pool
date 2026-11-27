package tray

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State file: the tray's own small state in the user's config folder (R6.2).
// It holds no secret and no token.
type fileState struct {
	// NoTrayNoticeVersion is the version for which the GNOME notice was shown (R5.24).
	NoTrayNoticeVersion string `json:"no_tray_notice_version,omitempty"`
	// UpdatedNoticeVersion is the version for which "Updated to vX" was shown (CRSP147): once per version.
	UpdatedNoticeVersion string `json:"updated_notice_version,omitempty"`
}

func statePath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "crsp", "tray.json"), nil
}

func loadState(path string) fileState {
	var s fileState
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveState(path string, s fileState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFile(path, string(b))
}

// noticeDue reports whether the no-tray-area notice should show for version, and records it.
func noticeDue(path, version string) bool {
	s := loadState(path)
	if s.NoTrayNoticeVersion == version {
		return false
	}
	s.NoTrayNoticeVersion = version
	_ = saveState(path, s)
	return true
}

// updatedDue reports whether "Updated to <version>" should show, and records it, so
// two trays started after one install (the updater's and the old tray's) say it once.
func updatedDue(path, version string) bool {
	s := loadState(path)
	if s.UpdatedNoticeVersion == version {
		return false
	}
	s.UpdatedNoticeVersion = version
	_ = saveState(path, s)
	return true
}
