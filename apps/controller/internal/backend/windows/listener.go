package windows

import (
	"context"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
)

// Listener is the listener control of the Windows services (runners R5.134). The
// agent runs as LocalSystem, which may start and stop a service of another
// account (machines R5.44). A start that fails with a logon error is healed by
// the service heal step (R5.89) and tried again by the gate.
func (b *Backend) Listener() *persistent.Control {
	return &persistent.Control{
		Folder: func(ctx context.Context, ref string) (string, bool, error) {
			f, ok, err := b.lookup(ctx, ref)
			return f.Folder, ok, err
		},
		Listeners: func(ctx context.Context, folder string) ([]int, error) { return b.psPIDs(ctx, scriptListeners, folder) },
		Workers:   func(ctx context.Context, folder string) ([]int, error) { return b.psPIDs(ctx, scriptWorkers, folder) },
		StartService: func(ctx context.Context, ref string, _ bool) error {
			return b.psOK(ctx, "start", scriptStartService, map[string]string{"CRSP_SERVICE": ref}, nil)
		},
		StopService: func(ctx context.Context, ref string) error {
			return b.psOK(ctx, "stop", scriptStopService, map[string]string{"CRSP_SERVICE": ref}, nil)
		},
		Sleep: b.d.Sleep,
	}
}

func (b *Backend) psPIDs(ctx context.Context, script, folder string) ([]int, error) {
	res, err := b.ps(ctx, script, map[string]string{"CRSP_FOLDER": folder}, nil)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list processes: powershell exited %d", res.ExitCode)
	}
	return persistent.ParsePIDs(res.Stdout), nil
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
