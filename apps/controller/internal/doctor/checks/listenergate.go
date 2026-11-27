package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// listenerGate tells the owner when the listener gate of persistent runners only
// reports (runners R5.134). Observe mode is the default, so this is information,
// never a warning: the audit log shows when crsp would have started and stopped
// a listener, and no listener is touched until the owner switches enforcement on.
// New check.
func listenerGate(ctx context.Context, env doctor.Env) []doctor.Finding {
	if env.ListenerGate == nil {
		return []doctor.Finding{skip("listener.gate", controllerSubject, "The listener gate is not running", "Nothing was checked.")}
	}
	on, err := env.ListenerGate.GateEnforced(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("listener.gate", controllerSubject, "The listener gate setting", err)}
	}
	f := doctor.Finding{Check: "listener.gate", Subject: controllerSubject, Severity: doctor.OK,
		Title: "Persistent runners on the machine itself are gated: offline by design until a job needs them"}
	if !on {
		f.Severity = doctor.Info
		f.Title = "The listener gate is in observe mode: persistent runners keep their listener on"
		f.Detail = "The audit log shows when crsp would have started and stopped each listener. No listener is touched until you switch enforcement on."
		f.Fix = doctor.Fix{Text: "When the reports look right, switch on Gate persistent runners in Settings.", Link: "/settings"}
	}
	return []doctor.Finding{f}
}
