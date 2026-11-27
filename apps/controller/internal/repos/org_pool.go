package repos

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Pool per organization (github-integration R5.21). An organization owner
// turns the pool on for an organization. The controller then registers runners
// at organization level, in a runner group named for the pool. The group is
// visible to selected repositories only, and those are exactly the repositories
// whose pool is on. A personal account has no such mode: its runners stay at
// repository level.

// OrgPool is the stored state of an organization's pool mode.
type OrgPool struct {
	InstallationID int64
	Organization   string
	Enabled        bool
	GroupID        int64
	GroupName      string
	ChangedAt      time.Time
	ChangedBy      string
}

// OrgPoolView is an organization installation with its pool mode, for the
// console, the API and the CLI.
type OrgPoolView struct {
	OrgPool
	// IsOrganization is false for a personal account.
	IsOrganization bool
	// Eligible is true when the pool per organization can be turned on: an
	// organization, not suspended, with the organization runner permission.
	Eligible bool
	// Reason says why it is not eligible, or is empty.
	Reason string
	// Repositories are the full names of the repositories the group is visible
	// to now: the repositories of the organization whose pool is on.
	Repositories []string
}

// ErrNotOrganization: the installation is a personal account.
type ErrNotOrganization struct{ Account string }

func (e *ErrNotOrganization) Error() string {
	return fmt.Sprintf("%s is a personal account; its runners stay at repository level", e.Account)
}

// ErrNotOrgOwner: the user is not an owner of the organization on GitHub.
type ErrNotOrgOwner struct{ Org string }

func (e *ErrNotOrgOwner) Error() string {
	return fmt.Sprintf("you are not an owner of the %s organization in GitHub", e.Org)
}

// Is lets errors.Is(err, auth.ErrForbidden) match.
func (e *ErrNotOrgOwner) Is(target error) bool { return target == auth.ErrForbidden }

// orgPermission is the installation permission that organization runners and
// runner groups need (R5.23).
const orgPermission = "organization_self_hosted_runners"

func (s *Service) installation(ctx context.Context, id int64) (Installation, error) {
	ins, err := s.cfg.Store.ListInstallations(ctx)
	if err != nil {
		return Installation{}, err
	}
	for _, i := range ins {
		if i.ID == id {
			return i, nil
		}
	}
	return Installation{}, auth.ErrNotFound
}

func eligibility(in Installation) (bool, string) {
	switch {
	case in.AccountType != "Organization":
		return false, (&ErrNotOrganization{Account: in.AccountLogin}).Error()
	case in.Suspended:
		return false, "the GitHub App is suspended on " + in.AccountLogin
	case github.MissingPermissionsOf(in.Permissions, orgPermission) != nil:
		return false, "the App lacks the " + orgPermission + " permission; accept it in the installation settings on GitHub"
	}
	return true, ""
}

func (s *Service) groupName() string {
	if s.cfg.PoolName != "" {
		return s.cfg.PoolName
	}
	return "crsp"
}

func (s *Service) storedOrgPool(ctx context.Context, in Installation) (OrgPool, error) {
	op, err := s.cfg.Store.GetOrgPool(ctx, in.ID)
	if errors.Is(err, auth.ErrNotFound) {
		return OrgPool{InstallationID: in.ID, Organization: in.AccountLogin, GroupName: s.groupName()}, nil
	}
	return op, err
}

// OrgPools lists every installation with its pool mode (viewer).
func (s *Service) OrgPools(ctx context.Context, p auth.Principal) ([]OrgPoolView, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return nil, err
	}
	ins, err := s.cfg.Store.ListInstallations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OrgPoolView, 0, len(ins))
	for _, in := range ins {
		v, err := s.orgPoolView(ctx, in)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// OrgPool returns one installation's pool mode (viewer).
func (s *Service) OrgPool(ctx context.Context, p auth.Principal, installationID int64) (OrgPoolView, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return OrgPoolView{}, err
	}
	in, err := s.installation(ctx, installationID)
	if err != nil {
		return OrgPoolView{}, err
	}
	return s.orgPoolView(ctx, in)
}

func (s *Service) orgPoolView(ctx context.Context, in Installation) (OrgPoolView, error) {
	op, err := s.storedOrgPool(ctx, in)
	if err != nil {
		return OrgPoolView{}, err
	}
	v := OrgPoolView{OrgPool: op, IsOrganization: in.AccountType == "Organization", Repositories: []string{}}
	v.Eligible, v.Reason = eligibility(in)
	for _, r := range s.poolOnRepos(ctx, in.ID) {
		v.Repositories = append(v.Repositories, r.FullName)
	}
	return v, nil
}

