package machines

import (
	"context"
	"time"
)

// Repository is the controller's data for this area. Calls are atomic; the
// SQLite adapter maps them onto the tables of migration 00004 and the audit
// log.
type Repository interface {
	// InsertMachine adds a machine with its settings and first key hash in one step.
	InsertMachine(ctx context.Context, m Machine, s Settings, keyHash string) error
	GetMachine(ctx context.Context, id string) (Machine, error)
	// ListMachines lists machines that are not removed, controller's own first.
	ListMachines(ctx context.Context) ([]Machine, error)
	UpdateMachine(ctx context.Context, m Machine) error
	// MachineByKey finds the machine of a key hash that is not revoked and
	// whose machine is not removed; ErrUnauthenticated otherwise.
	MachineByKey(ctx context.Context, keyHash string) (Machine, error)
	// RevokeKeys revokes every key of a machine and marks it removed.
	RevokeKeys(ctx context.Context, machineID string) error
	// AddKey gives an existing machine one more live key: a rejoin (R5.70).
	AddKey(ctx context.Context, machineID, keyHash string) error
	MarkRemoved(ctx context.Context, machineID string, at time.Time) error

	GetSettings(ctx context.Context, machineID string) (Settings, error)
	PutSettings(ctx context.Context, machineID string, s Settings) error

	InsertJoinCode(ctx context.Context, c JoinCode) error
	GetJoinCode(ctx context.Context, id string) (JoinCode, error)
	FindJoinCodeByHash(ctx context.Context, hash string) (JoinCode, error)
	// RevokeOpenJoinCodes revokes every unused code of kind CodeKindCode (R5.4).
	RevokeOpenJoinCodes(ctx context.Context, at time.Time) error
	// RevokeJoinCode withdraws an unused code; false when it was used or gone.
	RevokeJoinCode(ctx context.Context, id string, at time.Time) (bool, error)
	// UseJoinCode marks the code used by a machine, only when it is unused,
	// not revoked and not expired at now; false otherwise (single use).
	UseJoinCode(ctx context.Context, hash, machineID string, now time.Time) (bool, error)

	InsertLoad(ctx context.Context, machineID string, p LoadPoint) error
	ListLoad(ctx context.Context, machineID string, from, to time.Time) ([]LoadPoint, error)
	DeleteLoadBefore(ctx context.Context, t time.Time) error

	InsertChange(ctx context.Context, c Change) error
	GetChange(ctx context.Context, machineID, id string) (Change, error)
	UpdateChange(ctx context.Context, c Change) error
	// OpenChange is the newest change in waiting, running or needs_you; ok false when none.
	OpenChange(ctx context.Context, machineID string) (Change, bool, error)
	// OpenChanges lists every open change, for the drain timeout tick.
	OpenChanges(ctx context.Context) ([]Change, error)

	// Audit appends who, what and when (R6.4). Detail never holds a secret.
	Audit(ctx context.Context, actor, action, subject, detail string) error
}
