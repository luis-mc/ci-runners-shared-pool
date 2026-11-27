//go:build linux

// Command snihost is a minimal StatusNotifierWatcher for the CI smoke on Linux:
// it owns org.kde.StatusNotifierWatcher on the session bus and accepts item
// registrations, so the tray finds a tray area under xvfb.
package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

type watcher struct{ conn *dbus.Conn }

func (w *watcher) RegisterStatusNotifierItem(sender dbus.Sender, service string) *dbus.Error {
	return nil
}
func (w *watcher) RegisterStatusNotifierHost(sender dbus.Sender, service string) *dbus.Error {
	return nil
}

const iface = "org.kde.StatusNotifierWatcher"

func main() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "snihost:", err)
		os.Exit(1)
	}
	w := &watcher{conn: conn}
	if err := conn.Export(w, "/StatusNotifierWatcher", iface); err != nil {
		fmt.Fprintln(os.Stderr, "snihost:", err)
		os.Exit(1)
	}
	node := introspect.Node{Name: "/StatusNotifierWatcher", Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		{Name: iface, Methods: introspect.Methods(w)},
	}}
	_ = conn.Export(introspect.NewIntrospectable(&node), "/StatusNotifierWatcher", "org.freedesktop.DBus.Introspectable")
	reply, err := conn.RequestName(iface, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintln(os.Stderr, "snihost: cannot own", iface)
		os.Exit(1)
	}
	fmt.Println("snihost ready")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
