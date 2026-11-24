// Package github is the one adapter over the GitHub API (decision 0002;
// STACKSPEC 3.3; github-integration R5.2 to R5.38, R6.2, R6.6 to R6.9).
//
// Every other package depends on the interfaces here, never on go-github or
// the scale-set client (depguard github-confinement). Tests use the fake
// server in internal/github/githubfake (R6.9), never github.com.
//
// Rules every implementation keeps: every REST call sends one pinned
// X-GitHub-Api-Version (R6.8) and has a 10-second deadline except the
// scale-set long poll; writes retry at most 3 times with backoff and jitter
// (R6.7); background work uses at most half of an installation's hourly
// limit (R6.6). App JWTs live at most 10 minutes and installation tokens are
// cached in memory only (R6.2). Registration and remove tokens are returned
// to the caller and never stored (R5.31, R5.32).
package github

import (
	"context"
	"errors"
	"time"
)

// Errors callers act on.
var (
	// ErrUnavailable: GitHub did not answer, or answered 5xx. Callers fail
	// closed for changes and show the last known state for reads (section 8).
	ErrUnavailable = errors.New("github unavailable")
	// ErrRateLimited: a primary or secondary limit; wait until ResetAt.
	ErrRateLimited = errors.New("github rate limited")
	// ErrNotFound: the App, installation, repository or runner is gone.
	ErrNotFound = errors.New("github: not found")
	// ErrRunnerBusy: GitHub answered 422 to the delete of a runner, which means
	// the runner has a job. Callers leave the runner running (R5.33, R5.62).
	ErrRunnerBusy = errors.New("github: the runner has a job")
	// ErrForbidden: a permission is missing (R5.24).
	ErrForbidden = errors.New("github: forbidden")
	// ErrBadKey: a candidate App private key is not a PEM RSA key (R5.7).
	ErrBadKey = errors.New("github: not a usable private key")
	// ErrKeyRefused: GitHub does not accept the key for this App (R5.7).
	ErrKeyRefused = errors.New("github: the key is not for this App")
)

// APIError is an error answer of GitHub with its real status and message, so a
// page can show what GitHub said (UX-28). It unwraps to the package error of
// its status (ErrNotFound, ErrForbidden, ErrUnavailable) when there is one.
type APIError struct {
	Status  int
	Message string
	Err     error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }

// RateLimitError carries when a limit resets.
type RateLimitError struct {
	ResetAt time.Time
	// Secondary is set for GitHub's secondary (abuse) limit, which a short
	// wait can clear; a primary limit waits for ResetAt.
	Secondary bool
}

func (e *RateLimitError) Error() string {
	return "github rate limited until " + e.ResetAt.UTC().Format(time.RFC3339)
}

// Unwrap lets errors.Is match ErrRateLimited.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// App is the GitHub App as GitHub describes it (no secrets).
type App struct {
	ID          int64
	Slug        string
	ClientID    string
	OwnerLogin  string
	OwnerType   string // User or Organization
	HTMLURL     string
	Permissions map[string]string
	// Events are the webhook events the App subscribes to (GET /app).
	Events []string
}

// AppCredentials is what the manifest conversion returns once (R5.3). The
// caller puts PrivateKeyPEM, ClientSecret and WebhookSecret in the OS secret
// store at once and drops them.
type AppCredentials struct {
	App           App
	PrivateKeyPEM []byte
	ClientSecret  string
	WebhookSecret string
}

// User is a GitHub user, identified by the numeric id (R5.9).
type User struct {
	ID    int64
	Login string
}

// UserToken is a GitHub user access token from the OAuth or device flow. It is
// held in memory only for the call that needs it.
type UserToken struct {
	Value     string
	ExpiresAt time.Time
}

// Installation is one installation of the App (R5.17).
type Installation struct {
	ID                  int64
	AccountLogin        string
	AccountType         string // User or Organization
	RepositorySelection string // all or selected
	Permissions         map[string]string
	Suspended           bool
}

// Repository is one repository of an installation (R5.18).
type Repository struct {
	ID            int64
	FullName      string
	Owner         string
	Name          string
	Visibility    string // public, private or internal
	DefaultBranch string
	Archived      bool
	// Admin is the user's GitHub admin permission on it (R5.19); only set on
	// lists made with a user token.
	Admin bool
}

// RunnerScope is where a runner registers: a repository, or an organization
// with its runner group.
type RunnerScope struct {
	Owner        string
	Repo         string // empty for organization runners
	GroupID      int64
	Organization string
}

// Runner is a self-hosted runner as GitHub lists it.
type Runner struct {
	ID     int64
	Name   string
	OS     string
	Status string // online or offline
	Busy   bool
	Labels []string
}

// Variable is a repository Actions variable (R5.28).
type Variable struct {
	Name  string
	Value string
}

// WorkflowFile is one file of .github/workflows (R5.30).
type WorkflowFile struct {
	Path    string
	Content []byte
	ETag    string
}

// Job is a workflow job as GitHub reports it (R5.36 to R5.38).
type Job struct {
	ID         int64
	RunID      int64
	RunAttempt int
	Repository string
	Workflow   string
	// WorkflowPath is the workflow file of the run, ".github/workflows/ci.yml",
	// when the source knew it (the REST run list does; a webhook does not).
	WorkflowPath string
	Name         string
	HeadSHA      string
	HeadBranch   string
	Event        string
	FromFork     bool
	// Partial is true for a job read from a webhook: the event that started its
	// run and whether it came from a fork are not known yet (CRSP82). A partial
	// job never gets a runner until the poller reads the run.
	Partial     bool
	Labels      []string
	Status      string // queued, in_progress or completed
	Conclusion  string
	RunnerName  string
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	HTMLURL     string
	// ScaleSetID is set on a job that came from a scale-set session (R5.40).
	// The runner for such a job takes its JIT config from that scale set.
	ScaleSetID int64
}

