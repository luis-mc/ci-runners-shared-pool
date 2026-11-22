package assign

import (
	"context"
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// GitHubJobs is the part of github.InstallationClient the poller reads.
type GitHubJobs interface {
	ListOpenJobs(ctx context.Context, installationID int64, owner, repo string) ([]github.Job, error)
}

// ClientSource is the Source over the GitHub client. Its reads are the
// ETag-conditional ones of github.REST (a 304 costs no rate limit).
type ClientSource struct {
	Client GitHubJobs
	List   func(ctx context.Context) ([]github.PollTarget, error)
}

// Targets implements Source.
func (s ClientSource) Targets(ctx context.Context) ([]github.PollTarget, error) { return s.List(ctx) }

// OpenJobs implements Source.
func (s ClientSource) OpenJobs(ctx context.Context, t github.PollTarget) ([]github.Job, error) {
	return s.Client.ListOpenJobs(ctx, t.InstallationID, t.Owner, t.Repo)
}

// GitHubRunners is the part of the client the purge uses.
type GitHubRunners interface {
	ListRunners(ctx context.Context, installationID int64, scope github.RunnerScope) ([]github.Runner, error)
	DeleteRunner(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64) error
}

// Purger deletes a runner by name at GitHub, for a start that crashed between
// the JIT call and its record. A runner that has a job is never deleted.
type Purger struct {
	Client   GitHubRunners
	Env      Env
	Resolver runners.Resolver
}

// Purge implements Controller.Purge.
func (p Purger) Purge(ctx context.Context, repository, name string) error {
	repo, ok, err := p.Env.Repo(ctx, repository)
	if err != nil || !ok {
		return err
	}
	t, err := p.Resolver.Resolve(ctx, repo.Scope)
	if err != nil {
		return err
	}
	list, err := p.Client.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return err // an unreadable list deletes nothing
	}
	for _, r := range list {
		if !strings.EqualFold(r.Name, name) {
			continue
		}
		if r.Busy {
			return runners.ErrRunnerBusy
		}
		if err := p.Client.DeleteRunner(ctx, t.InstallationID, t.Scope, r.ID); err != nil && !errors.Is(err, github.ErrNotFound) {
			return err
		}
	}
	return nil
}
