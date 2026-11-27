package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DownloadDeadline bounds one log or artifact download, header to last byte (ci-run-insights R5.2, R5.3).
const DownloadDeadline = 5 * time.Minute

// ErrGone is returned when GitHub no longer has the log or the artifact (404 or 410). internal/insights/collect
// maps it to its own ErrGone (that package cannot be imported here: it reaches the store, which
// imports this package).
var ErrGone = errors.New("github: no longer has it")

// Artifact is one artifact of a workflow run.
type Artifact struct {
	ID      int64
	Name    string
	Size    int64
	Expired bool
}

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// JobLog opens the plain-text log of one job (GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs). GitHub
// answers with a redirect to a short-lived address, which is followed without the installation token. size is
// -1 when GitHub did not say. A log GitHub no longer has (404, 410) is ErrGone. It is a background
// call (github-integration R6.6, ci-run-insights R6.1).
func (c *REST) JobLog(ctx context.Context, installationID int64, owner, repo string, jobID int64) (io.ReadCloser, int64, error) {
	return c.download(ctx, installationID, fmt.Sprintf("%s/actions/jobs/%d/logs", repoPath(owner, repo), jobID))
}

// DownloadArtifact opens the zip of one artifact (GET .../actions/artifacts/{id}/zip), followed through the
// redirect like a log. An expired or deleted artifact is ErrGone.
func (c *REST) DownloadArtifact(ctx context.Context, installationID int64, owner, repo string, artifactID int64) (io.ReadCloser, int64, error) {
	return c.download(ctx, installationID, fmt.Sprintf("%s/actions/artifacts/%d/zip", repoPath(owner, repo), artifactID))
}

// RunArtifacts lists the artifacts of one run (GET .../actions/runs/{run_id}/artifacts), every page.
func (c *REST) RunArtifacts(ctx context.Context, installationID int64, owner, repo string, runID int64) ([]Artifact, error) {
	ctx = Background(ctx)
	r, err := c.withInstallation(ctx, installationID, req{path: fmt.Sprintf("%s/actions/runs/%d/artifacts", repoPath(owner, repo), runID)})
	if err != nil {
		return nil, err
	}
	var all []Artifact
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Artifacts []struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				Size    int64  `json:"size_in_bytes"`
				Expired bool   `json:"expired"`
			} `json:"artifacts"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, a := range page.Artifacts {
			all = append(all, Artifact{ID: a.ID, Name: a.Name, Size: a.Size, Expired: a.Expired})
		}
		return nil
	})
	return all, err
}

// CommitTree returns the tree sha of a commit (GET .../git/commits/{sha}), for ci-run-insights R5.26.
func (c *REST) CommitTree(ctx context.Context, installationID int64, owner, repo, sha string) (string, error) {
	ctx = Background(ctx)
	r, err := c.withInstallation(ctx, installationID, req{method: "GET", path: fmt.Sprintf("%s/git/commits/%s", repoPath(owner, repo), url.PathEscape(sha))})
	if err != nil {
		return "", err
	}
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := c.do(ctx, r, &out); err != nil {
		return "", err
	}
	if out.Tree.SHA == "" {
		return "", fmt.Errorf("github: commit %s has no tree", sha)
	}
	return out.Tree.SHA, nil
}

// download opens a body that can be large, so it is streamed and not read into memory like do does. The
// answer is not JSON: 200 is the body, 404 and 410 are ErrGone, anything else is classified like
// every other call (rate limit, forbidden, unavailable).
func (c *REST) download(ctx context.Context, installationID int64, path string) (io.ReadCloser, int64, error) {
	ctx = Background(ctx)
	tok, err := c.installationToken(ctx, installationID)
	if err != nil {
		return nil, 0, err
	}
	if err := c.checkBudget(ctx, installationID); err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, DownloadDeadline)
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.APIBase+path, nil)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	hr.Header.Set("Accept", "application/vnd.github+json")
	hr.Header.Set("X-GitHub-Api-Version", APIVersion)
	hr.Header.Set("User-Agent", "crsp")
	hr.Header.Set("Authorization", "Bearer "+tok)
	res, err := c.noTokenOnRedirect().Do(hr)
	if err != nil {
		cancel()
		if ctx.Err() != nil && ctx.Err() != context.DeadlineExceeded {
			return nil, 0, ctx.Err()
		}
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, scrubURLError(err))
	}
	c.noteBudget(installationID, res.Header)
	switch res.StatusCode {
	case http.StatusOK:
		return &cancelBody{ReadCloser: res.Body, cancel: cancel}, res.ContentLength, nil
	case http.StatusNotFound, http.StatusGone:
		_ = res.Body.Close()
		cancel()
		return nil, 0, ErrGone
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	_ = res.Body.Close()
	cancel()
	return nil, 0, c.classify(res, data)
}

// noTokenOnRedirect is the HTTP client with a rule that the installation token never goes to the address a
// log or an artifact is redirected to (a short-lived signed address of another host). Go drops the header
// across hosts already; this drops it always, so the rule does not depend on the host name.
func (c *REST) noTokenOnRedirect() *http.Client {
	cl := *c.cfg.HTTP
	prev := cl.CheckRedirect
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cl
}

// cancelBody ends the download deadline when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
