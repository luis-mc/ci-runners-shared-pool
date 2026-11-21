package repos

import (
	"context"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Config builds a Service.
type Config struct {
	// Addresses are GitHub's addresses for the pages linked to the owner; the
	// zero value takes the github.com defaults.
	Addresses addresses.Set
	Store     Store
	GitHub    GitHub
	Tokens    TokenSource
	Audit     Auditor
	Capacity  Capacity
	// Sizes lists the machines with their pool size, for the size variables
	// such as RUNS_ON_LINUX_LARGE (scheduling R5.67); nil writes none.
	Sizes SizeCapacity
	// SizeFloors is the owner's map of size name to floor; nil or empty means
	// the size class table (DefaultSizeFloors).
	SizeFloors func() map[string]SizeFloor
	// Ephemeral says whether the pool can make an ephemeral runner now; nil
	// means unknown and the repository view claims nothing (R5.60).
	Ephemeral EphemeralCapacity
	// Registrar keeps the hidden registrations of the pool runners; nil means
	// none are kept.
	Registrar PoolRegistrar
	// PoolName names the runner group of the pool per organization (R5.21);
	// empty means "crsp".
	PoolName string
	// Fleet removes job runners of a repository that stops being served (CRSP82); nil means none.
	Fleet Fleet
	// Up says which OSes have a machine that is online and accepting jobs; nil
	// turns the hosted fallback off for the whole controller (R5.43).
	Up UpSource
	// FallbackGrace is how long no machine of an OS must have been up before an
	// opted-in repository falls back; 0 means DefaultFallbackGrace.
	FallbackGrace func() time.Duration
	Now           func() time.Time
	// FactsReported says whether every machine that was online has reported its
	// facts since this start; nil means unknown, so only the settle period
	// (CapacitySettle) makes capacity known.
	FactsReported func(ctx context.Context) (bool, error)
	// PublicRepositories reads the setting Public repositories (R5.54); nil
	// means off. A read error refuses a public repository.
	PublicRepositories func(ctx context.Context) (bool, error)
	// OnProgress, when set, is called with the plan after each step and when
	// the plan ends or is stopped (the plan.progress event).
	OnProgress func(Plan)
}

// Service is the pool switch, policy, variables and runs-on check.
type Service struct {
	cfg Config

	mu     sync.Mutex
	admin  map[int64]map[int64]bool // user id -> repository id -> GitHub admin, from the last sync; a missing repository is unknown
	synced map[int64]time.Time
	etags  map[int64]map[string]string // repository id -> workflow path -> etag
	cache  map[int64]map[string][]byte // repository id -> workflow path -> content

	fb fallbackState

	started time.Time
	absent  map[string]time.Time // repository/variable -> first sync that saw its OS capacity absent

	stopMu  sync.Mutex
	stops   map[string]bool // plan id -> a stop was asked while it applies
	running map[string]bool // plan id -> ApplyPlan is running it in this process
}

// New returns a Service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Addresses == (addresses.Set{}) {
		cfg.Addresses = addresses.Defaults()
	}
	return &Service{cfg: cfg, admin: map[int64]map[int64]bool{}, synced: map[int64]time.Time{},
		etags: map[int64]map[string]string{}, cache: map[int64]map[string][]byte{}, stops: map[string]bool{}, running: map[string]bool{}, fb: newFallbackState(), started: cfg.Now(), absent: map[string]time.Time{}}
}

// SetRegistrar sets who keeps the registrations of the pool runners. The
// wiring calls it once the runners service exists.
func (s *Service) SetRegistrar(r PoolRegistrar) { s.cfg.Registrar = r }

// EphemeralCapacity says whether an ephemeral pool ceiling slot exists on an
// online machine. (*runners.Service).EphemeralCapacity implements it.
type EphemeralCapacity interface {
	EphemeralCapacity(ctx context.Context) (bool, error)
}

// SetEphemeral sets who answers whether the pool has ephemeral capacity. The
// wiring calls it once the runners service exists.
func (s *Service) SetEphemeral(e EphemeralCapacity) { s.cfg.Ephemeral = e }

