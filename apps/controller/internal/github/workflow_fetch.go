package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// GetRunWorkflowPath reads the workflow file path of one run
// (GET /repos/{owner}/{repo}/actions/runs/{id}), for a job that arrived by
// webhook, which does not carry it. One call, charged to the installation.
func (c *REST) GetRunWorkflowPath(ctx context.Context, installationID int64, owner, repo string, runID int64) (string, error) {
	r, err := c.installationReq(ctx, installationID, req{method: "GET",
		path: fmt.Sprintf("/repos/%s/%s/actions/runs/%d", url.PathEscape(owner), url.PathEscape(repo), runID)})
	if err != nil {
		return "", err
	}
	var run apiRun
	if _, err := c.do(ctx, r, &run); err != nil {
		return "", err
	}
	return workflowFilePath(run.Path), nil
}

// GetFileAt reads one file at a commit with the contents API
// (GET /repos/{owner}/{repo}/contents/{path}?ref={sha}). The shared REST client
// counts it and backs off on a rate limit like every other call.
func (c *REST) GetFileAt(ctx context.Context, installationID int64, owner, repo, filePath, ref string) ([]byte, error) {
	segs := strings.Split(filePath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	r, err := c.installationReq(ctx, installationID, req{method: "GET",
		path:  "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/contents/" + strings.Join(segs, "/"),
		query: url.Values{"ref": {ref}}})
	if err != nil {
		return nil, err
	}
	var f struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if _, err := c.do(ctx, r, &f); err != nil {
		return nil, err
	}
	if f.Encoding != "base64" {
		return nil, fmt.Errorf("github: %s: unexpected encoding %q", filePath, f.Encoding)
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
}
