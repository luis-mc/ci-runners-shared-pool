// Package tray is the `crsp tray` mode: a notification-area icon that shows the
// state of the local service, opens the web console through a one-time link,
// and keeps a per-user autostart entry. Spec: docs/features/tray.
package tray

// ServiceName is the OS service name of the controller or agent on this machine.
const ServiceName = "crsp"

// Exit codes of `crsp tray` (spec section 7).
const (
	ExitOK         = 0
	ExitNoTrayHost = 2
	ExitNoSession  = 3
)
