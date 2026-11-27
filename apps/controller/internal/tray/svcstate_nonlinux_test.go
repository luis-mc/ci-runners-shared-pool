//go:build !linux

package tray

func parseSystemctlForTest(string) (Status, bool) { return Status{}, false }
