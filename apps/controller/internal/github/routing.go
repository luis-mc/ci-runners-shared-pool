package github

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Runner labels and the one-time workflow pull request (github-integration
// R5.101 to R5.108). They sit beside InstallationClient as small interfaces so
// a fake in a test needs only these methods.

// RunnerLabeler adds and removes custom labels of a runner that is already
// registered. A runner made with a JIT config gets its labels at creation and
// is read-only afterwards, so only persistent runners use this.
type RunnerLabeler interface {
	AddRunnerLabels(ctx context.Context, installationID int64, scope RunnerScope, runnerID int64, labels []string) error
	RemoveRunnerLabel(ctx context.Context, installationID int64, scope RunnerScope, runnerID int64, label string) error
}

// PullRequestFile is a file the pull request writes.
type PullRequestFile struct {
	Path    string
	Content []byte
}

// PullRequestRequest asks for one pull request that changes some files.
type PullRequestRequest struct {
	Branch string // the head branch to make, or to update when it exists
	Base   string // the base branch; empty means the default branch
	Title  string
	Body   string
	// Message is the commit message of each file.
	Message string
	Files   []PullRequestFile
}

// PullRequest is the pull request GitHub made or found.
type PullRequest struct {
	Number  int
	URL     string
	Created bool // false when an open pull request of the branch already existed
}

// PullRequester opens a pull request and reads a branch head.
type PullRequester interface {
	BranchHead(ctx context.Context, installationID int64, owner, repo, branch string) (sha string, err error)
	OpenPullRequest(ctx context.Context, installationID int64, owner, repo string, pr PullRequestRequest) (PullRequest, error)
}

var (
	_ RunnerLabeler = (*REST)(nil)
	_ PullRequester = (*REST)(nil)
)

func runnerPath(scope RunnerScope, runnerID int64) string {
	return fmt.Sprintf("%s/actions/runners/%d/labels", scopePath(scope), runnerID)
}

// AddRunnerLabels adds custom labels to a runner (POST .../labels). GitHub
// fails the add that would pass 100 labels with HTTP 500, so the caller keeps
// the count (routing.LabelCap).
func (c *REST) AddRunnerLabels(ctx context.Context, id int64, scope RunnerScope, runnerID int64, labels []string) error {
	r, err := c.installationReq(ctx, id, req{method: "POST", path: runnerPath(scope, runnerID), write: true, body: map[string][]string{"labels": labels}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}

// RemoveRunnerLabel removes one custom label (DELETE .../labels/{name}). A
// label or runner that is already gone is not an error.
func (c *REST) RemoveRunnerLabel(ctx context.Context, id int64, scope RunnerScope, runnerID int64, label string) error {
	r, err := c.installationReq(ctx, id, req{method: "DELETE", path: runnerPath(scope, runnerID) + "/" + url.PathEscape(label), write: true})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// BranchHead reads the commit a branch points at.
func (c *REST) BranchHead(ctx context.Context, id int64, owner, repo, branch string) (string, error) {
	r, err := c.installationReq(ctx, id, req{path: repoPath(owner, repo) + "/git/ref/heads/" + escapeRef(branch)})
	if err != nil {
		return "", err
	}
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := c.do(ctx, r, &out); err != nil {
		return "", err
	}
	return out.Object.SHA, nil
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// OpenPullRequest makes the head branch from the base (when it is not there),
// writes each file on it and opens the pull request. When an open pull request
// of the branch exists it is returned and no second one is opened; the files
// are written again so the branch keeps the newest patch.
func (c *REST) OpenPullRequest(ctx context.Context, id int64, owner, repo string, pr PullRequestRequest) (PullRequest, error) {
	base := pr.Base
	if base == "" {
		r, err := c.installationReq(ctx, id, req{path: repoPath(owner, repo)})
		if err != nil {
			return PullRequest{}, err
		}
		var info struct {
			DefaultBranch string `json:"default_branch"`
		}
		if _, err := c.do(ctx, r, &info); err != nil {
			return PullRequest{}, err
		}
		base = info.DefaultBranch
	}
	head, err := c.BranchHead(ctx, id, owner, repo, pr.Branch)
	switch {
	case errors.Is(err, ErrNotFound):
		sha, err := c.BranchHead(ctx, id, owner, repo, base)
		if err != nil {
			return PullRequest{}, err
		}
		r, err := c.installationReq(ctx, id, req{method: "POST", path: repoPath(owner, repo) + "/git/refs", write: true,
			body: map[string]string{"ref": "refs/heads/" + pr.Branch, "sha": sha}})
		if err != nil {
			return PullRequest{}, err
		}
		if _, err := c.do(ctx, r, nil); err != nil {
			return PullRequest{}, err
		}
	case err != nil:
		return PullRequest{}, err
	default:
		_ = head
	}
	for _, f := range pr.Files {
		if err := c.putFile(ctx, id, owner, repo, pr.Branch, pr.Message, f); err != nil {
			return PullRequest{}, err
		}
	}
	// An open pull request of this branch is the one pull request.
	r, err := c.installationReq(ctx, id, req{path: repoPath(owner, repo) + "/pulls",
		query: url.Values{"state": {"open"}, "head": {owner + ":" + pr.Branch}}})
	if err != nil {
		return PullRequest{}, err
	}
	var open []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if _, err := c.do(ctx, r, &open); err != nil {
		return PullRequest{}, err
	}
	if len(open) > 0 {
		return PullRequest{Number: open[0].Number, URL: open[0].HTMLURL}, nil
	}
	r, err = c.installationReq(ctx, id, req{method: "POST", path: repoPath(owner, repo) + "/pulls", write: true,
		body: map[string]any{"title": pr.Title, "body": pr.Body, "head": pr.Branch, "base": base}})
	if err != nil {
		return PullRequest{}, err
	}
	var made struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if _, err := c.do(ctx, r, &made); err != nil {
		return PullRequest{}, err
	}
	return PullRequest{Number: made.Number, URL: made.HTMLURL, Created: true}, nil
}

func (c *REST) putFile(ctx context.Context, id int64, owner, repo, branch, message string, f PullRequestFile) error {
	path := repoPath(owner, repo) + "/contents/" + escapeRef(f.Path)
	body := map[string]string{"message": message, "branch": branch, "content": base64.StdEncoding.EncodeToString(f.Content)}
	r, err := c.installationReq(ctx, id, req{path: path, query: url.Values{"ref": {branch}}})
	if err != nil {
		return err
	}
	var cur struct {
		SHA string `json:"sha"`
	}
	switch _, err := c.do(ctx, r, &cur); {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	default:
		body["sha"] = cur.SHA
	}
	r, err = c.installationReq(ctx, id, req{method: "PUT", path: path, write: true, body: body})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}
