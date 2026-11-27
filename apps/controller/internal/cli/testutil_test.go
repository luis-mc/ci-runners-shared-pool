package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/contexts"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

const canary = "crsp_canary_TOKEN_0123456789"

// memSecrets is an in-memory secrets.Store (decision 0002: tests use a fake).
type memSecrets struct {
	mu sync.Mutex
	m  map[secrets.Key][]byte
}

func (s *memSecrets) Get(_ context.Context, k secrets.Key) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return v, nil
}
func (s *memSecrets) Put(_ context.Context, k secrets.Key, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[secrets.Key][]byte{}
	}
	s.m[k] = v
	return nil
}
func (s *memSecrets) Delete(_ context.Context, k secrets.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}
func (s *memSecrets) Backend() string { return "memory" }

// fixture is a fake controller (an httptest TLS server) with a CLI signed in.
type fixture struct {
	t        *testing.T
	srv      *httptest.Server
	routes   map[string]http.HandlerFunc
	mu       sync.Mutex
	calls    []string
	dir      string
	secrets  *memSecrets
	terminal bool
	stdin    string
	opened   []string
	now      time.Time
	ctx      context.Context
	cancel   context.CancelFunc
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, routes: map[string]http.HandlerFunc{}, dir: t.TempDir(), secrets: &memSecrets{}, now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	t.Cleanup(f.cancel)
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		f.mu.Lock()
		h := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			writeJSON(w, 404, apiErr("not_found", "no such route in the fake controller"))
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	cf := &contexts.File{Contexts: map[string]contexts.Context{}}
	cf.Set(contexts.Context{Name: "home", URL: f.srv.URL, Fingerprint: f.fingerprint()})
	if err := cf.Save(f.dir); err != nil {
		t.Fatal(err)
	}
	tok, _ := json.Marshal(storedToken{Token: canary, ID: "tok1"})
	_ = f.secrets.Put(context.Background(), tokenKey("home"), tok)
	return f
}

func (f *fixture) fingerprint() string {
	sum := sha256.Sum256(f.srv.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

// handle registers a handler for "METHOD /path".
func (f *fixture) handle(pattern string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[pattern] = h
}

// reply registers a fixed JSON answer.
func (f *fixture) reply(pattern string, status int, body any) {
	f.handle(pattern, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, status, body)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func apiErr(code, msg string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code, "message": msg}}
}

// called reports whether a request line was seen.
func (f *fixture) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fixture) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fixture) env(out, errOut *bytes.Buffer) *Env {
	return &Env{
		Stdin:      strings.NewReader(f.stdin),
		Stdout:     out,
		Stderr:     errOut,
		Getenv:     func(string) string { return "" },
		IsTerminal: func() bool { return f.terminal },
		ConfigDir:  f.dir,
		Secrets:    f.secrets,
		OpenBrowser: func(u string) error {
			f.opened = append(f.opened, u)
			return nil
		},
		// The clock moves one minute per reading so timeouts pass quickly.
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.now = f.now.Add(time.Minute)
			return f.now
		},
		PollEvery: time.Millisecond,
		Version:   "test",
		GoVersion: "go-test",
		Context:   f.ctx,
	}
}

// run executes the CLI and returns exit code, stdout and stderr.
func (f *fixture) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(f.env(&out, &errOut), args)
	return code, out.String(), errOut.String()
}

// cancelOnWrite is a stdout that cancels the fixture's context the moment the
// output holds marker: "Ctrl-C after the CLI printed this". The test waits on
// what the CLI did, not on the clock.
type cancelOnWrite struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	marker string
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.marker) {
		w.cancel()
	}
	return n, err
}

func (w *cancelOnWrite) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// runUntilPrinted runs the CLI and presses Ctrl-C once stdout holds marker.
func (f *fixture) runUntilPrinted(marker string, args ...string) (int, string, string) {
	var errOut bytes.Buffer
	out := &cancelOnWrite{marker: marker, cancel: f.cancel}
	env := f.env(&bytes.Buffer{}, &errOut)
	env.Stdout = out
	code := Run(env, args)
	return code, out.String(), errOut.String()
}

type flagT = flag.Flag

func newFlagSetForTest() *flag.FlagSet {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
