package agent

import (
	"context"
	"errors"
	"runtime"

	"connectrpc.com/connect"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/recovery"
)

// ErrNeedsRecovery is the controller's refusal of a Session because this
// agent is too old for the agent link (operations R5.44). The agent keeps only
// the frozen recovery call.
var ErrNeedsRecovery = errors.New("the controller says this agent needs recovery")

// Recoverer applies a signed recovery target: it installs the release with the
// same checks as an update (SHA-256 and attestation). *update.Manager through
// a small adapter implements it.
type Recoverer interface {
	Recover(ctx context.Context, t recovery.Target) error
}

func isTooOld(err error) bool {
	return connect.CodeOf(err) == connect.CodeFailedPrecondition
}

// recoverOnce makes the one recovery call and, for a valid signed target,
// hands it to the Recoverer. A target that fails any check is dropped.
func recoverOnce(ctx context.Context, cfg Config, key, id, pin string) error {
	if cfg.Recoverer == nil {
		return errors.New("this agent cannot recover itself")
	}
	c := &recovery.Client{HTTP: pinnedClient(pin), Controller: cfg.Controller, Pin: pin, MachineID: id, Key: key}
	t, err := c.Poll(ctx, cfg.Version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	return cfg.Recoverer.Recover(ctx, t)
}
