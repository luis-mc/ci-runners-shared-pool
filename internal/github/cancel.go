package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ErrCancelNeedsWrite says the App may not cancel runs: cancelling a run needs
// the Actions permission at write level, which the App is made without today.
var ErrCancelNeedsWrite = errors.New("github refused the cancel: the App needs the Actions permission at write level")

// CancelWorkflowRun cancels a whole run: POST /repos/{owner}/{repo}/actions/runs/{id}/cancel
// (scheduling R5.104). GitHub has no per-job cancel. A run that is already
// completed answers 409; there is nothing left to cancel, so that is no error.
func (c *REST) CancelWorkflowRun(ctx context.Context, installationID int64, owner, repo string, runID int64) error {
	r, err := c.withInstallation(ctx, installationID, req{method: http.MethodPost, write: true,
		path: fmt.Sprintf("/repos/%s/%s/actions/runs/%d/cancel", url.PathEscape(owner), url.PathEscape(repo), runID)})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	var api *APIError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &api) && api.Status == http.StatusConflict:
		return nil
	case errors.Is(err, ErrForbidden):
		return fmt.Errorf("%w: %v", ErrCancelNeedsWrite, err)
	}
	return err
}
