package debugaccess

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Who may open a session on a repository (R5.2).
const (
	WhoAdmins = "admins"
	WhoOwner  = "owner"
)

// The spec's limits (R5.4, R5.17 to R5.21).
const (
	DefaultLimitMin = 30
	MinLimitMin     = 5
	MaxLimitMin     = 120
	DefaultIdleMin  = 10
	MinIdleMin      = 1
	MaxIdleMin      = 60
	MaxHoldMin      = 30
	// MaxPerUser is how many sessions one user may have open at once.
	MaxPerUser = 2
	// MaxPerJob is how many sessions one job may have open at once.
	MaxPerJob = 1
	// ExtendBy is the one extension a session may take (R5.19).
	ExtendBy = 15 * time.Minute
	// JobEndNotice is how long a session stays after its job ends (R5.20).
	JobEndNotice = 60 * time.Second
	// ReasonMin and ReasonMax bound the one-line reason (R5.3).
	ReasonMin = 3
	ReasonMax = 200
)

// RepoSettings is a repository's debug setting. The zero value is off.
type RepoSettings struct {
	Enabled      bool
	Who          string
	LimitMin     int
	IdleMin      int
	HoldMin      int
	PersistentOK bool
	UpdatedAt    time.Time
	UpdatedBy    string
}

// DefaultRepoSettings is the setting of a repository nobody switched on (R5.1).
func DefaultRepoSettings() RepoSettings {
	return RepoSettings{Who: WhoAdmins, LimitMin: DefaultLimitMin, IdleMin: DefaultIdleMin}
}

func repoFromStore(r store.DebugRepoSettings, ok bool) RepoSettings {
	if !ok {
		return DefaultRepoSettings()
	}
	return RepoSettings{Enabled: r.Enabled, Who: r.Who, LimitMin: r.LimitMin, IdleMin: r.IdleMin, HoldMin: r.HoldMin,
		PersistentOK: r.PersistentOK, UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}
}

// Validate checks the ranges of the spec; the database holds them too.
func (r RepoSettings) Validate() error {
	switch {
	case r.Who != WhoAdmins && r.Who != WhoOwner:
		return &Refusal{Code: CodeBadSetting, Text: "Who may open must be Admins and owner, or Owner only."}
	case r.LimitMin < MinLimitMin || r.LimitMin > MaxLimitMin:
		return &Refusal{Code: CodeBadSetting, Text: fmt.Sprintf("Session limit must be %d to %d minutes.", MinLimitMin, MaxLimitMin)}
	case r.IdleMin < MinIdleMin || r.IdleMin > MaxIdleMin:
		return &Refusal{Code: CodeBadSetting, Text: fmt.Sprintf("Idle limit must be %d to %d minutes.", MinIdleMin, MaxIdleMin)}
	case r.IdleMin > r.LimitMin:
		return &Refusal{Code: CodeBadSetting, Text: "Idle limit must not be longer than the session limit."}
	case r.HoldMin < 0 || r.HoldMin > MaxHoldMin:
		return &Refusal{Code: CodeBadSetting, Text: fmt.Sprintf("Hold failed jobs must be 0 to %d minutes.", MaxHoldMin)}
	}
	return nil
}

