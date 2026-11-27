//go:build linux

package tray

import "github.com/godbus/dbus/v5"

// DesktopNotice shows one desktop notification.
func DesktopNotice(title, body string) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").Call(
		"org.freedesktop.Notifications.Notify", 0, "crsp", uint32(0), "", title, body,
		[]string{}, map[string]dbus.Variant{}, int32(-1)).Err
}
