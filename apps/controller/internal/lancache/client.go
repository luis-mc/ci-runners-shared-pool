package lancache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// downFor is how long a peer that failed is skipped before it is tried again.
const downFor = 30 * time.Second

// Client reads and writes the cache of one holder. Any failure is a miss for
// the caller, who then takes its normal path (local cache or fresh download).
type Client struct {
	base       string
	machineID  string
	credential string
	http       *http.Client
	now        func() time.Time

	mu        sync.Mutex
	downUntil time.Time
}

// NewClient makes a client. hc should carry tlsca.ClientTLS(pin) and a timeout.
func NewClient(base, machineID, credential string, hc *http.Client, now func() time.Time) *Client {
	if now == nil {
		now = time.Now
	}
	return &Client{base: strings.TrimRight(base, "/"), machineID: machineID, credential: credential, http: hc, now: now}
}

func (c *Client) isDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().Before(c.downUntil)
}

func (c *Client) markDown() {
	c.mu.Lock()
	c.downUntil = c.now().Add(downFor)
	c.mu.Unlock()
}

func (c *Client) url(ns, scope, name string) (string, error) {
	if _, err := Name(ns, scope, name); err != nil {
		return "", err
	}
	segs := strings.Split(name, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return c.base + pathPrefix + ns + "/" + url.PathEscape(scope) + "/" + strings.Join(segs, "/"), nil
}

func (c *Client) do(ctx context.Context, method, u string, body io.Reader, digest string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set(HeaderMachineID, c.machineID)
	if digest != "" {
		req.Header.Set(HeaderDigest, digest)
	}
	return c.http.Do(req)
}

// Fetch returns the bytes of an entry. want is the digest the caller expects
// (a tool pin); when empty the digest the holder names is used. It returns
// ErrMiss when the holder is down, has no entry or sends bytes that do not
// match: the caller falls back. The bytes are checked before they are returned.
func (c *Client) Fetch(ctx context.Context, ns, scope, name, want string) ([]byte, error) {
	if c.isDown() {
		return nil, ErrMiss
	}
	u, err := c.url(ns, scope, name)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		c.markDown()
		return nil, ErrMiss
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrMiss
	}
	if resp.StatusCode != http.StatusOK {
		c.markDown()
		return nil, ErrMiss
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.markDown()
		return nil, ErrMiss
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if want == "" {
		want = resp.Header.Get(HeaderDigest)
	}
	if want == "" || !strings.EqualFold(got, want) {
		return nil, ErrMiss
	}
	return data, nil
}

// Store sends bytes to the holder. A failure is returned, and the caller
// ignores it: sharing is best effort.
func (c *Client) Store(ctx context.Context, ns, scope, name string, data []byte) error {
	if c.isDown() {
		return ErrMiss
	}
	u, err := c.url(ns, scope, name)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	resp, err := c.do(ctx, http.MethodPut, u, strings.NewReader(string(data)), hex.EncodeToString(sum[:]))
	if err != nil {
		c.markDown()
		return fmt.Errorf("send to the LAN cache: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("the LAN cache refused the entry: %s", resp.Status)
	}
	return nil
}

// FetchOrFallback reads from the LAN cache and, on any miss or failure, calls
// fallback (the normal path). It reports whether the LAN cache served it.
func FetchOrFallback(ctx context.Context, c *Client, ns, scope, name, want string, fallback func(context.Context) ([]byte, error)) ([]byte, bool, error) {
	if c != nil {
		data, err := c.Fetch(ctx, ns, scope, name, want)
		if err == nil {
			return data, true, nil
		}
		if !errors.Is(err, ErrMiss) && ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
	}
	data, err := fallback(ctx)
	return data, false, err
}