// poolOnRepos are the repositories of an installation whose pool is on, in id
// order. The group of the organization is visible to these and no other.
func (s *Service) poolOnRepos(ctx context.Context, installationID int64) []Repository {
	on := true
	rs, _ := s.cfg.Store.ListRepositories(ctx, ListFilter{InstallationID: installationID, Pool: &on})
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

// requireOrgOwner reads, fresh, that the user is an active owner of the
// organization on GitHub. With the user's own token it asks as the user; when
// that token is gone it asks as the App. A GitHub error fails closed.
func (s *Service) requireOrgOwner(ctx context.Context, p auth.Principal, in Installation) error {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return err
	}
	if p.Source == "controller" {
		return nil
	}
	notOwner := &ErrNotOrgOwner{Org: in.AccountLogin}
	var role string
	var active bool
	tok, err := s.cfg.Tokens.GitHubToken(p.UserID)
	switch {
	case err == nil:
		role, active, err = s.cfg.GitHub.OrgMembership(ctx, tok, in.AccountLogin)
	case errors.Is(err, auth.ErrGitHubSessionExpired):
		role, active, err = s.cfg.GitHub.OrgMemberRole(ctx, in.ID, in.AccountLogin, p.Login)
	}
	if err != nil {
		if errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrForbidden) {
			return notOwner
		}
		return err
	}
	if !active || role != github.OrgRoleOwner {
		return notOwner
	}
	return nil
}

// EnableOrgPool turns the pool on for an organization. The caller must be an
// admin of the product and an owner of the organization on GitHub, read fresh.
// It finds or creates the runner group named for the pool, limits it to the
// repositories whose pool is on, and reads it back to check it is no wider.
func (s *Service) EnableOrgPool(ctx context.Context, p auth.Principal, installationID int64) (OrgPoolView, error) {
	in, err := s.installation(ctx, installationID)
	if err != nil {
		return OrgPoolView{}, err
	}
	if ok, why := eligibility(in); !ok {
		if in.AccountType != "Organization" {
			return OrgPoolView{}, &ErrNotOrganization{Account: in.AccountLogin}
		}
		return OrgPoolView{}, &auth.InvalidError{Msg: why}
	}
	if err := s.requireOrgOwner(ctx, p, in); err != nil {
		return OrgPoolView{}, err
	}
	op, err := s.storedOrgPool(ctx, in)
	if err != nil {
		return OrgPoolView{}, err
	}
	group, err := s.ensureGroup(ctx, in, op.GroupName)
	if err != nil {
		return OrgPoolView{}, err
	}
	op.GroupID, op.GroupName, op.Enabled = group.ID, group.Name, true
	op.ChangedAt, op.ChangedBy = s.cfg.Now(), p.Login
	if err := s.cfg.Store.SetOrgPool(ctx, op); err != nil {
		return OrgPoolView{}, err
	}
	s.cfg.Audit.Audit(ctx, p, "org_pool.on", "organization", in.AccountLogin,
		fmt.Sprintf("runner group %q, selected repositories only", group.Name))
	return s.orgPoolView(ctx, in)
}

// DisableOrgPool turns the pool mode off. New runners register at repository
// level again. Runners already registered at organization level are removed
// after their running jobs end (drain, never kill), with the scope they were
// registered with. The group is left as it is, never widened.
func (s *Service) DisableOrgPool(ctx context.Context, p auth.Principal, installationID int64) (OrgPoolView, error) {
	in, err := s.installation(ctx, installationID)
	if err != nil {
		return OrgPoolView{}, err
	}
	if in.AccountType != "Organization" {
		return OrgPoolView{}, &ErrNotOrganization{Account: in.AccountLogin}
	}
	if err := s.requireOrgOwner(ctx, p, in); err != nil {
		return OrgPoolView{}, err
	}
	op, err := s.storedOrgPool(ctx, in)
	if err != nil {
		return OrgPoolView{}, err
	}
	if op.Enabled {
		op.Enabled, op.ChangedAt, op.ChangedBy = false, s.cfg.Now(), p.Login
		if err := s.cfg.Store.SetOrgPool(ctx, op); err != nil {
			return OrgPoolView{}, err
		}
		s.cfg.Audit.Audit(ctx, p, "org_pool.off", "organization", in.AccountLogin,
			"new runners register at repository level; runners that have a job finish it")
	}
	return s.orgPoolView(ctx, in)
}

