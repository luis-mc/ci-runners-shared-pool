// Package assign decides where a job runs, before GitHub assigns it (runners
// R5.110 to R5.116, github-integration R5.109 to R5.112, OPS80).
//
// crsp makes a runner for a job only when the machine has room, so a job waits
// queued on GitHub, where no job clock runs. One central controller keeps one
// lease per GitHub job id: a repeated webhook or a restart never makes a second
// runner. The controller reads the workflow_job webhooks and, as a safety net,
// a poller that lists the queued jobs of every repository. It reaches GitHub,
// the machines, the runner service and the store through small interfaces, so
// every rule is tested on fakes with an injected clock.
package assign

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

// State is where a lease is.
type State string

// The lease states.
const (
	// Waiting: the job is queued on GitHub and crsp made no runner yet.
	Waiting State = "waiting"
	// Starting: a runner was asked for; the job has not started on it.
	Starting State = "starting"
	// Running: the in_progress webhook named the job and the runner.
	Running State = "running"
	// Done: the job completed and its room is free.
	Done State = "done"
	// Gone: the job was cancelled, re-run or taken elsewhere before it ran here.
	Gone State = "gone"
)

// Open is true for a lease that still holds or waits for room.
func (s State) Open() bool { return s == Waiting || s == Starting || s == Running }

// Reason codes of a waiting lease (the API enum).
const (
	CodeNone      = ""
	CodePool      = "pool"
	CodeLoad      = "load"
	CodeReserved  = "reserved"
	CodeNever     = "never"
	CodeNoMachine = "no_machine"
	CodeBackoff   = "start_failed"
	CodeStarting  = "starting"
)

// Lease is the one lease of a GitHub job id.
type Lease struct {
	JobID      int64
	RunID      int64
	RunAttempt int
	Repository string // owner/name
	JobName    string
	// Labels are the resolved runs-on labels of the job.
	Labels []string
	// JobLabel is the job's routing label (T2), "" when the job has none.
	JobLabel string
	OS       string
	Arch     string
	State    State

	MachineID     string
	RunnerName    string
	RunnerRecord  string // the runners record id; "" until the runner is made
	GitHubRunner  int64
	Req           admit.Req
	Class         string
	QueuedAt      time.Time
	StartedAt     time.Time // when the runner was asked for
	Passes        int
	ReservedSince time.Time
	Reason        string
	ReasonCode    string
	Fails         int
	NextTry       time.Time
	// Alerted is true once the 12 h alert was sent.
	Alerted   bool
	UpdatedAt time.Time
}

// LabelKey names the label set of the lease: the same job label and OS is one
// set, whatever the repository, so concurrent runs of one job name share it.
func (l Lease) LabelKey() string { return KeyOf(l.Repository, l.OS, l.Labels) }

// KeyOf is the key of a busy label set.
func KeyOf(repository, os string, labels []string) string {
	ls := make([]string, 0, len(labels))
	for _, x := range labels {
		ls = append(ls, strings.ToLower(x))
	}
	sort.Strings(ls)
	return strings.ToLower(repository) + "|" + strings.ToLower(os) + "|" + strings.Join(ls, ",")
}

// Warm is the one ready runner of a busy label set (runners R5.113).
type Warm struct {
	ID           string
	MachineID    string
	LabelKey     string
	Repository   string
	OS           string
	Labels       []string
	RunnerName   string
	RunnerRecord string
	GitHubRunner int64
	Req          admit.Req
	StartedAt    time.Time
}

// Set is a label set that had a job: it is busy while a job of it was seen in
// the last hour.
type Set struct {
	Key        string
	Repository string
	OS         string
	Arch       string
	Labels     []string
	Req        admit.Req
	LastBusy   time.Time
}

// Store keeps the leases, the warm runners and the busy sets. Every method is
// safe for concurrent use; Claim and Begin are the compare-and-set steps that
// close the duplicate race.
type Store interface {
	// Claim inserts l when no lease of its job id exists. It returns the lease
	// that is stored and whether it was just made.
	Claim(ctx context.Context, l Lease) (Lease, bool, error)
	Get(ctx context.Context, jobID int64) (Lease, bool, error)
	// Save replaces the lease of l.JobID.
	Save(ctx context.Context, l Lease) error
	// Begin moves a waiting lease to starting and sets its machine, runner name
	// and start time. It returns false when the lease is not waiting any more:
	// another caller began it.
	Begin(ctx context.Context, jobID int64, machineID, runnerName string, now time.Time) (bool, error)
	// List returns the leases in the given states (all when none), oldest queued first.
	List(ctx context.Context, states ...State) ([]Lease, error)
	// ByRunner finds the open lease that holds a runner name.
	ByRunner(ctx context.Context, runnerName string) (Lease, bool, error)

	ListWarm(ctx context.Context) ([]Warm, error)
	PutWarm(ctx context.Context, w Warm) error
	DeleteWarm(ctx context.Context, id string) error

	TouchSet(ctx context.Context, s Set) error
	ListSets(ctx context.Context) ([]Set, error)
}

// Machine is a machine as the controller sees it.
type Machine struct {
	ID, Name string
	OS, Arch string
	// Pool is the CPUs and GiB its pool holds.
	Pool admit.Pool
	// Ceiling is the largest class a runner there can be (zero: not known).
	Ceiling      admit.Req
	CeilingKnown bool
	// Labels are the generic labels its runners carry; a routing label is
	// always carried by a runner of the job it names.
	Labels []string
	// LoadOK is true when the load is measured and under budget; LoadText says why not.
	LoadOK   bool
	LoadText string
	// Online is true while its agent is connected.
	Online bool
}

// Serves reports whether a runner of this machine can carry all the labels of
// a job: each generic label is one the machine has, and the OS matches.
func (m Machine) Serves(os string, labels []string) bool {
	if !m.Online || (os != "" && !strings.EqualFold(m.OS, os)) {
		return false
	}
	have := map[string]bool{}
	for _, l := range m.Labels {
		have[strings.ToLower(l)] = true
	}
	have[strings.ToLower(m.OS)] = true
	have[strings.ToLower(m.Arch)] = true
	have["self-hosted"] = true
	have[runners.EphemeralLabel] = true
	for _, l := range labels {
		k := strings.ToLower(strings.TrimSpace(l))
		if k == "" || runners.IsRoutingLabel(k) {
			continue
		}
		if !have[k] {
			return false
		}
	}
	return true
}

// Repo is what the controller needs to know about a repository with the pool on.
type Repo struct {
	Scope          runners.Scope
	InstallationID int64
	ExtraPackages  []string
}

// Env is the controller's view of the rest of crsp.
type Env interface {
	// Machines lists the machines and what they hold now.
	Machines(ctx context.Context) ([]Machine, error)
	// Repo returns the repository named owner/name when its pool is on.
	Repo(ctx context.Context, fullName string) (Repo, bool, error)
	// Requirement is what a job needs of the pool and its class name.
	Requirement(repository, jobName string) (admit.Req, string)
}

// Runners is the part of the runner service the controller uses.
type Runners interface {
	StartJobRunner(ctx context.Context, j runners.JobRunner, actor string) (runners.Record, error)
	FinishJobRunner(ctx context.Context, runnerID, actor string) error
	// RemoveIdleRunner deletes a runner nobody used, through GitHub and the
	// agent; it returns runners.ErrRunnerBusy when the runner has a job.
	RemoveIdleRunner(ctx context.Context, runnerID, actor string) error
}

// Notifier tells the owner: a job that waits 12 h, a job no machine can serve.
type Notifier interface {
	Notify(ctx context.Context, l Lease, text string)
}
