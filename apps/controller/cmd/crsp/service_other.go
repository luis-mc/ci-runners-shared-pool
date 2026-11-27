//go:build !windows

package main

// runAsService is false off Windows: systemd and launchd start crsp as a plain
// process.
func runAsService() bool { return false }

func hideConsoleForTray(_ []string) {}
