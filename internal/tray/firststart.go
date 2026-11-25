package tray

import (
	"os"
	"path/filepath"
)

// First start (TRAY7, decision tray D20): on macOS and Linux the first time a person's tray
// runs it opens one window: Welcome, choices, Done. It is shown once per user; a marker file in
// the config folder says it was.

// FirstStartCommand is the hidden command of crsp that runs the first-start window.
const FirstStartCommand = "first-start"

// FirstStartMarker is the file whose presence says the window was shown.
func FirstStartMarker() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "crsp", "first-start-shown")
}

// FirstStartDue says this user has not seen the first-start window.
func FirstStartDue() bool {
	_, err := os.Stat(FirstStartMarker())
	return err != nil
}

// MarkFirstStartShown records that the window was shown, so it is never shown again.
func MarkFirstStartShown() error {
	p := FirstStartMarker()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte("shown\n"), 0o600)
}
