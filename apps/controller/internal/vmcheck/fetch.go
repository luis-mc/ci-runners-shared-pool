package vmcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// HTTPFetcher reads over HTTPS. The image download has no overall timeout of
// its own: Boot's context ends it.
type HTTPFetcher struct{ Client *http.Client }

func (h HTTPFetcher) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

// Get implements Fetcher.
func (h HTTPFetcher) Get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req) //nolint:gosec // URLs come from the catalogue
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return resp.Body, nil
}

// Text implements Fetcher.
func (h HTTPFetcher) Text(ctx context.Context, url string) (string, error) {
	rc, err := h.Get(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	return string(b), err
}

// RealDeps is the Deps of a runner: the network, /dev/kvm and exec.
func RealDeps() Deps {
	return Deps{
		Fetch: HTTPFetcher{},
		KVMUsable: func() bool {
			f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
			if err != nil {
				return false
			}
			_ = f.Close()
			return true
		},
		Run: func(ctx context.Context, name string, args ...string) error {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed program names
			if err != nil {
				return fmt.Errorf("%w: %s", err, firstLineOf(string(out)))
			}
			return nil
		},
		Start: func(ctx context.Context, name string, args ...string) (func() error, error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed program names
			cmd.WaitDelay = 5 * time.Second
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd.Wait, nil
		},
		FreeMemMB: RealFreeMemMB,
		Sleep: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		},
	}
}

func firstLineOf(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

// RealGuestDeps is the GuestDeps of a runner: RealDeps plus ssh/scp through exec.
func RealGuestDeps() GuestDeps {
	return GuestDeps{
		Deps: RealDeps(),
		Exec: func(ctx context.Context, out io.Writer, name string, args ...string) (int, error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // ssh and scp, fixed names
			cmd.Stdout, cmd.Stderr = out, out
			cmd.WaitDelay = 5 * time.Second
			err := cmd.Run()
			var ee *exec.ExitError
			switch {
			case err == nil:
				return 0, nil
			case ctx.Err() != nil:
				return -1, ctx.Err()
			case errors.As(err, &ee):
				return ee.ExitCode(), nil
			}
			return -1, err
		},
		FreePort: func() (int, error) {
			l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
			if err != nil {
				return 0, err
			}
			defer func() { _ = l.Close() }()
			return l.Addr().(*net.TCPAddr).Port, nil
		},
	}
}
