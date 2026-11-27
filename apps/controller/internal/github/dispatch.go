package github

import (
	"context"
	"net/url"
)

// DispatchWorkflow starts a workflow that has a workflow_dispatch trigger
// (github-integration R5.66, the shadow trial). inputs may be empty.
func (c *REST) DispatchWorkflow(ctx context.Context, id int64, owner, repo, workflow, ref string, inputs map[string]string) error {
	body := map[string]any{"ref": ref}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/workflows/" + url.PathEscape(workflow) + "/dispatches"
	r, err := c.installationReq(ctx, id, req{method: "POST", path: path, write: true, body: body})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}
