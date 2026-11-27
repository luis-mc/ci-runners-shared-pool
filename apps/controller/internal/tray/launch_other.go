//go:build !windows

package tray

// StartForSignedInUsers starts the tray for each signed-in desktop user. It does
// nothing off Windows: there the tray relaunches itself (NeedsRelaunch).
func StartForSignedInUsers(string) (int, error) { return 0, nil }
