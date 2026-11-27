//go:build linux

package tray

import "testing"

func TestNoSessionExit3(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if ProbeHost() != HostNoSession {
		t.Fatal("no display and no bus must report no session")
	}
	if ExitNoSession != 3 || ExitNoTrayHost != 2 {
		t.Fatal("exit codes")
	}
	var o Options
	o.Stderr = discard{}
	if code := Run(nil, o); code != ExitNoSession {
		t.Fatalf("exit %d", code)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
