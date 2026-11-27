package slots

import (
	"context"
	"time"
)

// JobState is the state of a job as the console shows it.
type JobState string

// Job states.
const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobDone    JobState = "done"
)

// JobRecord is one job attempt as GitHub reported it, with the wait the
// scheduler saw (scheduling R5.52, R5.53; observability R5.7).
type JobRecord struct {
	ID           int64 // GitHub job id
	Repository   string
	RepositoryID int64
	RunID        int64
	RunAttempt   int
	Workflow     string
	JobName      string
	HeadSHA      string
	Trigger      string
	Labels       []string
	State        JobState
	Conclusion   string
	WaitReason   string
	WaitText     string
	MachineID    string
	RunnerName   string
	// Backend and Isolation are the runner's, empty while no runner is known.
	Backend     string
	Isolation   string
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	// QueuedAt and SlotAt come from the slot log: when the job took its ticket and its slot.
	QueuedAt time.Time
	SlotAt   time.Time
}

// JobFilter selects jobs for listJobs.
type JobFilter struct {
	State        JobState
	RepositoryID int64
	MachineID    string
	Backend      string
	Result       string
	From, To     time.Time
	// After is the id of the last job of the previous page (newest first, so smaller ids follow).
	After int64
	Limit int
}

// Jobs reads jobs for the API.
type Jobs interface {
	// ListJobs returns up to Limit+1 jobs, newest first; the caller trims the extra one to find the next cursor.
	ListJobs(ctx context.Context, f JobFilter) ([]JobRecord, error)
	GetJob(ctx context.Context, id int64) (JobRecord, bool, error)
	// MachineExists says whether the machine id is known.
	MachineExists(ctx context.Context, id string) (bool, error)
}