// HasEphemeralCapacity reports whether the pool can make an ephemeral runner
// now. known is false when no source is wired or it failed.
func (s *Service) HasEphemeralCapacity(ctx context.Context) (has, known bool) {
	if s.cfg.Ephemeral == nil {
		return false, false
	}
	ok, err := s.cfg.Ephemeral.EphemeralCapacity(ctx)
	if err != nil {
		return false, false
	}
	return ok, true
}

// ErrNotAdmin: the user lacks the GitHub admin permission on the repository.
type ErrNotAdmin struct{ Repo string }

func (e *ErrNotAdmin) Error() string {
	return fmt.Sprintf("you do not have admin rights on %s in GitHub", e.Repo)
}

// Is lets errors.Is(err, auth.ErrForbidden) match.
func (e *ErrNotAdmin) Is(target error) bool { return target == auth.ErrForbidden }

// Sync lists the installations and repositories the App can reach and keeps
// them in the store. It reads them with the App's own key (an installation
// token), so it needs no personal sign-in and survives a restart (R5.17,
// R5.18). When the user's GitHub token is still in memory it also refreshes
// the user's admin flags; without it the flags stay as they were and the
// admin check (requireGitHubAdmin) still reads the permission fresh. A failed
// App call is returned as it is, so the page shows the real reason.
func (s *Service) Sync(ctx context.Context, p auth.Principal) error {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return err
	}
	bg := github.Background(ctx)
	ins, err := s.cfg.GitHub.ListAppInstallations(bg)
	if err != nil {
		return err
	}
	for _, in := range ins {
		if err := s.cfg.Store.UpsertInstallation(ctx, toInstallation(in)); err != nil {
			return err
		}
		if in.Suspended {
			continue
		}
		repos, err := s.cfg.GitHub.ListAppRepositories(bg, in.ID)
		if err != nil {
			return err
		}
		for _, r := range repos {
			if err := s.cfg.Store.UpsertRepository(ctx, toRepository(r, in.ID)); err != nil {
				return err
			}
		}
	}
	flags, ok := s.userAdminFlags(ctx, p)
	if !ok {
		flags = s.appAdminFlags(ctx, p, ins)
	}
	s.mu.Lock()
	if flags != nil {
		s.admin[p.UserID] = flags
	}
	s.synced[p.UserID] = s.cfg.Now()
	s.mu.Unlock()
	return nil
}

// userAdminFlags reads which repositories the user administers, with the
// user's own token. ok is false when the token is gone or GitHub refuses: the
// list does not depend on it, and the caller asks the App instead.
func (s *Service) userAdminFlags(ctx context.Context, p auth.Principal) (flags map[int64]bool, ok bool) {
	tok, err := s.cfg.Tokens.GitHubToken(p.UserID)
	if err != nil {
		return nil, false
	}
	ins, err := s.cfg.GitHub.ListUserInstallations(ctx, tok)
	if err != nil {
		return nil, false
	}
	flags = map[int64]bool{}
	for _, in := range ins {
		repos, err := s.cfg.GitHub.ListInstallationRepositories(ctx, tok, in.ID)
		if err != nil {
			return nil, false
		}
		for _, r := range repos {
			flags[r.ID] = r.Admin
		}
	}
	return flags, true
}

// appAdminFlags reads the user's admin rights with the App, the way
// requireGitHubAdmin does when the user's token is gone (OPS15): the owner of
// a user-account installation is admin, any other repository is asked with
// CollaboratorPermission. A repository whose answer is not a clear yes or no
// (a GitHub error) is left out of the map: its flag is unknown, never false.
func (s *Service) appAdminFlags(ctx context.Context, p auth.Principal, ins []github.Installation) map[int64]bool {
	if p.Login == "" {
		return nil
	}
	flags := map[int64]bool{}
	for _, in := range ins {
		if in.Suspended {
			continue
		}
		repos, err := s.cfg.Store.ListRepositories(ctx, ListFilter{InstallationID: in.ID})
		if err != nil {
			continue
		}
		ownsAccount := in.AccountType != "Organization" && strings.EqualFold(in.AccountLogin, p.Login)
		for _, r := range repos {
			if ownsAccount {
				flags[r.ID] = true
				continue
			}
			perm, err := s.cfg.GitHub.CollaboratorPermission(ctx, in.ID, r.Owner, r.Name, p.Login)
			switch {
			case err == nil:
				flags[r.ID] = strings.EqualFold(perm, "admin")
			case errors.Is(err, github.ErrNotFound):
				flags[r.ID] = false
			}
		}
	}
	return flags
}

