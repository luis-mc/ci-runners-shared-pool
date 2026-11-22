package macos

import (
	"context"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// Listener is the listener control of the launchd daemons (runners R5.134). A
// stop is `launchctl bootout` and a start `launchctl bootstrap`, both from the
// root agent. The plist restarts the listener only after a failure (R5.23), so
// a clean stop stays stopped.
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
			_, err := b.run(ctx, "bootstrap", sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(ref)}})
			return err
		},
		StopService: func(ctx context.Context, ref string) error { return b.bootout(ctx, ref) },
		Sleep:       b.d.Sleep,
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
