package repos

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// GrantStepInstall is the step before the other two when the App is not
// installed where the task needs it (CRSP139, R5.86).
const GrantStepInstall = "install"

// AppReader reads the App as GitHub has it (GET /app with the App JWT): what
// it asks for. The GitHub adapter implements it.
type AppReader interface {
	GetApp(ctx context.Context) (github.App, error)
}

// TokenForgetter drops the cached token of an installation. A token keeps the
// permissions it was made with, so it is dropped when new ones are accepted.
type TokenForgetter interface {
	ForgetInstallationToken(id int64)
}

// GrantInput says what a task needs and where.
type GrantInput struct {
	// Needs are GitHub's permission names (github.RequiredPermissions keys).
	Needs []string
	// RepositoryID or InstallationID name the installation; with neither the
	// first stored installation is used.
	RepositoryID   int64
	InstallationID int64
}

// GrantView is the live state of a grant: one step list, the next step, and
// the permissions still missing, read from both sides of GitHub.
type GrantView struct {
	Ready          bool
	Steps          []github.GrantStep
	Next           string
	Missing        []string
	InstallationID int64
}

// installURL is GitHub's page that installs the App on more repositories.
func (s *Service) installURL(app github.App) string {
	return s.cfg.Addresses.InstallPage(app.Slug)
}

// freshInstallation reads the installation's permissions from GitHub, so a
// permission the owner just accepted counts at once; when GitHub cannot be
// reached it uses the stored copy. A change in what is accepted refreshes the
// stored copy and drops the cached installation token (R5.87).
func (s *Service) freshInstallation(ctx context.Context, id int64) (Installation, error) {
	stored, err := s.installation(ctx, id)
	if err != nil {
		return Installation{}, err
	}
	live, err := s.cfg.GitHub.ListAppInstallations(ctx)
	if err != nil {
		return stored, nil
	}
	for _, i := range live {
		if i.ID != id {
			continue
		}
		now := toInstallation(i)
		if !samePermissions(stored.Permissions, now.Permissions) {
			if err := s.cfg.Store.UpsertInstallation(ctx, now); err != nil {
				return Installation{}, err
			}
			s.forgetToken(id)
		}
		return now, nil
	}
	return stored, nil
}

func samePermissions(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (s *Service) forgetToken(id int64) {
	if f, ok := s.cfg.GitHub.(TokenForgetter); ok {
		f.ForgetInstallationToken(id)
	}
}

// PermissionsAccepted is the installation webhook with action
// new_permissions_accepted (R5.87): the cached token is dropped so the next
// call carries the new permissions, and the stored copy is read again.
func (s *Service) PermissionsAccepted(ctx context.Context, installationID int64) error {
	s.forgetToken(installationID)
	return s.ReconcileInstallations(ctx)
}

// Grant reads both sides of GitHub and says which steps are done for the
// permissions a task needs (R5.86).
func (s *Service) Grant(ctx context.Context, p auth.Principal, in GrantInput) (GrantView, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return GrantView{}, err
	}
	id := in.InstallationID
	if in.RepositoryID != 0 {
		r, err := s.cfg.Store.GetRepository(ctx, in.RepositoryID)
		if err != nil {
			return GrantView{}, err
		}
		id = r.InstallationID
	}
	if id == 0 {
		all, err := s.cfg.Store.ListInstallations(ctx)
		if err != nil {
			return GrantView{}, err
		}
		for _, i := range all {
			if !i.Suspended {
				id = i.ID
				break
			}
		}
	}
	var app github.App
	if ar, ok := s.cfg.GitHub.(AppReader); ok {
		a, err := ar.GetApp(ctx)
		if err != nil {
			return GrantView{}, err
		}
		app = a
	}
	if id == 0 {
		g := github.DiffGrant(in.Needs, nil, nil)
		steps := append([]github.GrantStep{{ID: GrantStepInstall, URL: s.installURL(app)}}, g.Steps...)
		for i := 1; i < len(steps); i++ {
			steps[i].URL = ""
		}
		return GrantView{Steps: steps, Next: GrantStepInstall, Missing: g.Missing}, nil
	}
	ins, err := s.freshInstallation(ctx, id)
	if err != nil {
		return GrantView{}, err
	}
	return s.grantFor(app, ins, in.Needs), nil
}

// grantFor makes the view for a known App and installation.
func (s *Service) grantFor(app github.App, ins Installation, needs []string) GrantView {
	g := github.DiffGrant(needs, app.Permissions, ins.Permissions)
	appURL, insURL := github.GrantPages(s.cfg.Addresses.GitHubURL, app, github.Installation{ID: ins.ID, AccountLogin: ins.AccountLogin, AccountType: ins.AccountType})
	if strings.TrimSpace(app.Slug) == "" {
		appURL = ""
	}
	steps := []github.GrantStep{{ID: GrantStepInstall, Done: true}}
	for _, st := range g.Steps {
		switch st.ID {
		case github.GrantStepAsk:
			st.URL = appURL
		case github.GrantStepAccept:
			st.URL = insURL
		}
		steps = append(steps, st)
	}
	return GrantView{Ready: g.Ready, Steps: steps, Next: g.Next, Missing: g.Missing, InstallationID: ins.ID}
}