// LastSync is when the user's listing was last refreshed from GitHub.
func (s *Service) LastSync(userID int64) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.synced[userID]
}

func toInstallation(i github.Installation) Installation {
	return Installation{ID: i.ID, AccountLogin: i.AccountLogin, AccountType: i.AccountType,
		Selection: i.RepositorySelection, Permissions: i.Permissions, Suspended: i.Suspended}
}

func toRepository(r github.Repository, installationID int64) Repository {
	return Repository{ID: r.ID, InstallationID: installationID, FullName: r.FullName, Owner: r.Owner, Name: r.Name,
		Visibility: r.Visibility, DefaultBranch: r.DefaultBranch, Archived: r.Archived}
}

// missingFor names what an installation lacks. The organization runner
// permission only exists on an organization account, so a personal account is
// never asked for it (R5.24, R5.86).
func missingFor(i Installation) []string {
	var out []string
	for _, n := range github.MissingPermissions(i.Permissions) {
		if n == orgPermission && i.AccountType != "Organization" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// Installations lists the stored installations with the permissions they lack
// (R5.17, R5.24).
type InstallationView struct {
	Installation
	MissingPermissions []string
}

// Installations returns the known installations and what each lacks.
func (s *Service) Installations(ctx context.Context, p auth.Principal) ([]InstallationView, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return nil, err
	}
	ins, err := s.cfg.Store.ListInstallations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstallationView, 0, len(ins))
	for _, i := range ins {
		out = append(out, InstallationView{Installation: i, MissingPermissions: missingFor(i)})
	}
	return out, nil
}

// RepositoryView is a repository with its policy, variables and the user's
// GitHub admin flag.
type RepositoryView struct {
	Repository
	Policy      Policy
	Variables   []ManagedVariable
	UserIsAdmin bool
	// AdminKnown is false when the flag was not read (no token and the App
	// could not say): the page must not claim the user lacks admin rights (OPS15).
	AdminKnown     bool
	FindingsFail   int
	FindingsWarn   int
	VariablesDrift bool
	// Routing is where the repository's jobs go now and why (R5.44).
	Routing Routing
	// EphemeralCapacity is false when the repository's jobs go to ephemeral
	// runners only and the pool cannot make one, so its jobs would wait. Nil
	// while the pool is off for the repository or the answer is unknown (R5.60).
	EphemeralCapacity *bool
	// Problem is a fault the owner can fix, or nil (R5.56).
	Problem *Problem
}

func (s *Service) view(ctx context.Context, p auth.Principal, r Repository) (RepositoryView, error) {
	v := RepositoryView{Repository: r, Policy: DefaultPolicy()}
	if pol, err := s.cfg.Store.GetPolicy(ctx, r.ID); err == nil {
		v.Policy = pol
	} else if !errors.Is(err, auth.ErrNotFound) {
		return v, err
	}
	vars, err := s.cfg.Store.ListManagedVariables(ctx, r.ID)
	if err != nil {
		return v, err
	}
	v.Variables = vars
	for _, mv := range vars {
		if mv.Status != VarOK {
			v.VariablesDrift = true
		}
	}
	fs, _, err := s.cfg.Store.ListFindings(ctx, r.ID)
	if err != nil {
		return v, err
	}
	for _, f := range fs {
		if f.Severity() == SeverityFail {
			v.FindingsFail++
		} else {
			v.FindingsWarn++
		}
	}
	if rt, err := s.routing(ctx, r, v.Policy); err == nil {
		v.Routing = rt
	}
	if r.PoolOn && v.Policy.EphemeralOnly(r.Visibility) {
		if has, known := s.HasEphemeralCapacity(ctx); known {
			v.EphemeralCapacity = &has
		}
	}
	v.Problem = s.problemOf(ctx, r)
	s.mu.Lock()
	v.UserIsAdmin, v.AdminKnown = s.admin[p.UserID][r.ID]
	s.mu.Unlock()
	return v, nil
}

