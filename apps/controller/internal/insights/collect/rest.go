package collect

import (
	"context"
	"errors"
	"io"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// RESTClient is the part of *github.REST (rest_insights.go) the collector calls.
type RESTClient interface {
	JobLog(ctx context.Context, installationID int64, owner, repo string, jobID int64) (io.ReadCloser, int64, error)
	RunArtifacts(ctx context.Context, installationID int64, owner, repo string, runID int64) ([]github.Artifact, error)
	DownloadArtifact(ctx context.Context, installationID int64, owner, repo string, artifactID int64) (io.ReadCloser, int64, error)
	CommitTree(ctx context.Context, installationID int64, owner, repo, sha string) (string, error)
}

var _ RESTClient = (*github.REST)(nil)

// FromREST makes the GitHub of the collector from the REST client: github.ErrGone becomes ErrGone.
func FromREST(c RESTClient) GitHub { return restGitHub{c} }

type restGitHub struct{ c RESTClient }

func gone(err error) error {
	if errors.Is(err, github.ErrGone) {
		return ErrGone
	}
	return err
}

func (r restGitHub) JobLog(ctx context.Context, inst int64, owner, repo string, jobID int64) (io.ReadCloser, int64, error) {
	rc, n, err := r.c.JobLog(ctx, inst, owner, repo, jobID)
	return rc, n, gone(err)
}

func (r restGitHub) RunArtifacts(ctx context.Context, inst int64, owner, repo string, runID int64) ([]Artifact, error) {
	in, err := r.c.RunArtifacts(ctx, inst, owner, repo, runID)
	out := make([]Artifact, 0, len(in))
	for _, a := range in {
		out = append(out, Artifact{ID: a.ID, Name: a.Name, Size: a.Size, Expired: a.Expired})
	}
	return out, err
}

func (r restGitHub) DownloadArtifact(ctx context.Context, inst int64, owner, repo string, artifactID int64) (io.ReadCloser, int64, error) {
	rc, n, err := r.c.DownloadArtifact(ctx, inst, owner, repo, artifactID)
	return rc, n, gone(err)
}

func (r restGitHub) CommitTree(ctx context.Context, inst int64, owner, repo, sha string) (string, error) {
	return r.c.CommitTree(ctx, inst, owner, repo, sha)
}
