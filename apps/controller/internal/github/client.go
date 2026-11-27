package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Defaults.
const (
	// CallDeadline bounds every REST call (R6.7).
	CallDeadline = 10 * time.Second
	// MaxWriteAttempts is the most tries a write gets (R6.7).
	MaxWriteAttempts = 3
	// tokenRenewBefore renews an installation token this long before expiry (R6.2).
	tokenRenewBefore = 5 * time.Minute
)

// Config builds a Client.
type Config struct {
	APIBase string // default addresses.DefaultGitHubAPIURL
	WebBase string // default addresses.DefaultGitHubURL (OAuth pages)
	HTTP    *http.Client
	Secrets secrets.Store
	Logger  *slog.Logger
	// Now and Sleep are seams for tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// REST is the HTTP implementation of Client.
type REST struct {
	cfg Config

	mu       sync.Mutex
	appID    int64
	clientID string
	key      *rsa.PrivateKey
	tokens   map[int64]cachedToken
	budgets  map[int64]*budget
	runCache map[string]cachedRuns
	jobCache map[string]cachedJobs

	// retries counts retried writes, for tests.
	retries int
}

type cachedToken struct {
	value   string
	expires time.Time
}

// New returns a REST client with no App yet; call UseApp once the App is known.
func New(cfg Config) *REST {
	if cfg.APIBase == "" {
		cfg.APIBase = addresses.DefaultGitHubAPIURL
	}
	if cfg.WebBase == "" {
		cfg.WebBase = addresses.DefaultGitHubURL
	}
	cfg.APIBase = strings.TrimRight(cfg.APIBase, "/")
	cfg.WebBase = strings.TrimRight(cfg.WebBase, "/")
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &REST{cfg: cfg, tokens: map[int64]cachedToken{}, budgets: map[int64]*budget{}}
}

// UseApp tells the client which App it acts as. The private key is read from
// the OS secret store on first use and kept in memory only.
func (c *REST) UseApp(appID int64, clientID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appID, c.clientID, c.key = appID, clientID, nil
	c.tokens = map[int64]cachedToken{}
}

// AppID returns the App in use, or 0.
func (c *REST) AppID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appID
}

var errNoApp = errors.New("github: no App is set up")

func (c *REST) appJWT(ctx context.Context) (string, error) {
	c.mu.Lock()
	appID, key := c.appID, c.key
	c.mu.Unlock()
	if appID == 0 {
		return "", errNoApp
	}
	if key == nil {
		if c.cfg.Secrets == nil {
			return "", secrets.ErrUnavailable
		}
		pemBytes, err := c.cfg.Secrets.Get(ctx, SecretKey(appID, SecretPrivateKey))
		if err != nil {
			return "", fmt.Errorf("app private key: %w", err)
		}
		key, err = ParsePrivateKey(pemBytes)
		if err != nil {
			return "", err
		}
		c.mu.Lock()
		c.key = key
		c.mu.Unlock()
	}
	return SignAppJWT(key, appID, c.cfg.Now())
}

// installationToken returns a cached installation token, renewing it 5
// minutes before expiry (R6.2). It is never written anywhere.
func (c *REST) installationToken(ctx context.Context, id int64) (string, error) {
	c.mu.Lock()
	t, ok := c.tokens[id]
	c.mu.Unlock()
	if ok && c.cfg.Now().Add(tokenRenewBefore).Before(t.expires) {
		return t.value, nil
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return "", err
	}
	if _, err := c.do(ctx, req{method: "POST", path: fmt.Sprintf("/app/installations/%d/access_tokens", id), bearer: jwt, write: true}, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[id] = cachedToken{value: out.Token, expires: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// ForgetInstallationToken drops the cached token of an installation, so the
// next call asks GitHub for a new one that carries the permissions accepted
// since (CRSP139, R5.87). A token keeps the permissions it was made with.
func (c *REST) ForgetInstallationToken(id int64) {
	c.mu.Lock()
	delete(c.tokens, id)
	c.mu.Unlock()
}

type req struct {
	method  string
	path    string // under APIBase, or a full URL when abs is set
	abs     bool
	query   url.Values
	body    any
	bearer  string
	etag    string
	write   bool
	headers map[string]string
	// installation, when set, charges the call to that installation's budget.
	installation int64
}

type response struct {
	status int
	etag   string
	header http.Header
	next   string // Link rel=next
}

// do sends one request. Writes retry up to MaxWriteAttempts times (R6.7).
func (c *REST) do(ctx context.Context, r req, out any) (response, error) {
	attempts := 1
	if r.write {
		attempts = MaxWriteAttempts
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		resp, wait, err := c.once(ctx, r, out)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if i == attempts-1 || !retryable(err) {
			break
		}
		c.mu.Lock()
		c.retries++
		c.mu.Unlock()
		if wait == 0 {
			wait = time.Duration(1<<i)*250*time.Millisecond + time.Duration(rand.Int64N(int64(100*time.Millisecond)))
		}
		if err := c.cfg.Sleep(ctx, wait); err != nil {
			return resp, err
		}
	}
	return response{}, lastErr
}

func retryable(err error) bool {
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	var rl *RateLimitError
	return errors.As(err, &rl) && rl.Secondary
}

func (c *REST) once(ctx context.Context, r req, out any) (response, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, CallDeadline)
	defer cancel()

	target := r.path
	if !r.abs {
		target = c.cfg.APIBase + r.path
	}
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return response{}, 0, err
		}
		body = bytes.NewReader(b)
	}
	hr, err := http.NewRequestWithContext(ctx, r.method, target, body)
	if err != nil {
		return response{}, 0, err
	}
	hr.Header.Set("Accept", "application/vnd.github+json")
	hr.Header.Set("X-GitHub-Api-Version", APIVersion)
	hr.Header.Set("User-Agent", "crsp")
	if r.body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.bearer != "" {
		hr.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.etag != "" {
		hr.Header.Set("If-None-Match", r.etag)
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	if err := c.checkBudget(ctx, r.installation); err != nil {
		return response{}, 0, err
	}
	res, err := c.cfg.HTTP.Do(hr)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return response{}, 0, ctx.Err()
		}
		return response{}, 0, fmt.Errorf("%w: %v", ErrUnavailable, scrubURLError(err))
	}
	defer func() { _ = res.Body.Close() }()
	c.noteBudget(r.installation, res.Header)
	resp := response{status: res.StatusCode, etag: res.Header.Get("ETag"), header: res.Header, next: nextLink(res.Header.Get("Link"))}

	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return resp, 0, fmt.Errorf("%w: reading answer", ErrUnavailable)
	}
	switch {
	case res.StatusCode == http.StatusNotModified:
		return resp, 0, nil
	case res.StatusCode >= 200 && res.StatusCode < 300:
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp, 0, fmt.Errorf("github: unreadable answer: %w", err)
			}
		}
		return resp, 0, nil
	}
	return resp, retryAfter(res.Header, c.cfg.Now()), c.classify(res, data)
}

