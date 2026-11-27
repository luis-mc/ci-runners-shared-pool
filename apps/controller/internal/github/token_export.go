package github

import "context"

// InstallationToken returns a current installation token for read-only calls
// made outside this package (the speed comparison, observability R5.34). The
// caller uses it for one request and drops it; it is never stored or logged.
func (c *REST) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	return c.installationToken(ctx, installationID)
}
