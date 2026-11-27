package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/packages"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// realController builds the controller the way runController does, over a
// temporary data directory and an in-memory secret store, and serves its API.
func realController(t *testing.T) (*controller, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ListenAddr: "127.0.0.1:8460"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ops := backup.Start(ctx, backup.StartOptions{DB: db, DataDir: dir, Version: "test", Logger: logger})
	// LIFO: stop and wait for the background jobs, then close the db, then TempDir goes.
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { cancel(); ops.Wait() })
	sec := secrets.NewMemory()
	ca, err := tlsca.LoadOrCreate(ctx, tlsca.Options{Dir: dir + "/tls", Secrets: sec, Hosts: []string{"localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "localhost", AgentPort: 8461, Fingerprint: ca.Fingerprint()})
	mr := &machinesRuntime{Service: svc, CA: ca, stop: func() {}}
	um, _ := startUpdates(ctx, cfg, logger, mr, ops.Settings, &idleHook{}, audit.New(db))
	queue := scheduler.NewEngine(db, scheduler.WithLogger(logger))

	ctl, err := buildController(ctx, controllerDeps{
		Cfg: cfg, Logger: logger, DB: db, Secrets: sec, Ops: ops,
		Machines: svc, CA: ca, Updates: um, Queue: queue, Packages: wiringPackages(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctl.Close)
	mux := http.NewServeMux()
	api.Register(mux, ctl.Options)
	return ctl, api.Recover(logger, mux)
}

// stubbedOperations reads the operations that internal/api/stubs.go still
// answers 501 for, so the allowed list cannot rot: building an operation
// deletes its stub, and the guard then holds it to a real answer.
func stubbedOperations(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../internal/api/stubs.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`func \(s \*server\) (\w+)\(`).FindAllStringSubmatch(string(b), -1) {
		out[strings.ToLower(m[1][:1])+m[1][1:]] = true
	}
	return out
}

// TestControllerAnswersEveryBuiltOperation is the wiring guard: every
// operation of the contract, except the ones stubs.go marks not built, must
// answer something other than 501 through the real controller wiring. Area
// tests wire their own service, so only this test sees a service that
// cmd/crsp forgot to pass (CRSP41).
func TestControllerAnswersEveryBuiltOperation(t *testing.T) {
	_, h := realController(t)
	stubs := stubbedOperations(t)

	raw, err := os.ReadFile("../../../../packages/contract/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	seenStub := map[string]bool{}
	var bad []string
	checked := 0
	var paths []string
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		for method, op := range doc.Paths[p] {
			method = strings.ToUpper(method)
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				continue
			}
			if stubs[op.OperationID] {
				seenStub[op.OperationID] = true
				continue
			}
			path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(p, "1")
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			req := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader([]byte("{}")))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			cancel()
			checked++
			if rec.Code == http.StatusNotImplemented {
				bad = append(bad, op.OperationID+" "+method+" "+p)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d operations checked; the contract was not read", checked)
	}
	for s := range stubs {
		if !seenStub[s] {
			t.Errorf("stubs.go stubs %q, which is not an operation of the contract", s)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("the controller wiring leaves %d built operations answering 501 (an area is missing from buildController):\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// TestFirstRunFlow walks the first run as far as it goes without GitHub: the
// state answers, the link the controller printed is redeemed once, and the
// manifest form comes back.
func TestFirstRunFlow(t *testing.T) {
	ctl, h := realController(t)
	do := func(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	st := do("GET", "/api/v1/setup", "")
	if st.Code != http.StatusOK {
		t.Fatalf("getSetupState: %d %s", st.Code, st.Body)
	}
	var state map[string]any
	_ = json.Unmarshal(st.Body.Bytes(), &state)
	if state["has_app"] != false {
		t.Fatalf("a fresh controller has no App: %v", state)
	}

	if !strings.HasPrefix(ctl.SetupLink, "http://127.0.0.1:8460/setup?code=") {
		t.Fatalf("the controller printed no setup link: %q", ctl.SetupLink)
	}
	code := strings.TrimPrefix(ctl.SetupLink, "http://127.0.0.1:8460/setup?code=")

	if rec := do("POST", "/api/v1/setup/github-app", `{"app_name":"crsp","account_type":"user","console_urls":["http://127.0.0.1:8460"]}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the manifest needs the setup cookie: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/setup/redeem", `{"code":"WRONG-CODE"}`); rec.Code == http.StatusOK {
		t.Fatalf("a wrong code was accepted")
	}
	red := do("POST", "/api/v1/setup/redeem", `{"code":"`+code+`"}`)
	if red.Code != http.StatusOK {
		t.Fatalf("redeemSetupLink: %d %s", red.Code, red.Body)
	}
	cookies := red.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("redeemSetupLink set no setup cookie")
	}
	if again := do("POST", "/api/v1/setup/redeem", `{"code":"`+code+`"}`); again.Code == http.StatusOK {
		t.Fatal("a setup link worked twice")
	}

	man := do("POST", "/api/v1/setup/github-app", `{"app_name":"crsp","account_type":"user","console_urls":["http://127.0.0.1:8460"]}`, cookies...)
	if man.Code != http.StatusOK {
		t.Fatalf("startAppManifest: %d %s", man.Code, man.Body)
	}
	var form struct {
		Manifest map[string]any `json:"manifest"`
		PostURL  string         `json:"post_url"`
		State    string         `json:"state"`
	}
	if err := json.Unmarshal(man.Body.Bytes(), &form); err != nil || form.State == "" || form.PostURL == "" || form.Manifest["name"] == nil {
		t.Fatalf("manifest form: %v %s", err, man.Body)
	}
}

type wiringRelease struct{}

func (wiringRelease) Latest(context.Context) (update.Release, error) {
	return update.Release{Version: "1.0.0", Assets: map[string]string{"SHA256SUMS": "https://example.test/SHA256SUMS"}}, nil
}

// wiringPackages is a package store of a release that lists no installer, so a package answers 404, not 501.
func wiringPackages(t *testing.T) *packages.Store {
	return &packages.Store{
		Source: wiringRelease{}, Dir: t.TempDir(), AllowURL: func(string) bool { return true },
		Fetch: func(_ context.Context, _, dst string) error { return os.WriteFile(dst, nil, 0o600) },
	}
}