// List returns one page of repositories from the store, after the last full
// name in f.AfterName. The listing was filled by Sync; the page's age is
// LastSync (R6.11).
func (s *Service) List(ctx context.Context, p auth.Principal, f ListFilter) ([]RepositoryView, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return nil, err
	}
	rs, err := s.cfg.Store.ListRepositories(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]RepositoryView, 0, len(rs))
	for _, r := range rs {
		v, err := s.view(ctx, p, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns one repository view.
func (s *Service) Get(ctx context.Context, p auth.Principal, id int64) (RepositoryView, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return RepositoryView{}, err
	}
	r, err := s.cfg.Store.GetRepository(ctx, id)
	if err != nil {
		return RepositoryView{}, err
	}
	return s.view(ctx, p, r)
}

// requireGitHubAdmin reads the user's GitHub admin permission fresh, at the
// moment of the change (R5.19). With the user's own token it asks GitHub as the
// user; when that token is gone it asks as the App for the user's permission,
// which answers the same question. GitHub errors fail closed.
func (s *Service) requireGitHubAdmin(ctx context.Context, p auth.Principal, r Repository) error {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return err
	}
	if p.Source == "controller" {
		return nil
	}
	if tok, err := s.cfg.Tokens.GitHubToken(p.UserID); err == nil {
		ok, err := s.cfg.GitHub.UserCanAdmin(ctx, tok, r.Owner, r.Name)
		if err != nil {
			if errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrForbidden) {
				return &ErrNotAdmin{Repo: r.FullName}
			}
			return err
		}
		if !ok {
			return &ErrNotAdmin{Repo: r.FullName}
		}
		return nil
	} else if !errors.Is(err, auth.ErrGitHubSessionExpired) {
		return err
	}
	perm, err := s.cfg.GitHub.CollaboratorPermission(ctx, r.InstallationID, r.Owner, r.Name, p.Login)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return &ErrNotAdmin{Repo: r.FullName}
		}
		return err
	}
	if !strings.EqualFold(perm, "admin") {
		return &ErrNotAdmin{Repo: r.FullName}
	}
	return nil
}

// UpdatePolicy changes a repository's policy (R5.25 to R5.27). It needs the
// product role admin or owner and the GitHub admin permission, read fresh.
// Fork pull requests on ask for ConfirmForkRisk. Turning them off, or leaving
// them off, makes sure the repository's fork approval setting holds outside
// contributors' runs (R5.26). When the pool is on, the variables are
// rewritten and the runs-on check runs again.
func (s *Service) UpdatePolicy(ctx context.Context, p auth.Principal, id int64, u PolicyUpdate) (Policy, error) {
	r, err := s.cfg.Store.GetRepository(ctx, id)
	if err != nil {
		return Policy{}, err
	}
	if err := s.requireGitHubAdmin(ctx, p, r); err != nil {
		return Policy{}, err
	}
	old, err := s.policyOrDefault(ctx, id)
	if err != nil {
		return Policy{}, err
	}
	np, err := u.Apply(old)
	if err != nil {
		return Policy{}, err
	}
	if np.HostedFallback && r.Visibility == "public" {
		return Policy{}, auth.Invalid("%s is a public repository; the hosted fallback is for private repositories only, so a public one never runs on GitHub-hosted runners", r.FullName)
	}
	np.UpdatedAt, np.UpdatedBy = s.cfg.Now(), p.Login
	if r.PoolOn && !np.AllowForkPRs {
		if err := s.guardForks(ctx, r); err != nil {
			return Policy{}, err
		}
	}
	if err := s.cfg.Store.UpsertPolicy(ctx, id, np); err != nil {
		return Policy{}, err
	}
	s.cfg.Audit.Audit(ctx, p, "policy.changed", "repository", r.FullName, describePolicy(old, np))
	if !np.HostedFallback && s.dropFallback(id) {
		// Turned off while on fallback: the switch back is logged like any
		// other (R5.43); syncVariables below writes the crsp labels at once.
		s.cfg.Audit.Audit(ctx, p, "fallback.ended", "repository", r.FullName, "fallback was turned off for this repository")
	}
	if r.PoolOn {
		if err := s.syncVariables(ctx, p, r, np); err != nil {
			return np, err
		}
		_ = s.CheckRunsOn(ctx, id)
	}
	return np, nil
}