// PoolSettings is the pool-wide setting (owner only): Enabled is the kill
// switch (R5.26), Record keeps transcripts (R5.25). Both are on when unsaved.
type PoolSettings struct {
	Enabled   bool      `json:"enabled"`
	Record    bool      `json:"record"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// DefaultPoolSettings is the pool setting nobody saved.
func DefaultPoolSettings() PoolSettings { return PoolSettings{Enabled: true, Record: true} }

// Job is what the policy needs to know about a job.
type Job struct {
	ID           int64
	RepositoryID int64
	Repository   string
	Name         string
	// Running is true while the job runs on a runner of the pool.
	Running bool
	// Event is the GitHub event that started the run ("push", "pull_request", ...).
	Event       string
	MachineID   string
	MachineName string
	RunnerName  string
	Backend     string
	// Isolation is "container", "VM", "microVM" or "persistent"; "" when crsp
	// does not know it.
	Isolation string
	OS        string
}

// Repo is what the policy needs to know about the job's repository.
type Repo struct {
	// Known is true when the repository is one of the pool's installation (the
	// user can see it in the console).
	Known      bool
	Visibility string
}

// Codes of a refusal; the console shows Text beside the disabled command.
const (
	CodePoolOff      = "pool_off"
	CodeRepoOff      = "repo_off"
	CodeNotSignedIn  = "not_signed_in"
	CodeViewer       = "viewer"
	CodeOwnerOnly    = "owner_only"
	CodeRepoUnknown  = "repo_unknown"
	CodePublic       = "public_repository"
	CodePullRequest  = "pull_request"
	CodeNotRunning   = "not_running"
	CodeUnknownEnv   = "unknown_environment"
	CodePersistent   = "persistent_runner"
	CodeShared       = "shared_container"
	CodeCannotEnter  = "cannot_enter"
	CodeReason       = "reason"
	CodeUserCap      = "user_cap"
	CodeJobCap       = "job_cap"
	CodeBadSetting   = "bad_setting"
	CodeNotFound     = "not_found"
	CodeEnded        = "ended"
	CodeExtended     = "extended"
	CodePastLimit    = "past_limit"
	CodeNotYours     = "not_yours"
	CodeNeedsConfirm = "needs_confirm"
)

// Refusal is why a debug command is not allowed. It is also the error the
// service returns, so the API answers with the same words the console shows.
type Refusal struct {
	Code string
	Text string
}

func (r *Refusal) Error() string { return r.Text }

// Words of the refusals (design system content, Debug access words).
const (
	TextPoolOff     = "Debug sessions are off for this pool."
	TextRepoOff     = "Debug access is off for this repository."
	TextNotSignedIn = "Sign in to open a session."
	TextViewer      = "Only admins can open a session."
	TextOwnerOnly   = "Only the owner can open a session on this repository."
	TextRepoUnknown = "This repository is not in the pool's GitHub App installation."
	TextPublic      = "Not available for public repositories."
	TextPullRequest = "This job runs for a pull request, which may come from a fork."
	TextNotRunning  = "The job is not running."
	TextUnknownEnv  = "crsp does not know this job's environment, so it cannot be entered safely."
	TextPersistent  = "This job runs on a persistent runner. Turn on Allow on persistent runners for this repository first."
	TextCannotEnter = "This machine cannot open debug sessions yet."
	TextReason      = "Write a reason of 3 to 200 characters."
	TextUserCap     = "You have 2 sessions open. End one first."
	// TextPersistentWarn is the warning before a session on a persistent runner (R5.10).
	TextPersistentWarn = "This runner keeps files between jobs."
	// TextMacNotRun is the macOS wording (R5.34).
	TextMacNotRun = "Not run on a real Mac yet."
	// TextWhatItSees is how the console describes the shell (R5.14).
	TextWhatItSees = "Can see this job's files and its running processes' output, not its secrets."
)

func sharedText(other int64) string {
	return fmt.Sprintf("Another job of this repository is running in the same container: job %d.", other)
}

func jobCapText(user string) string {
	return fmt.Sprintf("%s has a session open on this job.", user)
}

// Facts is everything one decision reads.
type Facts struct {
	Pool PoolSettings
	Repo RepoSettings
	// RepoInfo is the job's repository as the installation knows it.
	RepoInfo Repo
	Who      auth.Principal
	Job      Job
	// SharedWith lists other running jobs of the repository in the same
	// container (same machine and runner); empty when none.
	SharedWith []int64
	// Enter is the machine's answer to "can you enter this environment"; nil means yes.
	Enter error
	// UserOpen is how many sessions the user has open.
	UserOpen int
	// JobOpen lists the open sessions of the job.
	JobOpen []store.DebugSession
}

// Decide says whether the user may open a session on the job now, checking in
// the spec's order (section 3, step 2): the pool and repository switches, the
// role and sight of the repository, the public and pull request guard, the
// environment, then the caps. The first failure is the answer; nil allows.
func Decide(f Facts) *Refusal {
	if !f.Pool.Enabled {
		return &Refusal{Code: CodePoolOff, Text: TextPoolOff}
	}
	if !f.Repo.Enabled {
		return &Refusal{Code: CodeRepoOff, Text: TextRepoOff}
	}
	if f.Who.Login == "" || !f.Who.Role.Valid() {
		return &Refusal{Code: CodeNotSignedIn, Text: TextNotSignedIn}
	}
	if !f.Who.Role.AtLeast(auth.RoleAdmin) {
		return &Refusal{Code: CodeViewer, Text: TextViewer}
	}
	if f.Repo.Who == WhoOwner && !f.Who.Role.AtLeast(auth.RoleOwner) {
		return &Refusal{Code: CodeOwnerOnly, Text: TextOwnerOnly}
	}
	if !f.RepoInfo.Known {
		return &Refusal{Code: CodeRepoUnknown, Text: TextRepoUnknown}
	}
	if f.RepoInfo.Visibility != "private" && f.RepoInfo.Visibility != "internal" {
		// Fail closed: an unknown visibility is treated as public.
		return &Refusal{Code: CodePublic, Text: TextPublic}
	}
	if isPullRequestEvent(f.Job.Event) {
		return &Refusal{Code: CodePullRequest, Text: TextPullRequest}
	}
	if !f.Job.Running {
		return &Refusal{Code: CodeNotRunning, Text: TextNotRunning}
	}
	if r := environmentRefusal(f); r != nil {
		return r
	}
	if f.UserOpen >= MaxPerUser {
		return &Refusal{Code: CodeUserCap, Text: TextUserCap}
	}
	if len(f.JobOpen) >= MaxPerJob {
		return &Refusal{Code: CodeJobCap, Text: jobCapText(f.JobOpen[0].UserLogin)}
	}
	return nil
}

func environmentRefusal(f Facts) *Refusal {
	switch f.Job.Isolation {
	case "container":
		if len(f.SharedWith) > 0 {
			return &Refusal{Code: CodeShared, Text: sharedText(f.SharedWith[0])}
		}
	case "VM", "microVM":
	case "persistent":
		if !f.Repo.PersistentOK {
			return &Refusal{Code: CodePersistent, Text: TextPersistent}
		}
	default:
		// No host shell, ever (R5.6): an environment crsp cannot name is refused.
		return &Refusal{Code: CodeUnknownEnv, Text: TextUnknownEnv}
	}
	if f.Job.MachineID == "" || f.Job.RunnerName == "" {
		return &Refusal{Code: CodeUnknownEnv, Text: TextUnknownEnv}
	}
	if f.Enter != nil {
		if r, ok := f.Enter.(*Refusal); ok {
			return r
		}
		return &Refusal{Code: CodeCannotEnter, Text: TextCannotEnter}
	}
	return nil
}

// isPullRequestEvent is true for every event that runs code of a pull request.
// crsp does not record the head repository of a pull request, so a fork cannot
// be told apart and every one is refused (R5.5, fail closed).
func isPullRequestEvent(ev string) bool {
	return strings.HasPrefix(ev, "pull_request")
}

// CheckReason checks the one-line reason (R5.3) and returns it trimmed.
func CheckReason(reason string) (string, *Refusal) {
	r := strings.TrimSpace(reason)
	if strings.ContainsAny(r, "\r\n") {
		return "", &Refusal{Code: CodeReason, Text: TextReason}
	}
	n := utf8.RuneCountInString(r)
	if n < ReasonMin || n > ReasonMax {
		return "", &Refusal{Code: CodeReason, Text: TextReason}
	}
	return r, nil
}

// Warnings are what the Debug card says before a session opens (R5.8, R5.10, R5.34).
func Warnings(j Job) []string {
	var out []string
	if j.Isolation == "persistent" {
		out = append(out, TextPersistentWarn, "On a persistent runner the shell can see more than this job's processes.")
	}
	if strings.EqualFold(j.OS, "macos") || strings.EqualFold(j.OS, "darwin") {
		out = append(out, TextMacNotRun)
	}
	return out
}
