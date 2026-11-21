// Package jobsdata is the data of the Jobs section: runs, attempts, jobs, steps, daily summaries and the
// workflow graph cache (observability R5.7 to R5.12, R6.8; OPS34 owner decisions D1 to D14). This file holds the
// shared types and the two interfaces: Writer (ingest, pruning) and Reader (the API and the forecast). The SQLite
// implementation lives in internal/store (jobsdata*.go); ingest lives in internal/jobsdata/ingest.
package jobsdata

import (
	"context"
	"strings"
	"time"
)

// Retention is how long each kind of row is kept (owner decisions D1, D2).
type Retention struct {
	Steps     time.Duration // 30 days
	Jobs      time.Duration // 90 days, runs and attempts with them
	Summaries time.Duration // 400 days
}

// DefaultRetention is the owner's setting: steps 30 days, jobs 90, daily summaries 400.
var DefaultRetention = Retention{Steps: 30 * 24 * time.Hour, Jobs: 90 * 24 * time.Hour, Summaries: 400 * 24 * time.Hour}

// State is a job or run state as the console shows it.
type State string

// States.
const (
	Queued  State = "queued"
	Running State = "running"
	Done    State = "done"
)

// Result is how a finished job or run ended.
type Result string

// Results. Lost is a job crsp lost (agent gone) and is never used for a forecast.
const (
	Success   Result = "success"
	Failure   Result = "failure"
	Cancelled Result = "cancelled"
	TimedOut  Result = "timed_out"
	Lost      Result = "lost"
)

// RunnerKind is persistent or ephemeral (D9).
type RunnerKind string

// Runner kinds.
const (
	Persistent RunnerKind = "persistent"
	Ephemeral  RunnerKind = "ephemeral"
)

// Run is one attempt of a workflow run. A re-run is a new attempt of the same RunID.
type Run struct {
	RepositoryID int64
	Repository   string // owner/name
	RunID        int64
	Attempt      int
	Workflow     string // workflow file path, ".github/workflows/ci.yml"; "" when unknown
	WorkflowName string
	Event        string
	Branch       string // head_branch, always shown (D6)
	HeadSHA      string
	PullRequest  int    // 0 when none
	TaskID       string // parsed from the branch when it starts with a task id (D6), else ""
	Worktree     string // only when a job reported it (D6), else ""
	HTMLURL      string
	CreatedAt    time.Time
	StartedAt    time.Time
	CompletedAt  time.Time
	Conclusion   Result // "" while open
	// TriggeredBy is the run that started this one when the source names it (a workflow_run payload), else 0.
	// The store infers the link for a workflow_run run when this is 0 (followup.go).
	TriggeredBy int64
}

// Job is one job attempt: one GitHub job id.
type Job struct {
	ID           int64 // GitHub job id
	RepositoryID int64
	Repository   string
	RunID        int64
	Attempt      int
	Workflow     string
	Name         string   // as GitHub names it: "test (ubuntu, 22)"
	JobType      string   // Name without the matrix values: "test" (D10)
	Matrix       []string // the matrix values, "ubuntu", "22"
	JobKey       string   // repository / workflow / name (with matrix values); the unit the forecast learns on
	Branch       string
	HeadSHA      string
	Event        string
	Labels       []string
	State        State
	Result       Result
	HTMLURL      string

	// Times, zero when unknown.
	CreatedAt   time.Time // GitHub created_at
	QueuedAt    time.Time // took a ticket in the slot queue
	SlotAt      time.Time // took a slot
	StartedAt   time.Time
	CompletedAt time.Time
	CleanupMS   int64 // clean-up after the job; 0 when unknown

	// crsp's own facts (D6, D9).
	MachineID   string
	MachineName string
	RunnerID    string
	RunnerName  string
	RunnerKind  RunnerKind // "" when unknown
	Backend     string
	Isolation   string
	Cache       string // "warm", "cold" or ""
	WaitReason  string // the scheduler's wait reason while queued
	WaitText    string
	Worktree    string // reported by the job, else ""
	Light       bool   // light job (true) or heavy (false); see the scheduler classes

	// Forecast stored when the job started, so Charts can compare it with the actual time (REPORT 4, 5).
	Forecast *ForecastRecord
}

// ForecastRecord is a forecast as stored with a job.
type ForecastRecord struct {
	LoMS, MidMS, HiMS int64
	Basis             string
	Samples           int
}

// Step is one step of a job, from the GitHub workflow_job event (D5).
type Step struct {
	JobID       int64
	Number      int
	Name        string
	State       string // queued, running, done, skipped
	Result      string // success, failure, cancelled, skipped, ""
	StartedAt   time.Time
	CompletedAt time.Time
}

