//go:build linux

package tray

import (
	"os"

	"github.com/godbus/dbus/v5"
)

// HostCheck says whether a tray can be shown here.
type HostCheck int

// Results of ProbeHost.
const (
	HostOK HostCheck = iota
	HostNoSession
	HostNoTrayArea
)

// ProbeHost looks for a graphical session and a StatusNotifierItem host on the
// session D-Bus (R5.24, R5.25).
func ProbeHost() HostCheck {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return HostNoSession
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return HostNoSession
	}
	defer func() { _ = conn.Close() }()
	return probeOn(conn)
}

func probeOn(conn *dbus.Conn) HostCheck {
	var has bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has)
	if err != nil || !has {
		return HostNoTrayArea
	}
	return HostOK
}
