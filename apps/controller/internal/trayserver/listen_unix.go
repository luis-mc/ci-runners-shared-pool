//go:build !windows

package trayserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// Group is the OS group that may use the channel (tray R6.8).
const Group = "crsp"

// DefaultPath is the socket path of this OS (CRSP_TRAY_SOCKET overrides it).
func DefaultPath() string { return tray.SocketPath() }

// Listen opens the Unix socket at path: a stale socket file is replaced, the
// mode is 0660, and the group is `crsp` when that group exists.
func Listen(path string, logger *slog.Logger) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the socket folder is traversable; the socket itself is 0660
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, err
	}
	if g, err := user.LookupGroup(Group); err == nil {
		if gid, err := strconv.Atoi(g.Gid); err == nil {
			if err := os.Chown(path, -1, gid); err != nil && logger != nil {
				logger.Warn("tray channel: could not give the socket to the crsp group", "err", err)
			}
		}
	} else if logger != nil {
		logger.Warn("tray channel: no crsp group; only the service user can use the socket", "group", Group)
	}
	return ln, nil
}
