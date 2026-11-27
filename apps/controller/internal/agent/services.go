package agent

import (
	"context"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

// The agent keeps every runner service able to log on (runners R5.89). On
// Windows a service account can lose "log on as a service" after it was made
// (a policy refresh at boot, or an account made again with a new SID), and
// the runner then stays offline without a word. The agent gives the right back
// at start, with every machine report (so after each boot and every
// RedetectEvery), and the backend does it again right before it starts a
// service. What the pass saw goes to the controller in the MachineReport.

// serviceHealer is the runner backend's heal step, when it has one.
func (c Config) serviceHealer() backend.ServiceHealer {
	h, _ := c.Runners.(backend.ServiceHealer)
	return h
}

// healServices runs one pass and returns what it saw, as the wire report. A
// pass that fails as a whole reports the services it could read; a service
// that did not start carries its reason in `problem`.
func healServices(ctx context.Context, h backend.ServiceHealer, log *slog.Logger, red *redact.Redactor) []*agentv1.RunnerServiceReport {
	if h == nil {
		return nil
	}
	got, err := h.HealServices(ctx)
	if err != nil && log != nil {
		log.Warn("runner services", "err", red.Redact(err.Error()))
	}
	out := make([]*agentv1.RunnerServiceReport, 0, len(got))
	for _, s := range got {
		out = append(out, &agentv1.RunnerServiceReport{
			RunnerName: s.Runner, ServiceName: s.Service, Account: s.Account, Running: s.Running,
			LogonKnown: s.LogonKnown, LogonHeld: s.LogonHeld, Regranted: s.Regranted, Problem: red.Redact(s.Problem),
		})
	}
	return out
}

// healAtStart is the pass at agent start: a boot that took the right away is
// put right before the controller is even reached.
func healAtStart(ctx context.Context, cfg Config) {
	healServices(ctx, cfg.serviceHealer(), cfg.Logger, cfg.redactor())
}

func (c Config) redactor() *redact.Redactor {
	if c.Redactor != nil {
		return c.Redactor
	}
	return redact.New()
}
