package sys

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// HTTPFetcher downloads over HTTPS.
type HTTPFetcher struct {
	Client *http.Client
}

// Fetch writes the body of url to dest.
func (f HTTPFetcher) Fetch(ctx context.Context, url, dest string) error {
	c := f.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // dest is the agent's own download path.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