// classify turns an error status into one of the package errors.
func (c *REST) classify(res *http.Response, data []byte) error {
	var msg struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &msg)
	text := strings.ToLower(msg.Message)

	if res.StatusCode == http.StatusTooManyRequests ||
		(res.StatusCode == http.StatusForbidden && (res.Header.Get("X-RateLimit-Remaining") == "0" ||
			res.Header.Get("Retry-After") != "" || strings.Contains(text, "rate limit"))) {
		reset := c.cfg.Now().Add(time.Minute)
		if v := res.Header.Get("X-RateLimit-Reset"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				reset = time.Unix(n, 0)
			}
		}
		if d := retryAfter(res.Header, c.cfg.Now()); d > 0 {
			reset = c.cfg.Now().Add(d)
		}
		return &RateLimitError{ResetAt: reset, Secondary: res.Header.Get("X-RateLimit-Remaining") != "0"}
	}
	switch {
	case res.StatusCode == http.StatusUnprocessableEntity && res.Request.Method == http.MethodDelete && strings.Contains(res.Request.URL.Path, "/actions/runners/"):
		// GitHub refuses to delete a runner that has a job (R5.32, runners R5.62).
		return fmt.Errorf("%w: %s", ErrRunnerBusy, res.Request.URL.Path)
	case res.StatusCode == http.StatusNotFound:
		return &APIError{Status: res.StatusCode, Message: msg.Message, Err: fmt.Errorf("%w: %s", ErrNotFound, res.Request.URL.Path)}
	case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized:
		return &APIError{Status: res.StatusCode, Message: msg.Message, Err: fmt.Errorf("%w: %s", ErrForbidden, msg.Message)}
	case res.StatusCode >= 500:
		return &APIError{Status: res.StatusCode, Message: msg.Message, Err: fmt.Errorf("%w: status %d", ErrUnavailable, res.StatusCode)}
	}
	return &APIError{Status: res.StatusCode, Message: msg.Message, Err: fmt.Errorf("github: status %d: %s", res.StatusCode, msg.Message)}
}

func retryAfter(h http.Header, now time.Time) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		d := time.Duration(n) * time.Second
		if d > 30*time.Second {
			// A long wait is not retried in the call; the caller sees the limit.
			return 0
		}
		return d
	}
	return 0
}

// scrubURLError drops the URL from a transport error, so no query string
// reaches a log.
func scrubURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		segs := strings.Split(strings.TrimSpace(part), ";")
		if len(segs) < 2 || strings.TrimSpace(segs[1]) != `rel="next"` {
			continue
		}
		return strings.Trim(strings.TrimSpace(segs[0]), "<>")
	}
	return ""
}

// getAll follows Link rel=next and calls each for every page's raw JSON.
func (c *REST) getAll(ctx context.Context, r req, each func(raw json.RawMessage) error) error {
	r.method = "GET"
	if r.query == nil {
		r.query = url.Values{}
	}
	if r.query.Get("per_page") == "" {
		r.query.Set("per_page", "100")
	}
	for pages := 0; pages < 1000; pages++ {
		var raw json.RawMessage
		resp, err := c.do(ctx, r, &raw)
		if err != nil {
			return err
		}
		if err := each(raw); err != nil {
			return err
		}
		if resp.next == "" {
			return nil
		}
		r = req{method: "GET", path: resp.next, abs: true, bearer: r.bearer, installation: r.installation}
	}
	return errors.New("github: too many pages")
}

// asApp, asUser and asInstallation fill the bearer.
func (c *REST) withInstallation(ctx context.Context, id int64, r req) (req, error) {
	tok, err := c.installationToken(ctx, id)
	if err != nil {
		return r, err
	}
	r.bearer = tok
	r.installation = id
	return r, nil
}

// APIBase is the REST address in use (the scale-set client registers there).
func (c *REST) APIBase() string {
	if c.cfg.APIBase != "" {
		return c.cfg.APIBase
	}
	return addresses.DefaultGitHubAPIURL
}

// WebBase is the web address in use.
func (c *REST) WebBase() string {
	if c.cfg.WebBase != "" {
		return c.cfg.WebBase
	}
	return addresses.DefaultGitHubURL
}