// Graph is the needs graph of one workflow at one head commit (D3).
type Graph struct {
	RepositoryID int64
	Workflow     string
	HeadSHA      string
	// Needs maps a job id in the workflow file to the job ids it needs.
	Needs map[string][]string
	// Dynamic is true when a matrix or a reusable workflow makes the jobs expand at run time.
	Dynamic map[string]bool
	// OK is false when the file could not be read or parsed; the screens then fall back to timing overlap.
	OK        bool
	FetchedAt time.Time
}

// Facts are crsp's own facts about a job, written as they become known; a zero field is left unchanged.
type Facts struct {
	MachineID, MachineName, RunnerID, RunnerName string
	RunnerKind                                   RunnerKind
	Backend, Isolation, Cache                    string
	WaitReason, WaitText                         string
	QueuedAt, SlotAt                             time.Time
	CleanupMS                                    int64
	Worktree                                     string
	Light                                        *bool
	Lost                                         bool // the job was lost: result becomes Lost
}

// Sample is one finished successful job duration used by the forecast.
type Sample struct {
	JobID     int64
	JobKey    string
	Workflow  string
	At        time.Time // completed_at
	Seconds   float64
	MachineID string
	StepName  string // set for a step sample
}

// SampleQuery selects samples, newest first, never older than Since, at most Limit.
type SampleQuery struct {
	RepositoryID int64
	JobKey       string // exact job key, or
	Workflow     string // all jobs of the workflow with JobType when set, or
	JobType      string
	StepName     string // a step of the job key (Steps are kept 30 days only)
	Since        time.Time
	Limit        int
}

// RunFilter selects runs for listRuns.
type RunFilter struct {
	State        State
	Result       Result
	RepositoryID int64
	Branch       string
	From, To     time.Time // on created_at
	// After is the sort key of the last run of the previous page (see RunPage.NextAfter).
	After int64
	Limit int
}

// RunRow is a run in a list: its counts and total time.
type RunRow struct {
	Run
	State      State
	JobsTotal  int
	JobsDone   int
	JobsFailed int
	TotalMS    int64 // wall time from created to completed (or to now while open, computed by the caller)
	SortKey    int64 // paging key
	// FollowUps is how many runs this run started next, FollowUpsOpen how many of them are queued or running.
	FollowUps     int
	FollowUpsOpen int
	// StartedNext lists the runs this run started, oldest first.
	StartedNext []FollowUp
	// ParentRunID is the run this one was started by, 0 when it is none.
	ParentRunID int64
}

// RunDetail is a run with its jobs.
type RunDetail struct {
	RunRow
	Attempts []int
	Jobs     []Job
	Graph    Graph // OK false means no graph: draw by timing overlap
}

// JobFilter selects jobs.
type JobFilter struct {
	State        State
	Result       Result
	RepositoryID int64
	MachineID    string
	RunnerName   string // D9: by runner is the runner name
	RunnerKind   RunnerKind
	Backend      string
	JobType      string // D10
	JobKey       string
	Branch       string
	RunID        int64
	From, To     time.Time // on created_at
	After        int64     // job id of the last job of the previous page
	Limit        int
}

// JobDetail is a job with steps, attempts and history.
type JobDetail struct {
	Job
	Steps    []Step
	Attempts []JobAttempt
	Needs    []string // job names this job needs (from the graph)
}

// JobAttempt is one attempt of the same job in a run.
type JobAttempt struct {
	Attempt int
	JobID   int64
	State   State
	Result  Result
}

// HistoryPoint is one earlier run of a job key.
type HistoryPoint struct {
	At      time.Time
	Seconds float64
	Result  Result
	Branch  string
	Machine string
}

// GroupBy is a Jobs sub-section (D9, D10).
type GroupBy string

// Groupings.
const (
	ByRepository GroupBy = "repository"
	ByMachine    GroupBy = "machine"
	ByRunner     GroupBy = "runner"
	ByRunnerType GroupBy = "runner-type"
	ByJobType    GroupBy = "job-type"
)

// GroupRow is one row of a grouped table.
type GroupRow struct {
	Name             string
	Filter           string // the query that lists this group's jobs, "repository=owner/name"
	Running, Queued  int
	Done, Failed     int
	WaitP50, WaitP90 float64 // seconds
	RunP50, RunP90   float64
	Trend            []float64 // last run times, oldest first, at most 20
	Light            *bool     // job type only
}

// Day is one day of the charts.
type Day struct {
	Day              string // 2026-11-18, UTC
	Jobs             int
	Failed           int
	WaitP50, WaitP90 float64
	RunP50, RunP90   float64
}

// HourCount counts jobs by weekday (0 Sunday) and hour (UTC).
type HourCount struct{ Weekday, Hour, Count int }

// ForecastPoint is a forecast with the actual time, for the honesty chart.
type ForecastPoint struct {
	JobID                        int64
	ForecastS, ActualS, LoS, HiS float64
	Basis                        string
}

