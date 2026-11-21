package toolstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HTTPFetcher is the real Fetcher. It is only compiled and vetted in tests (R5.119): a test never calls it.
type HTTPFetcher struct{ Client *http.Client }

var _ Fetcher = HTTPFetcher{}

func (h HTTPFetcher) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func (h HTTPFetcher) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// Download implements Fetcher.
func (h HTTPFetcher) Download(ctx context.Context, url, file string) (string, error) {
	resp, err := h.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return resp.Request.URL.String(), nil
}

// Text implements Fetcher.
func (h HTTPFetcher) Text(ctx context.Context, url string) (string, error) {
	resp, err := h.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(b), err
}

// ExecRunner is the real Runner: it runs the manager as the agent's admin identity with the recipe's
// environment added to the agent's own. Only compiled and vetted in tests.
type ExecRunner struct{}

var _ Runner = ExecRunner{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, argv []string, env map[string]string, dir string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 600 {
			tail = tail[len(tail)-600:]
		}
		return fmt.Errorf("%w: %s", err, tail)
	}
	return nil
}
