package registry

import (
	"errors"
	"time"
)

// Kind is standard (one image per profile for the whole pool) or custom (one
// per GitHub repository and profile). registry R5.4.
type Kind string

// The kinds.
const (
	KindStandard Kind = "standard"
	KindCustom   Kind = "custom"
)

// State is where an image is in its life (registry R5.29). The store keeps it
// in registry_images.state; the API shows it as RegistryImageState.
type State string

// The states.
const (
	// StateStaged is pushed to crsp/incoming and not checked yet. No other
	// machine is offered it.
	StateStaged State = "staged"
	// StateRefused failed the promotion check; it stays in staging until the
	// weekly cleanup removes it.
	StateRefused State = "refused"
	// StateCurrent is promoted and offered to every machine.
	StateCurrent State = "current"
	// StateVariant is promoted from a branch run that is not the default
	// branch (D19).
	StateVariant State = "variant"
	// StateSuperseded is replaced by a newer image; it stays until KeepUntil
	// so the owner can go back to it.
	StateSuperseded State = "superseded"
	// StateDeleted has its manifest removed; its blobs wait for the cleanup.
	StateDeleted State = "deleted"
)

// ScanState is the SBOM and vulnerability scan of an image (REG8).
type ScanState string

// The scan states.
const (
	ScanNotYet  ScanState = "not_scanned"
	ScanQueued  ScanState = "queued"
	ScanRunning ScanState = "scanning"
	ScanDone    ScanState = "done"
	ScanFailed  ScanState = "failed"
)

// Image is one promoted (or staged) image: one manifest under one name with
// one immutable tag, its Key.
type Image struct {
	ID       string // img_<random>, stable
	Kind     Kind
	Name     string // crsp/std/<profile> or crsp/custom/<owner>-<repo>/<profile>
	Key      Key
	Profile  string
	Arch     string // amd64 or arm64
	State    State
	Manifest string // manifest digest, sha256:<64 hex>; what a machine pulls (R5.9)
	// Inputs of the key, kept so the compare step can show what differs.
	StackHash  string
	BaseName   string // e.g. ubuntu:24.04, shown only
	BaseDigest string
	// Where it came from (OCI labels, R5.7).
	SourceRepository string // owner/name of the run that needed it; empty for a standard image
	Commit           string
	Branch           string // empty for the default branch
	PushedBy         string // machine id
	PushedAt         time.Time
	PromotedAt       time.Time
	// Lineage (REG3, REG7).
	Replaces   string // image id this one replaced
	ReplacedBy string
	KeepUntil  time.Time // zero unless superseded
	// Branch variants (REG6): when the branch merged or was deleted.
	BranchGoneAt time.Time
	// Sizes (REG4): all layers, and the layers no other image holds.
	SizeBytes   int64
	UniqueBytes int64
	LastPullAt  time.Time
	Scan        ScanState
}

// Layer is one blob of the registry with the images that hold it (REG4).
type Layer struct {
	Digest    string
	SizeBytes int64
	MediaType string
	// ChargedTo is the kind whose budget counts this layer: the kind of the
	// oldest image that holds it (RES20 4.2).
	ChargedTo Kind
	Images    []string // image ids
}

// Pointer says which image a repository's default branch uses for a profile
// and arch (RES20 5.2, REG3). A custom pointer moves only with the owner's yes
// (REG7); a standard pointer moves alone when crsp updates its own pins.
type Pointer struct {
	Repository string // owner/name
	Profile    string
	Arch       string
	ImageID    string
	Previous   string // image id before the last move, for rollback
	MovedAt    time.Time
	MovedBy    string // login, or "crsp"
}

// Rule is the reason of a recommendation (RES20 6.1, registry R5.43).
type Rule string

// The rules.
const (
	RuleReplace        Rule = "replace"         // an update was found; owner's yes
	RuleSuperseded     Rule = "superseded"      // past keep_until; crsp alone
	RuleVariant        Rule = "variant"         // branch merged or deleted, or unused 14 days; crsp alone
	RuleUnused         Rule = "unused"          // no pull for unused_days, no pointer; owner's yes
	RuleOverBudget     Rule = "over_budget"     // a kind is over its budget; owner's yes
	RuleDangling       Rule = "dangling"        // blobs no manifest names; crsp alone at the weekly cleanup
	RuleProfileRemoved Rule = "profile_removed" // standard image of a removed profile; owner's yes
)

// Decider says who acts on a recommendation.
type Decider string

// The deciders.
const (
	DecidedByOwner Decider = "owner"
	DecidedByCrsp  Decider = "crsp"
)

// AloneRules are the only rules crsp acts on without the owner (owner decision
// of 19 Nov 2026). Every other rule waits for a yes.
var AloneRules = map[Rule]bool{RuleSuperseded: true, RuleVariant: true, RuleDangling: true}