// Stats is the chart data of the last Days days.
type Stats struct {
	Days           []Day
	Hours          []HourCount
	ForecastPoints []ForecastPoint
}

// DailySummary is one row kept 400 days: one job type of one repository on one day (D2, D12).
type DailySummary struct {
	Day        string
	Repository string
	JobType    string
	Jobs       int
	Failed     int
	WaitP50MS  int64
	WaitP90MS  int64
	RunP50MS   int64
	RunP90MS   int64
}

// Writer is what ingest and pruning use.
type Writer interface {
	// UpsertRun inserts or updates an attempt; a later write never moves a finished run back to open.
	UpsertRun(ctx context.Context, r Run) error
	// UpsertJob inserts or updates a job; the state never moves backward (queued, running, done) and crsp
	// facts already stored are kept when the update carries none.
	UpsertJob(ctx context.Context, j Job) error
	// ReplaceSteps stores the steps of a job (D5).
	ReplaceSteps(ctx context.Context, jobID int64, steps []Step) error
	// SetFacts merges crsp's own facts into a job by GitHub job id. A job not seen yet is parked and merged on its upsert.
	SetFacts(ctx context.Context, jobID int64, f Facts) error
	// SetForecast stores the forecast made when the job started.
	SetForecast(ctx context.Context, jobID int64, f ForecastRecord) error
	// GetGraph and PutGraph are the workflow graph cache (D3).
	GetGraph(ctx context.Context, repositoryID int64, workflow, headSHA string) (Graph, bool, error)
	PutGraph(ctx context.Context, g Graph) error
	// Prune rolls the day's summaries first, then deletes steps, jobs (with runs and attempts left empty) and summaries past Retention.
	Prune(ctx context.Context, now time.Time, r Retention) (Pruned, error)
}

// Pruned says what a prune removed.
type Pruned struct{ Steps, Jobs, Runs, Summaries, Graphs int64 }

// Reader is what the API and the forecast use. Lists are newest first.
type Reader interface {
	// ListRuns returns up to Limit+1 runs; the caller trims the extra one to find the next cursor. total is the
	// count of the filter without paging.
	ListRuns(ctx context.Context, f RunFilter) (rows []RunRow, total int, err error)
	GetRun(ctx context.Context, runID int64, attempt int) (RunDetail, bool, error)
	ListJobs(ctx context.Context, f JobFilter) (rows []Job, total int, err error) // up to Limit+1 rows
	GetJob(ctx context.Context, id int64) (JobDetail, bool, error)
	// History is the last n runs of a job key, oldest first.
	History(ctx context.Context, repositoryID int64, jobKey string, n int) ([]HistoryPoint, error)
	Samples(ctx context.Context, q SampleQuery) ([]Sample, error)
	// OpenJobs lists every queued and running job, for the queue simulation.
	OpenJobs(ctx context.Context) ([]Job, error)
	Groups(ctx context.Context, by GroupBy) ([]GroupRow, error)
	Stats(ctx context.Context, days int, f JobFilter) (Stats, error)
	Summaries(ctx context.Context, from, to time.Time) ([]DailySummary, error)
}

// Store is both halves.
type Store interface {
	Writer
	Reader
}

// SplitName gives the job type (the name without matrix values) and the matrix values of a GitHub job name:
// "test (ubuntu, 22)" is "test" with ["ubuntu", "22"]; "build" is "build" with none.
func SplitName(name string) (jobType string, matrix []string) {
	name = strings.TrimSpace(name)
	i := strings.LastIndex(name, " (")
	if i <= 0 || !strings.HasSuffix(name, ")") {
		return name, nil
	}
	for _, v := range strings.Split(name[i+2:len(name)-1], ",") {
		if v = strings.TrimSpace(v); v != "" {
			matrix = append(matrix, v)
		}
	}
	return name[:i], matrix
}

// MakeKey is the job key: repository / workflow / job name (the name already holds the matrix values).
func MakeKey(repository, workflow, name string) string {
	return repository + " / " + workflow + " / " + name
}

// TaskFromBranch gives the task id when the branch starts with one, "OPS34-jobs-section" gives "OPS34" (D6).
func TaskFromBranch(branch string) string {
	i := strings.IndexByte(branch, '-')
	if i < 2 || i > 12 {
		return ""
	}
	head, digitsFrom := branch[:i], -1
	for k := 0; k < len(head); k++ {
		c := head[k]
		switch {
		case c >= 'A' && c <= 'Z':
			if digitsFrom >= 0 {
				return ""
			}
		case c >= '0' && c <= '9':
			if digitsFrom < 0 {
				digitsFrom = k
			}
		default:
			return ""
		}
	}
	if digitsFrom <= 0 {
		return ""
	}
	return head
}
