package repos

import (
	"context"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
)

// RunsOnPullRequests is the routing service as this package needs it: the
// patch of a repository, and the one pull request that applies it.
// (*routing.Service) implements it.
type RunsOnPullRequests interface {
	PatchFor(ctx context.Context, r routing.Repo) (routing.Patch, error)
	OpenPR(ctx context.Context, r routing.Repo) (routing.PRResult, error)
}

// SetRunsOnPullRequests sets who builds and opens the runs-on pull request.
// The wiring calls it once the routing service exists; nil leaves the feature
// off.
func (s *Service) SetRunsOnPullRequests(r RunsOnPullRequests) { s.cfg.RunsOnPR = r }

// ErrRunsOnPRUnavailable: no routing service is wired in this build.
var ErrRunsOnPRUnavailable = errors.New("the runs-on pull request is not available in this build")

// States of a preview and of a result.
const (
	PRPreviewChange  = "change"
	PRNothing        = "nothing_to_change"
	PRNotOwned       = "not_owned"
	PRError          = "error"
	PROpened         = "opened"
	PRExisting       = "existing"
	auditRunsOnPR    = "runs_on_pr.opened"
	auditRunsOnPRHad = "runs_on_pr.existing"
)

// RunsOnPRFile is one workflow file the pull request changes.
type RunsOnPRFile struct {
	Path    string
	Changes int
	Diff    string
}

// RunsOnPRPreview is what the pull request of one repository would change.
type RunsOnPRPreview struct {
	RepositoryID int64
	FullName     string
	State        string
	Files        []RunsOnPRFile
	Diff         string
	Skipped      []string
	Message      string
}

// RunsOnPRResult is what opening the pull request of one repository did.
type RunsOnPRResult struct {
	RepositoryID int64
	FullName     string
	State        string
	Number       int
	URL          string
	Message      string
}

// RunsOnPRTarget names one repository, or All opted-in repositories.
type RunsOnPRTarget struct {
	RepositoryID int64
	All          bool
}

func routingRepo(r Repository) routing.Repo {
	return routing.Repo{Installation: r.InstallationID, Owner: r.Owner, Name: r.Name, DefaultBranch: r.DefaultBranch}
}

// runsOnTargets resolves the target to repositories: the one named, or every
// repository whose pool is on and that is not archived.
func (s *Service) runsOnTargets(ctx context.Context, t RunsOnPRTarget) ([]Repository, error) {
	if s.cfg.RunsOnPR == nil {
		return nil, ErrRunsOnPRUnavailable
	}
	if t.All == (t.RepositoryID != 0) {
		return nil, auth.Invalid("name one repository, or ask for all")
	}
	if !t.All {
		r, err := s.cfg.Store.GetRepository(ctx, t.RepositoryID)
		if err != nil {
			return nil, err
		}
		return []Repository{r}, nil
	}
	on := true
	var out []Repository
	after := ""
	for {
		page, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &on, AfterName: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if !r.Archived {
				out = append(out, r)
			}
		}
		if len(page) < 200 {
			return out, nil
		}
		after = page[len(page)-1].FullName
	}
}

// PreviewRunsOnPR shows, for one repository or all opted-in ones, the files
// and the diff of the runs-on change. It writes nothing to GitHub.
func (s *Service) PreviewRunsOnPR(ctx context.Context, p auth.Principal, t RunsOnPRTarget) ([]RunsOnPRPreview, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return nil, err
	}
	rs, err := s.runsOnTargets(ctx, t)
	if err != nil {
		return nil, err
	}
	out := make([]RunsOnPRPreview, 0, len(rs))
	for _, r := range rs {
		pv := RunsOnPRPreview{RepositoryID: r.ID, FullName: r.FullName, Files: []RunsOnPRFile{}, Skipped: []string{}}
		if !s.runsOnOwned(r) {
			pv.State = PRNotOwned
			out = append(out, pv)
			continue
		}
		patch, err := s.cfg.RunsOnPR.PatchFor(ctx, routingRepo(r))
		switch {
		case err != nil:
			if !t.All {
				return nil, err
			}
			pv.State, pv.Message = PRError, err.Error()
		case patch.Empty():
			pv.State = PRNothing
			pv.Skipped = append(pv.Skipped, patch.Skipped...)
		default:
			pv.State, pv.Diff = PRPreviewChange, patch.Diff()
			pv.Skipped = append(pv.Skipped, patch.Skipped...)
			for _, f := range patch.Files {
				one := routing.Patch{Files: []routing.FilePatch{f}}
				pv.Files = append(pv.Files, RunsOnPRFile{Path: f.Path, Changes: len(f.Changes), Diff: one.Diff()})
			}
		}
		out = append(out, pv)
	}
	return out, nil
}

// runsOnOwned says whether crsp may open a pull request on the repository.
// The routing service decides in OpenPR; the preview asks the same question
// by name so the console does not offer a button that must fail.
func (s *Service) runsOnOwned(r Repository) bool {
	o, ok := s.cfg.RunsOnPR.(interface{ CanOpenPR(owner string) bool })
	return !ok || o.CanOpenPR(r.Owner)
}

// OpenRunsOnPR opens the pull request of one repository or of every opted-in
// repository, after an explicit owner action. An open crsp pull request is
// returned, never a second one. Each pull request writes one audit row. The
// GitHub admin permission is checked per repository at the moment of the call;
// for one repository a refusal is the error, for all it is that row's state.
func (s *Service) OpenRunsOnPR(ctx context.Context, p auth.Principal, t RunsOnPRTarget) ([]RunsOnPRResult, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return nil, err
	}
	rs, err := s.runsOnTargets(ctx, t)
	if err != nil {
		return nil, err
	}
	out := make([]RunsOnPRResult, 0, len(rs))
	for _, r := range rs {
		res := RunsOnPRResult{RepositoryID: r.ID, FullName: r.FullName}
		if err := s.requireGitHubAdmin(ctx, p, r); err != nil {
			if !t.All {
				return nil, err
			}
			res.State, res.Message = PRError, err.Error()
			out = append(out, res)
			continue
		}
		pr, err := s.cfg.RunsOnPR.OpenPR(ctx, routingRepo(r))
		switch {
		case errors.Is(err, routing.ErrNotOwned):
			res.State = PRNotOwned
		case err != nil:
			if !t.All {
				return nil, err
			}
			res.State, res.Message = PRError, err.Error()
		case pr.Empty:
			res.State = PRNothing
		default:
			res.Number, res.URL = pr.Number, pr.URL
			action := auditRunsOnPRHad
			res.State = PRExisting
			if pr.Created {
				action, res.State = auditRunsOnPR, PROpened
			}
			s.cfg.Audit.Audit(ctx, p, action, "repository", r.FullName, fmt.Sprintf("pull request #%d %s", pr.Number, pr.URL))
		}
		out = append(out, res)
	}
	return out, nil
}
