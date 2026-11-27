//go:build !windows

package tray

// MenuOpen says whether the user has the tray menu open. Linux and macOS draw the menu in
// the desktop host, and an item change reaches it in place without closing it (R5.45).
func MenuOpen() bool { return false }