// Recommendation is one line of the recommendations list. It cannot be
// dismissed (owner decision); it goes away when its condition ends or it is
// acted on.
type Recommendation struct {
	ID         string
	Rule       Rule
	ImageID    string // the image it is about; empty for over_budget and dangling
	OtherID    string // for replace: the image it would replace
	Kind       Kind   // for over_budget
	FreesBytes int64
	Decider    Decider
	// DueAt is when crsp acts alone; zero when the owner decides. Shown ahead.
	DueAt     time.Time
	CreatedAt time.Time
}

// PlanState is the state of a replace plan, the states of the shared Wizard
// part (REG7).
type PlanState string

// The plan states.
const (
	PlanDraft      PlanState = "draft"
	PlanRunning    PlanState = "running"
	PlanStopped    PlanState = "stopped"
	PlanFailed     PlanState = "failed"
	PlanDone       PlanState = "done"
	PlanRolledBack PlanState = "rolled_back"
)

// ReplacePlan moves the pointers of the repositories that use Old to New,
// warms the machines that held Old, and keeps Old until KeepUntil (RES20 5.4).
type ReplacePlan struct {
	ID           string
	NewImageID   string
	OldImageID   string
	KeepDays     int
	KeepUntil    time.Time
	Repositories []string // owner/name whose pointer moves
	Machines     []string // machine ids to warm
	State        PlanState
	StepsDone    int
	StepsTotal   int
	Error        string
	CreatedBy    string
	CreatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
}

// Budget is the limit and use of one kind (registry R5.41).
type Budget struct {
	Kind       Kind
	LimitBytes int64
	UsedBytes  int64
	// SharedBytes are layers shared with the other kind, counted once.
	SharedBytes int64
}

// Default budgets and days (owner decisions of 19 Nov 2026).
const (
	DefaultStandardBudget  int64 = 40_000_000_000 // 40 GB
	DefaultCustomBudget    int64 = 30_000_000_000 // 30 GB
	DefaultKeepOldDays           = 7
	DefaultVariantGoneDays       = 7
	DefaultVariantUnused         = 14
	DefaultUnusedDays            = 30
	// TokenTTL is the life of a registry token (R5.10).
	TokenTTL = 10 * time.Minute
)

// Settings are the registry settings the owner edits (REG6). The sum of the
// budgets may never exceed half the free disk of Folder's volume.
type Settings struct {
	Folder          string // default <data_dir>/registry
	StandardBudget  int64
	CustomBudget    int64
	UnusedDays      int
	KeepOldDays     int
	VariantGoneDays int
	VariantUnused   int
	CleanupWeekday  time.Weekday // default Sunday
	CleanupHour     int          // local hour, default 3
}

// MirrorSettings are the pull-through mirror settings (REG9).
type MirrorSettings struct {
	Enabled     bool
	Upstreams   []string // registry hosts, e.g. docker.io, ghcr.io
	Folder      string   // default <data_dir>/registry-mirror
	BudgetBytes int64
	// TagRecheck is how long a mirrored tag is trusted before it is asked
	// again upstream (caches R5.66).
	TagRecheck time.Duration
}

// Scope is one access of a registry token (R5.11).
type Scope struct {
	Name    string // repository name, or a prefix ending in "*"
	Actions []string
}

// Token is a registry bearer token for one machine.
type Token struct {
	Value     string // never logged, never stored, never given to a job
	ExpiresAt time.Time
	Scopes    []Scope
}

// Source says how a machine got an image (PullReport, R5.30).
type Source string

// The sources.
const (
	SourceLocal  Source = "local"  // the engine had it
	SourcePulled Source = "pulled" // pulled from the registry
	SourceBuilt  Source = "built"  // built on the machine
)

// Errors every task uses. The API maps them to the error envelope.
var (
	ErrNotFound      = errors.New("registry: not found")
	ErrTagExists     = errors.New("registry: the tag exists and tags are immutable")
	ErrReadOnly      = errors.New("registry: read-only during the cleanup")
	ErrOverBudget    = errors.New("registry: the push would go over the budget or the free disk floor")
	ErrInUse         = errors.New("registry: the image is in use")
	ErrPromoteRefuse = errors.New("registry: the staged image failed the promotion check")
	ErrBusy          = errors.New("registry: the controller is busy, ask again")
)

// InUse is the answer of a delete that needs a yes (409 needs_confirmation):
// what uses the image now.
type InUse struct {
	RunningJobs  []string // job references, e.g. owner/app ci.yml test (started 14:05)
	Pointers     []Pointer
	InRollback   bool // inside keep_until
	PendingPulls int  // tokens that name it and have not ended
}

// Empty reports whether nothing uses the image.
func (u InUse) Empty() bool {
	return len(u.RunningJobs) == 0 && len(u.Pointers) == 0 && !u.InRollback && u.PendingPulls == 0
}
