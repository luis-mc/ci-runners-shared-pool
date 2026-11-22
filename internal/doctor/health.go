package doctor

import (
	"context"
	"time"
)

// The machine health the checks of machines R5.70 to R5.77 read (OPS83). The
// controller fills it from the agents' HealthReports; a check never reads an
// agent itself.

// DiskHealth is one volume of a machine.
type DiskHealth struct {
	Role, Path string
	// Known is false when the OS could not give the free space.
	Known         bool
	FreePct       float64
	FreeBytes     int64
	DaysUntilFull *float64
}

// MachineHealth is what the health checks read of one machine.
type MachineHealth struct {
	MachineID string
	// Reported is false until the first report arrives.
	Reported   bool
	ReceivedAt time.Time

	Disks []DiskHealth

	RestartPending    bool
	RestartMayHitJobs bool
	RestartReasons    []string

	SleepKnown, SleepEnabled bool
	SleepModes               []string

	BatteryKnown, OnBattery bool

	SkewKnown   bool
	SkewSeconds float64

	AgentVersion, ControllerVersion string
	AgentOutOfDate                  bool
	RunnerVersion, LatestRunner     string
	RunnerOutOfDate                 bool

	OOMRecent bool
	OOMLastAt time.Time

	ThrottleKnown, Throttled bool
	ThrottleDetail           string
}

// HealthThresholds are the editable limits of the health checks (doctor R5.80).
type HealthThresholds struct {
	DiskDaysUnder      int
	DiskFreePctUnder   int
	NoHeartbeatSeconds int
	ClockSkewSeconds   int
	OOMWindowMinutes   int
}

// HealthFleet is the optional part of Fleet that gives the machine health. A
// Fleet that does not have it makes the health checks skip.
type HealthFleet interface {
	MachineHealths(ctx context.Context) ([]MachineHealth, error)
	HealthThresholds(ctx context.Context) (HealthThresholds, error)
}