// ensureGroup finds the runner group with the pool's name, or creates it, and
// makes it visible to the repositories whose pool is on and to no others.
// The default group is never used. A group that was wider is narrowed first.
func (s *Service) ensureGroup(ctx context.Context, in Installation, name string) (github.RunnerGroup, error) {
	want := repoIDs(s.poolOnRepos(ctx, in.ID))
	groups, err := s.cfg.GitHub.ListRunnerGroups(ctx, in.ID, in.AccountLogin)
	if err != nil {
		return github.RunnerGroup{}, err
	}
	for _, g := range groups {
		if !strings.EqualFold(g.Name, name) {
			continue
		}
		if g.Default {
			return github.RunnerGroup{}, &auth.InvalidError{Msg: fmt.Sprintf("the runner group %q is the default group of %s; it cannot be limited to selected repositories", g.Name, in.AccountLogin)}
		}
		if g.Visibility != github.VisibilitySelected {
			if err := s.cfg.GitHub.SelectRunnerGroup(ctx, in.ID, in.AccountLogin, g.ID); err != nil {
				return github.RunnerGroup{}, err
			}
		}
		if err := s.cfg.GitHub.SetRunnerGroupRepositories(ctx, in.ID, in.AccountLogin, g.ID, want); err != nil {
			return github.RunnerGroup{}, err
		}
		g.Visibility = github.VisibilitySelected
		return g, s.verifyGroup(ctx, in, g.ID, want)
	}
	g, err := s.cfg.GitHub.CreateRunnerGroup(ctx, in.ID, in.AccountLogin, name, want)
	if err != nil {
		return github.RunnerGroup{}, err
	}
	return g, s.verifyGroup(ctx, in, g.ID, want)
}

// verifyGroup reads the group back. A group wider than the wanted repositories
// is an error: the controller never leaves one behind.
func (s *Service) verifyGroup(ctx context.Context, in Installation, groupID int64, want []int64) error {
	got, err := s.cfg.GitHub.ListRunnerGroupRepositories(ctx, in.ID, in.AccountLogin, groupID)
	if err != nil {
		return fmt.Errorf("the runner group could not be checked, so it is not used: %w", err)
	}
	if !sameIDs(got, want) {
		return fmt.Errorf("the runner group of %s is visible to repositories that are not selected; it is not used", in.AccountLogin)
	}
	return nil
}

// SyncOrgGroup keeps the organization's runner group at the repositories whose
// pool is on. It does nothing for a personal account or when the pool mode is
// off. Callers run it after a repository's pool changes (R5.20, R5.22).
func (s *Service) SyncOrgGroup(ctx context.Context, installationID int64) error {
	in, err := s.installation(ctx, installationID)
	if errors.Is(err, auth.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if in.AccountType != "Organization" {
		return nil
	}
	op, err := s.cfg.Store.GetOrgPool(ctx, installationID)
	if errors.Is(err, auth.ErrNotFound) || (err == nil && (!op.Enabled || op.GroupID == 0)) {
		return nil
	}
	if err != nil {
		return err
	}
	want := repoIDs(s.poolOnRepos(ctx, installationID))
	if err := s.cfg.GitHub.SetRunnerGroupRepositories(ctx, in.ID, in.AccountLogin, op.GroupID, want); err != nil {
		return err
	}
	return s.verifyGroup(ctx, in, op.GroupID, want)
}

// OrgRunnerScope says where runners of a repository register now. For a
// repository of an organization whose pool mode is on it returns the
// organization, the group name and true; for anything else, including every
// repository of a personal account, it returns false and the caller registers
// at repository level.
func OrgRunnerScope(ctx context.Context, st Store, repoID int64) (OrgPool, bool, error) {
	r, err := st.GetRepository(ctx, repoID)
	if err != nil {
		return OrgPool{}, false, err
	}
	if !r.PoolOn {
		return OrgPool{}, false, nil
	}
	ins, err := st.ListInstallations(ctx)
	if err != nil {
		return OrgPool{}, false, err
	}
	for _, in := range ins {
		if in.ID != r.InstallationID || in.AccountType != "Organization" || in.Removed || in.Suspended {
			continue
		}
		op, err := st.GetOrgPool(ctx, in.ID)
		if errors.Is(err, auth.ErrNotFound) {
			return OrgPool{}, false, nil
		}
		if err != nil {
			return OrgPool{}, false, err
		}
		if op.Enabled && op.GroupID != 0 {
			op.Organization = in.AccountLogin
			return op, true, nil
		}
	}
	return OrgPool{}, false, nil
}

// OrgPoolByLogin finds the stored pool mode of an organization by its login.
func OrgPoolByLogin(ctx context.Context, st Store, login string) (OrgPool, bool, error) {
	ins, err := st.ListInstallations(ctx)
	if err != nil {
		return OrgPool{}, false, err
	}
	for _, in := range ins {
		if !strings.EqualFold(in.AccountLogin, login) || in.Removed {
			continue
		}
		op, err := st.GetOrgPool(ctx, in.ID)
		if errors.Is(err, auth.ErrNotFound) {
			return OrgPool{}, false, nil
		}
		if err != nil {
			return OrgPool{}, false, err
		}
		op.Organization = in.AccountLogin
		return op, true, nil
	}
	return OrgPool{}, false, nil
}

func repoIDs(rs []Repository) []int64 {
	ids := make([]int64, 0, len(rs))
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	return ids
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int64(nil), a...), append([]int64(nil), b...)
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	sort.Slice(y, func(i, j int) bool { return y[i] < y[j] })
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
