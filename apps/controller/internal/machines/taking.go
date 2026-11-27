package machines

import "time"

// Why a machine takes no new job, as the controller computes it (R5.62). One
// answer: the console state, the Now line, its note and the Stop or Resume
// button all read it.
const (
	NotTakingOffline    = "offline"
	NotTakingStopped    = "stopped"     // Stop new jobs from the console (drain)
	NotTakingModePaused = "mode_paused" // the mode is Paused
	NotTakingTrayPaused = "tray_paused" // a pause from the tray
	NotTakingOnBattery  = "on_battery"  // Automatic, battery pause on, on battery
	NotTakingBusy       = "busy"        // the load is over the budget
	NotTakingBaseImage  = "base_image_missing"
	NotTakingDiskLow    = "disk_low"
)

// Availability is whether a machine takes new jobs and, when it does not, why.
type Availability struct {
	Taking bool
	Reason string // empty while taking
}

// ComputeAvailability decides from what the controller itself holds (the
// machine's online flag, its drain state, its mode, the tray pause and the
// battery switch) and only then from what the agent alone can know (the load
// against the budget, a missing base image, a low disk). The
// agent's own "paused" and "on_battery" are not read: they lag behind a
// setting the controller has just written, and they are what made the console
// say Paused while the machine was online and Taking jobs after Stop (OPS29).
func ComputeAvailability(online bool, state string, set Settings, p Policy, now time.Time) Availability {
	no := func(reason string) Availability { return Availability{Reason: reason} }
	switch {
	case !online:
		return no(NotTakingOffline)
	case state == StateDraining || state == StateDrained:
		return no(NotTakingStopped)
	case set.EffectiveMode() == ModePaused:
		return no(NotTakingModePaused)
	case set.UserPaused(now):
		return no(NotTakingTrayPaused)
	case set.EffectiveMode() == ModeAutomatic && set.PauseOnBattery && p.BatteryKnown && p.OnBattery:
		return no(NotTakingOnBattery)
	}
	if !p.ReportedAt.IsZero() && !p.Accepting {
		switch p.PauseReason {
		case PauseReasonBudget:
			return no(NotTakingBusy)
		case PauseReasonBaseImage, PauseReasonDiskLow:
			return no(p.PauseReason)
		}
	}
	return Availability{Taking: true}
}

// legacyPauseReason maps a computed reason to the exact names other features
// read (R5.62).
func legacyPauseReason(reason string) string {
	switch reason {
	case NotTakingStopped, NotTakingModePaused, NotTakingTrayPaused:
		return PauseReasonPaused
	case NotTakingBusy:
		return PauseReasonBudget
	case NotTakingOffline:
		return ""
	}
	return reason
}