func (s *Service) policyOrDefault(ctx context.Context, id int64) (Policy, error) {
	pol, err := s.cfg.Store.GetPolicy(ctx, id)
	if errors.Is(err, auth.ErrNotFound) {
		return DefaultPolicy(), nil
	}
	return pol, err
}

// guardForks sets the fork approval setting on a public repository so that
// every outside contributor's run waits for approval (R5.26, guard one). The
// job-started hook is guard two.
func (s *Service) guardForks(ctx context.Context, r Repository) error {
	if r.Visibility != "public" {
		return nil
	}
	return s.cfg.GitHub.SetForkApproval(ctx, r.InstallationID, r.Owner, r.Name, github.ForkApprovalAllOutside)
}

func describePolicy(a, b Policy) string {
	var d []string
	if a.Priority != b.Priority {
		d = append(d, fmt.Sprintf("priority %d to %d", a.Priority, b.Priority))
	}
	if a.MaxParallelJobs != b.MaxParallelJobs {
		d = append(d, fmt.Sprintf("parallel %d to %d", a.MaxParallelJobs, b.MaxParallelJobs))
	}
	if a.AllowForkPRs != b.AllowForkPRs {
		d = append(d, fmt.Sprintf("fork pull requests %t to %t", a.AllowForkPRs, b.AllowForkPRs))
	}
	if strings.Join(a.Labels, ",") != strings.Join(b.Labels, ",") {
		d = append(d, "labels "+strings.Join(b.Labels, ","))
	}
	if strings.Join(a.RunnerGroups, ",") != strings.Join(b.RunnerGroups, ",") {
		d = append(d, "runner groups "+strings.Join(b.RunnerGroups, ","))
	}
	if strings.Join(a.ExtraPackages, ",") != strings.Join(b.ExtraPackages, ",") {
		d = append(d, "extra packages "+strings.Join(b.ExtraPackages, ","))
	}
	if a.LegacyVars != b.LegacyVars {
		d = append(d, fmt.Sprintf("old variable names %t to %t", a.LegacyVars, b.LegacyVars))
	}
	if a.HostedFallback != b.HostedFallback {
		d = append(d, fmt.Sprintf("hosted fallback %t to %t", a.HostedFallback, b.HostedFallback))
	}
	if a.FallbackCapMinutes != b.FallbackCapMinutes {
		d = append(d, fmt.Sprintf("fallback cap %d to %d minutes", a.FallbackCapMinutes, b.FallbackCapMinutes))
	}
	if fmt.Sprint(a.HostedLabels) != fmt.Sprint(b.HostedLabels) {
		d = append(d, fmt.Sprintf("hosted labels %v", b.HostedLabels))
	}
	if a.RunnerKinds != b.RunnerKinds {
		k := b.RunnerKinds
		if k == "" {
			k = RunnerKindsDefault
		}
		d = append(d, "runner kinds "+k)
	}
	if a.BudgetMemoryPct != b.BudgetMemoryPct || a.BudgetCPUPct != b.BudgetCPUPct {
		d = append(d, "budget")
	}
	if len(d) == 0 {
		return "no change"
	}
	return strings.Join(d, "; ")
}
