package linux

import (
	"context"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// Listener is the listener control of the systemd units (runners R5.134). A
// unit stopped cleanly stays stopped: Restart=on-failure does not restart it
// (R5.21); a start is `systemctl start`, a stop `systemctl stop` (SIGTERM, then
// the wait for the listener to exit by itself).
func (b *Backend) Listener() *persistent.Control {
	return &persistent.Control{
		Folder: func(ctx context.Context, ref string) (string, bool, error) {
			f, ok, err := b.lookup(ctx, ref)
			return f.Folder, ok, err
		},
		Listeners: func(ctx context.Context, folder string) ([]int, error) {
			return persistent.UnixListeners(ctx, b.d.Cmd, folder)
		},
		Workers: func(ctx context.Context, folder string) ([]int, error) {
			return persistent.UnixWorkers(ctx, b.d.Cmd, folder)
		},
		StartService: func(ctx context.Context, ref string, _ bool) error {
			_, err := b.run(ctx, "start", sys.Command{Name: "systemctl", Args: []string{"start", ref + ".service"}})
			return err
		},
		StopService: func(ctx context.Context, ref string) error {
			_, err := b.run(ctx, "stop", sys.Command{Name: "systemctl", Args: []string{"stop", ref + ".service"}})
			return err
		},
		Sleep: b.d.Sleep,
	}
}

var _ backend.ListenerControl = (*Backend)(nil)

// StartListener implements backend.ListenerControl.
func (b *Backend) StartListener(ctx context.Context, ref string, maintenance bool) error {
	return b.Listener().StartListener(ctx, ref, maintenance)
}

// StopListener implements backend.ListenerControl.
func (b *Backend) StopListener(ctx context.Context, ref string, jobAssigned bool) error {
	return b.Listener().StopListener(ctx, ref, jobAssigned)
}

// ListenerState implements backend.ListenerControl.
func (b *Backend) ListenerState(ctx context.Context, ref string) (backend.ListenerState, error) {
	return b.Listener().ListenerState(ctx, ref)
}
