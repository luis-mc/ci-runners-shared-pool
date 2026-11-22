package slots

import (
	"context"
	"errors"
	"time"
)

// ErrSlotTaken is returned by Tx.InsertSlot when the slot key is already held:
// a second take of the same slot fails (R5.14).
var ErrSlotTaken = errors.New("slot already taken")

// Job identifies one job attempt (R5.28).
type Job struct {
	Repository  string
	RunID       int64
	RunAttempt  int
	Job         string
	GitHubJobID int64
	Shard       string
	RunnerName  string
}

// SameJob matches on repository, run id, run attempt (1 when missing) and job key (R5.28).
func (j Job) SameJob(o Job) bool {
	return j.Repository == o.Repository && j.RunID == o.RunID && j.Job == o.Job &&
		attempt(j.RunAttempt) == attempt(o.RunAttempt)
}

func attempt(a int) int {
	if a < 1 {
		return 1
	}
	return a
}

// Holder names the processes that hold a ticket or slot (R5.31).
type Holder struct {
	Machine string
	PID     int64
	GatePID int64
	Started time.Time
}

// Ticket is one waiting job on a machine (R5.6).
type Ticket struct {
	ID           string
	MachineID    string
	Job          Job
	RepositoryID int64
	Kind         Kind // ci or heavy
	Priority     int
	EventClass   int
	QueuedAtNs   int64
	Random       int64
	BeatAt       time.Time
	ExpiresAt    time.Time
	Holder       Holder
	WaitReason   string
}

// QueuedAt is the first queued time.
func (t Ticket) QueuedAt() time.Time { return time.Unix(0, t.QueuedAtNs).UTC() }

// Slot is one held slot.
type Slot struct {
	MachineID    string
	Kind         Kind
	Number       int
	Claim        string
	Job          Job
	RepositoryID int64
	Holder       Holder
	QueuedAt     time.Time
	TakenAt      time.Time
	// ExpiresAt is the age limit: taken at plus the slot maximum age (R5.33).
	ExpiresAt time.Time
	// HolderID names the lease holder: the gate id that took the slot (R5.70).
	HolderID string
	// LeaseExpiresAt is when the lease ends unless a heartbeat renews it (R5.70).
	LeaseExpiresAt time.Time
}

// Slot-log events and outcomes (R5.38).
const (
	EventTaken      = "taken"
	EventFreed      = "freed"
	EventStaleFreed = "stale-freed"
	EventDropped    = "dropped"
	EventTimeout    = "timeout"

	OutcomeRan         = "ran"
	OutcomeStaleFreed  = "stale-freed"
	OutcomeNoCISlot    = "no-ci-slot"
	OutcomeError       = "error"
	OutcomeTicketDrop  = "ticket-dropped"
	OutcomeCompletedGH = "completed-on-github"
)

// LogRow is one slot-log row (R5.38).
type LogRow struct {
	MachineID  string
	At         time.Time
	Event      string
	Kind       Kind
	Number     int // 0 for a ticket
	Job        Job
	QueuedAt   time.Time
	SlotAt     time.Time
	ReleasedAt time.Time
	Reason     string // the outcome or the stale reason
}

// LogKeep is how many slot-log rows a machine keeps (R5.39).
const LogKeep = 5000

// Policy is a repository's scheduling attributes.
type Policy struct {
	Priority        int
	MaxParallel     int // 0 means no limit
	BudgetMemoryPct int // 0 means not set
	BudgetCPUPct    int
}

// Tx is the set of operations one scheduler transaction may use. Every method
// takes the transaction's own context.
type Tx interface {
	Settings(machineID string) (Settings, error)
	// Policy returns the repository's policy; ok is false when it has none.
	Policy(repository string) (p Policy, ok bool, err error)

	Tickets(machineID string) ([]Ticket, error)
	AllTickets() ([]Ticket, error)
	Ticket(id string) (t Ticket, ok bool, err error)
	InsertTicket(t Ticket) error
	BeatTicket(id string, beatAt, expiresAt time.Time) (bool, error)
	SetTicketWait(id, reason string) error
	DeleteTicket(id string) (bool, error)

	Slots(machineID string) ([]Slot, error)
	AllSlots() ([]Slot, error)
	InsertSlot(s Slot) error
	// FreeSlot deletes the slot; with a non-empty claim only when it still holds that claim (R5.15).
	FreeSlot(machineID string, kind Kind, number int, claim string) (bool, error)
	// RenewSlot moves the lease end of the slot that still holds this holder id
	// and claim; false when the slot is gone or taken again by another (R5.70).
	RenewSlot(machineID string, kind Kind, number int, holderID, claim string, leaseExpiresAt time.Time) (bool, error)
	// RunningByRepository counts ci and heavy slots held pool-wide per repository (R5.9, R5.10).
	RunningByRepository() (map[string]int, error)

	// AppendLog writes a slot-log row and keeps the newest LogKeep rows of the
	// machine. A failure here is returned; the caller ignores it (R5.39).
	AppendLog(r LogRow) error
	// Enqueue adds a follow-up row to the background job_queue in this
	// transaction (R5.54).
	Enqueue(kind, payload string) error

	// Reservation returns the reserved job of the machine; ok is false when none
	// (R5.93). SetReservation replaces it, ClearReservation removes it.
	Reservation(machineID string) (r Reservation, ok bool, err error)
	SetReservation(r Reservation) error
	ClearReservation(machineID string) error
	// Passes returns the passes counted for the tickets of the machine, by
	// ticket id. AddPass counts one more; DropPasses forgets a ticket's.
	Passes(machineID string) (map[string]int, error)
	AddPass(machineID, ticketID string) error
	DropPasses(ticketID string) error
}

// Reservation is the one reserved job of a machine (scheduling R5.93): a
// waiting job that was passed too often or waited too long. Expected is the
// expected start; zero when no forecast gives it.
type Reservation struct {
	MachineID string
	TicketID  string
	CPUs      int
	MemoryGiB int
	Class     string
	Since     time.Time
	Expected  time.Time
}

// Store runs scheduler work in transactions.
type Store interface {
	// Atomically runs fn in one transaction: all or nothing (R5.14).
	Atomically(ctx context.Context, fn func(Tx) error) error
}