// OAuth is the web and device sign-in flows (R5.8, R5.14).
type OAuth interface {
	// AuthorizeURL is GitHub's authorize page for a random state, a PKCE
	// challenge and the callback of the console address in use.
	AuthorizeURL(state, codeChallenge, redirectURI string) string
	// ExchangeCode trades an OAuth code (with the PKCE verifier) for a user token.
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (UserToken, error)
	// CurrentUser returns the user the token belongs to.
	CurrentUser(ctx context.Context, tok UserToken) (User, error)
	// ResolveLogin turns a login into a user id when a user is added (R5.11).
	ResolveLogin(ctx context.Context, login string) (User, error)
}

// AppClient is the App-level work: manifest conversion and App health (R5.3, R5.6).
type AppClient interface {
	// ConvertManifest trades the manifest code once
	// (POST /app-manifests/{code}/conversions).
	ConvertManifest(ctx context.Context, code string) (AppCredentials, error)
	// GetApp reads GET /app with an App JWT.
	GetApp(ctx context.Context) (App, error)
	// CheckAppKey signs an App JWT with a candidate private key and reads
	// GET /app; it returns ErrBadKey or ErrKeyRefused when the key does not
	// belong to the App (R5.7). The key is never stored or logged.
	CheckAppKey(ctx context.Context, appID int64, pemBytes []byte) error
	// SetAppHook sets the App's webhook address and secret
	// (PATCH /app/hook/config with an App JWT, OPS25).
	SetAppHook(ctx context.Context, h AppHook) error
	// ClearAppHook empties the App's webhook address and secret (OPS25).
	ClearAppHook(ctx context.Context) error
}

// AppHook is the webhook part of the App: where GitHub sends deliveries and
// the secret that signs them (OPS25). The content type is always JSON.
type AppHook struct {
	URL    string
	Secret string
}

// UserClient is what the console lists with the signed-in user's token
// (R5.17 to R5.19).
type UserClient interface {
	ListUserInstallations(ctx context.Context, tok UserToken) ([]Installation, error)
	ListInstallationRepositories(ctx context.Context, tok UserToken, installationID int64) ([]Repository, error)
}

// InstallationClient is the work done with an installation token.
type InstallationClient interface {
	GetInstallation(ctx context.Context, installationID int64) (Installation, error)

	// CreateRegistrationToken fetches a registration token right before a
	// runner registers (R5.31). The caller uses it once and drops it.
	CreateRegistrationToken(ctx context.Context, installationID int64, scope RunnerScope) (string, error)
	// CreateRemoveToken fetches a remove token to deregister a runner (R5.32).
	CreateRemoveToken(ctx context.Context, installationID int64, scope RunnerScope) (string, error)
	// GenerateJITConfig asks for the JIT config of one ephemeral runner (R5.34).
	// The caller hands it to the agent and drops it; it is never stored or
	// logged.
	GenerateJITConfig(ctx context.Context, installationID int64, scope RunnerScope, in JITRequest) (JITConfig, error)
	// ListRunners lists self-hosted runners of a scope.
	ListRunners(ctx context.Context, installationID int64, scope RunnerScope) ([]Runner, error)
	// DeleteRunner removes an offline runner whose agent cannot (R5.33).
	DeleteRunner(ctx context.Context, installationID int64, scope RunnerScope, runnerID int64) error

	// ListVariables, SetVariable and DeleteVariable keep CRSP_RUNS_ON_* correct
	// (R5.28, R5.29).
	ListVariables(ctx context.Context, installationID int64, owner, repo string) ([]Variable, error)
	SetVariable(ctx context.Context, installationID int64, owner, repo string, v Variable) error
	DeleteVariable(ctx context.Context, installationID int64, owner, repo, name string) error

	// ListWorkflowFiles reads .github/workflows with ETags (R5.30). A file whose
	// ETag matches etags is returned without Content.
	ListWorkflowFiles(ctx context.Context, installationID int64, owner, repo string, etags map[string]string) ([]WorkflowFile, error)

	// ListOpenJobs lists queued and in-progress jobs of a repository, the REST
	// polling fallback and the 5-minute reconcile (R5.37, R5.38).
	ListOpenJobs(ctx context.Context, installationID int64, owner, repo string) ([]Job, error)
	// GetJob reads one job, for history (observability R5.7).
	GetJob(ctx context.Context, installationID int64, owner, repo string, jobID int64) (Job, error)
}

// Client is the whole GitHub adapter.
type Client interface {
	OAuth
	AppClient
	UserClient
	InstallationClient
}

// WebhookVerifier checks a delivery's X-Hub-Signature-256 in constant time
// before the body is parsed (R6.5).
type WebhookVerifier interface {
	Verify(body []byte, signature256 string) bool
}

// Demand hides the three demand sources: the scale set (M2), the
// workflow_job webhook and REST polling (decision 0002, R5.36 to R5.38).
type Demand interface {
	// Jobs delivers job changes until ctx ends.
	Jobs(ctx context.Context) (<-chan Job, error)
}
