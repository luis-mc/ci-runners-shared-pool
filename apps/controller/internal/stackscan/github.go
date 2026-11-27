package stackscan

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Client is the part of *github.REST the scan uses.
type Client interface {
	ListBlobsAt(ctx context.Context, installationID int64, owner, repo, ref string) ([]github.TreeBlob, error)
	GetFileAt(ctx context.Context, installationID int64, owner, repo, filePath, ref string) ([]byte, error)
}

// Target finds the installation id of a repository (owner/name).
type Target func(ctx context.Context, repository string) (installationID int64, err error)

// GitHub is the Reader over the GitHub adapter. Every call is marked as
// background work, so a low rate budget delays the scan (R5.75, R6.6).
type GitHub struct {
	C      Client
	Target Target
}

var _ Reader = GitHub{}

func split(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("stackscan: repository %q is not owner/name", repository)
	}
	return owner, name, nil
}

// Blobs lists the commit's files.
func (g GitHub) Blobs(ctx context.Context, repository, commit string) ([]Blob, error) {
	owner, name, err := split(repository)
	if err != nil {
		return nil, err
	}
	inst, err := g.Target(ctx, repository)
	if err != nil {
		return nil, err
	}
	tb, err := g.C.ListBlobsAt(github.Background(ctx), inst, owner, name, commit)
	if err != nil {
		return nil, err
	}
	out := make([]Blob, len(tb))
	for i, b := range tb {
		out[i] = Blob{Path: b.Path, SHA: b.SHA, Size: b.Size}
	}
	return out, nil
}

// File reads one file at the commit.
func (g GitHub) File(ctx context.Context, repository, filePath, commit string) ([]byte, error) {
	owner, name, err := split(repository)
	if err != nil {
		return nil, err
	}
	inst, err := g.Target(ctx, repository)
	if err != nil {
		return nil, err
	}
	return g.C.GetFileAt(github.Background(ctx), inst, owner, name, filePath, commit)
}
