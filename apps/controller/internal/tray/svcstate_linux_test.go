//go:build linux

package tray

func parseSystemctlForTest(s string) (Status, bool) { return parseSystemctl(s) }
