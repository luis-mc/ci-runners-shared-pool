//go:build !windows

package tray

import (
	"context"
	"net"
	"os"
	"runtime"
)

// SocketPath is the service's Unix socket (spec section 7).
func SocketPath() string {
	if p := os.Getenv("CRSP_TRAY_SOCKET"); p != "" {
		return p
	}
	if runtime.GOOS == "darwin" {
		return "/var/run/crsp/tray.sock"
	}
	return "/run/crsp/tray.sock"
}

// DefaultDialer dials the Unix socket.
func DefaultDialer() Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", SocketPath())
	}
}
