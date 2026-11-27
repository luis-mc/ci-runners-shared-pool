package agentlink

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerswap"
)

// MaxBundle is the largest runner bundle the controller sends in one message.
const MaxBundle = 512 << 20

// ErrAgentCannotSwap is a machine whose agent does not say `runner_swap` in its
// machine report: an agent from before protocol version 2, or one with no
// runner folder access. Nothing is sent to it (runners R5.38).
var ErrAgentCannotSwap = errors.New("the agent of this machine cannot update runners yet")

// BundleSource gives the runner archive of a version for a machine (the
// official release asset of the pin for its OS and arch). The Updater checks
// its SHA-256 before it sends anything.
type BundleSource interface {
	Bundle(ctx context.Context, machineID string, req runners.UpdateRequest) ([]byte, error)
}

// Updater is the runners.RunnerUpdater of the agent link.
type Updater struct {
	L       *Link
	Bundles BundleSource
	// DrainTimeout is how long the agent waits for a job of the runner to end
	// before it answers `waiting`; zero takes one minute (the controller has
	// drained already, so this is only the second, last-moment check).
	DrainTimeout time.Duration
}

var _ runners.RunnerUpdater = (*Updater)(nil)

// UpdateRunner sends the bundle and waits for the agent's answer. A runner that
// still runs a job comes back as runners.ErrDrainTimeout; nothing was stopped.
func (u *Updater) UpdateRunner(ctx context.Context, machineID string, req runners.UpdateRequest) error {
	rep, ok := u.L.Reports()[machineID]
	if !ok || !rep.GetRunnerSwap() {
		return ErrAgentCannotSwap
	}
	if u.Bundles == nil {
		return errors.New("the controller has no source for runner bundles")
	}
	bundle, err := u.Bundles.Bundle(ctx, machineID, req)
	if err != nil {
		return fmt.Errorf("fetching the runner bundle: %w", err)
	}
	if len(bundle) > MaxBundle {
		return errors.New("the runner bundle is larger than the link allows")
	}
	if err := runnerswap.Verify(bundle, req.SHA256); err != nil {
		return fmt.Errorf("the runner download %w", err)
	}
	wait := u.DrainTimeout
	if wait <= 0 {
		wait = time.Minute
	}
	r, err := u.L.call(ctx, machineID, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_UpdateRunnerFiles{UpdateRunnerFiles: &agentv1.UpdateRunnerFiles{
			CommandId: id, RunnerId: req.RunnerID, RunnerName: req.Name, ServiceName: req.ServiceName,
			Version: req.Version, Sha256: req.SHA256, Bundle: bundle, DrainTimeoutSeconds: int32(wait / time.Second), //nolint:gosec // small
		}}}
	})
	if err != nil {
		return err
	}
	if r.GetOutcome() == "waiting" {
		return runners.ErrDrainTimeout{Job: r.GetError()}
	}
	return nil
}
