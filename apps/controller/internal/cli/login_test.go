package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/contexts"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// fakeGitHub serves the device-flow endpoints (the real GitHub API is never called).
func fakeGitHub(t *testing.T, pendingFirst bool) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "Iv23liTest" {
			writeJSON(w, 200, map[string]any{"error": "bad_client"})
			return
		}
		writeJSON(w, 200, map[string]any{"device_code": "dev123", "user_code": "WDJB-MJHT", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if pendingFirst && polls.Add(1) == 1 {
			writeJSON(w, 200, map[string]any{"error": "authorization_pending"})
			return
		}
		writeJSON(w, 200, map[string]any{"access_token": "gho_githubusertoken", "token_type": "bearer"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fixture) loginHandlers(gh *httptest.Server, exchanged *map[string]any) {
	f.reply("GET /api/v1/auth/cli/config", 200, map[string]any{"client_id": "Iv23liTest", "device_code_url": gh.URL + "/login/device/code", "verification_url": "https://github.com/login/device"})
	f.handle("POST /api/v1/auth/cli", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(exchanged)
		writeJSON(w, 201, map[string]any{"token": "ctl_secret_controller_token", "info": map[string]any{"id": "tok9", "name": "laptop", "user_id": 1, "user_login": "luis-mc", "created_at": "2026-11-14T10:00:00Z", "expires_at": "2026-12-14T10:00:00Z"}})
	})
}

func TestDeviceFlowSavesToken(t *testing.T) {
	f := newFixture(t)
	f.dir = t.TempDir() // fresh: no contexts yet
	f.secrets = &memSecrets{}
	gh := fakeGitHub(t, true)
	var ex map[string]any
	f.loginHandlers(gh, &ex)
	code, out, errOut := f.run("login", f.srv.URL, "--fingerprint", f.fingerprint(), "--name", "home", "--token-name", "laptop")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	if !strings.Contains(errOut, "WDJB-MJHT") || !strings.Contains(errOut, "https://github.com/login/device") || !strings.Contains(errOut, "Signed in to home as luis-mc") {
		t.Fatalf("stderr %q", errOut)
	}
	if ex["github_credential"] != "gho_githubusertoken" || ex["name"] != "laptop" {
		t.Fatalf("exchange %v", ex)
	}
	// Token only in the secret store; never printed; never in the config file.
	raw, err := f.secrets.Get(context.Background(), tokenKey("home"))
	if err != nil {
		t.Fatal(err)
	}
	var st storedToken
	_ = json.Unmarshal(raw, &st)
	if st.Token != "ctl_secret_controller_token" || st.ID != "tok9" {
		t.Fatalf("%+v", st)
	}
	cfg, _ := os.ReadFile(contexts.Path(f.dir))
	for _, secret := range []string{"ctl_secret_controller_token", "gho_githubusertoken"} {
		if strings.Contains(out+errOut+string(cfg), secret) {
			t.Fatalf("%s leaked", secret)
		}
	}
	cf, _ := contexts.Load(f.dir)
	if c := cf.Contexts["home"]; cf.Current != "home" || c.URL != f.srv.URL || c.Fingerprint != f.fingerprint() {
		t.Fatalf("%+v", cf)
	}
	// The saved token is used on the next command.
	var auth string
	f.handle("GET /api/v1/machines", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"items": []any{}})
	})
	if code, _, _ := f.run("machine", "list"); code != 0 || auth != "Bearer ctl_secret_controller_token" {
		t.Fatalf("%d %q", code, auth)
	}
}

func TestFingerprintPinnedAndMismatchRefused(t *testing.T) {
	f := newFixture(t)
	f.dir = t.TempDir()
	f.secrets = &memSecrets{}
	gh := fakeGitHub(t, false)
	var ex map[string]any
	f.loginHandlers(gh, &ex)
	// A wrong fingerprint is refused: exit 6, nothing saved.
	code, _, errOut := f.run("login", f.srv.URL, "--fingerprint", strings.Repeat("00", 32), "--name", "home")
	if code != ExitUnreachable || !strings.Contains(errOut, "Nothing changed") || f.called("POST /api/v1/auth/cli") {
		t.Fatalf("%d %q", code, errOut)
	}
	if cf, _ := contexts.Load(f.dir); len(cf.Contexts) != 0 {
		t.Fatal("a context was saved")
	}
	// Without a flag: the fingerprint is shown and a yes is asked.
	if code, _, e := f.run("login", f.srv.URL, "--name", "home"); code != ExitNotConfirmed || !strings.Contains(e, f.fingerprint()) {
		t.Fatalf("%d %q", code, e)
	}
	f.terminal, f.stdin = true, "yes\n"
	if code, _, e := f.run("login", f.srv.URL, "--name", "home"); code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	// A controller with another certificate is refused even with a good token.
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"items": []any{}}) }))
	other.TLS = &tls.Config{Certificates: []tls.Certificate{freshCert(t)}}
	other.StartTLS()
	defer other.Close()
	cf, _ := contexts.Load(f.dir)
	c := cf.Contexts["home"]
	c.URL = other.URL
	cf.Set(c)
	_ = cf.Save(f.dir)
	code, _, errOut = f.run("machine", "list")
	if code != ExitUnreachable || !strings.Contains(errOut, "does not match the fingerprint pinned at login") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestLoginErrors(t *testing.T) {
	f := newFixture(t)
	f.dir = t.TempDir()
	f.secrets = &memSecrets{}
	if code, _, _ := f.run("login"); code != ExitUsage {
		t.Fatal("no url")
	}
	if code, _, _ := f.run("login", "http://plain.example"); code != ExitUsage {
		t.Fatal("plain http must be refused (R6.3)")
	}
	gh := fakeGitHub(t, false)
	f.reply("GET /api/v1/auth/cli/config", 200, map[string]any{"client_id": "wrong", "device_code_url": gh.URL + "/login/device/code", "verification_url": "x"})
	if code, _, e := f.run("login", f.srv.URL, "--fingerprint", f.fingerprint(), "--name", "home"); code != ExitUnreachable {
		t.Fatalf("%d %q", code, e)
	}
}

func TestLogoutOffline(t *testing.T) {
	f := newFixture(t)
	f.reply("DELETE /api/v1/auth/cli/tokens/tok1", 204, nil)
	if code, _, e := f.run("logout"); code != 0 || !strings.Contains(e, "revoked") || !f.called("DELETE /api/v1/auth/cli/tokens/tok1") {
		t.Fatalf("%d %q", code, e)
	}
	if _, err := f.secrets.Get(context.Background(), tokenKey("home")); err != secrets.ErrNotFound {
		t.Fatal("token kept")
	}
	if code, _, e := f.run("logout"); code != ExitAuth || !strings.Contains(e, "Not signed in") {
		t.Fatalf("twice: %d %q", code, e)
	}
	// Offline: the controller cannot be reached; the local token goes anyway.
	f2 := newFixture(t)
	f2.srv.Close()
	code, _, e := f2.run("logout")
	if code != 0 || !strings.Contains(e, "deleted here") || !strings.Contains(e, "controller was not told") {
		t.Fatalf("%d %q", code, e)
	}
	if _, err := f2.secrets.Get(context.Background(), tokenKey("home")); err != secrets.ErrNotFound {
		t.Fatal("token kept offline")
	}
}

func TestOneTimeLinkPrinted(t *testing.T) {
	f := newFixture(t)
	f.reply("POST /api/v1/auth/console-links", 201, map[string]any{"url": f.srv.URL + "/auth/link?code=abc", "expires_at": "2026-11-14T10:01:00Z"})
	code, out, e := f.run("open")
	if code != 0 || strings.TrimSpace(out) != f.srv.URL+"/auth/link?code=abc" || !strings.Contains(e, "works once and expires in 60 seconds") || len(f.opened) != 1 {
		t.Fatalf("%d %q %q %v", code, out, e, f.opened)
	}
	f.opened = nil
	if code, out, _ := f.run("open", "--print"); code != 0 || out == "" || len(f.opened) != 0 {
		t.Fatalf("--print opened a browser: %v", f.opened)
	}
	if code, out, _ := f.run("open", "--json"); code != 0 || !strings.Contains(out, `"expires_at"`) {
		t.Fatalf("%q", out)
	}
}

// TestNoSecretInOutputOrConfig runs commands with a canary token and checks
// that it appears in no output, no config file and no request URL (R6.1).
func TestNoSecretInOutputOrConfig(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.repos(repoJSON(10, "luis-mc/app", true))
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{}})
	f.reply("GET /api/v1/jobs", 200, map[string]any{"items": []any{}, "stale": map[string]any{"is_stale": false}})
	f.reply("POST /api/v1/auth/console-links", 201, map[string]any{"url": "https://x/auth/link?code=abc", "expires_at": "2026-11-14T10:01:00Z"})
	f.reply("POST /api/v1/doctor/runs", 202, doctorRun("done", "ok"))
	f.reply("POST /api/v1/backups", 202, map[string]any{"id": "b1", "file": "f", "state": "writing", "created_at": "2026-11-14T10:00:00Z"})
	f.reply("DELETE /api/v1/auth/cli/tokens/tok1", 204, nil)
	f.reply("GET /api/v1/machines/m_build-01/changes/c1", 200, change("done"))
	f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("done"))
	var seen []string
	for _, args := range [][]string{
		{"machine", "list"}, {"machine", "list", "--json"}, {"repo", "list"}, {"runner", "list"}, {"job", "list"}, {"open"},
		{"doctor"}, {"backup"}, {"context", "list"}, {"machine", "drain", "build-01", "--yes"}, {"version"}, {"help"}, {"completion", "bash"},
		{"machine", "nope", "--json"}, {"machine", "list", "--context", "zzz"}, {"logout"},
	} {
		_, out, errOut := f.run(args...)
		seen = append(seen, out, errOut)
	}
	cfg, _ := os.ReadFile(contexts.Path(f.dir))
	seen = append(seen, string(cfg), strings.Join(f.calls, "\n"))
	for _, s := range seen {
		if strings.Contains(s, canary) {
			t.Fatalf("canary token leaked: %q", s)
		}
	}
}

// freshCert makes a self-signed certificate that differs from the one the
// httptest servers share, for the fingerprint mismatch test.
func freshCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "other"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// D38: a controller on 127.0.0.1 speaks plain HTTP: login needs no fingerprint
// and saves none. A plain-HTTP address that is not loopback is refused.
func TestLoginOverPlainHTTPOnLoopback(t *testing.T) {
	f := newFixture(t)
	f.dir = t.TempDir()
	f.secrets = &memSecrets{}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		h := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			writeJSON(w, 404, apiErr("not_found", "no such route"))
			return
		}
		h(w, r)
	}))
	t.Cleanup(plain.Close)
	gh := fakeGitHub(t, false)
	var ex map[string]any
	f.loginHandlers(gh, &ex)
	if !strings.HasPrefix(plain.URL, "http://127.0.0.1:") {
		t.Fatalf("test server not on loopback: %s", plain.URL)
	}
	code, _, errOut := f.run("login", plain.URL, "--name", "local", "--token-name", "laptop")
	if code != 0 || !strings.Contains(errOut, "Signed in to local as luis-mc") {
		t.Fatalf("%d %q", code, errOut)
	}
	cf, err := contexts.Load(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := cf.ByURL(plain.URL); !ok || c.Fingerprint != "" {
		t.Fatalf("context %+v %v", c, ok)
	}
	code, _, errOut = f.run("login", "http://192.168.1.20:8460", "--name", "lan")
	if code != ExitUsage || !strings.Contains(errOut, "must start with https://") {
		t.Fatalf("plain http off loopback: %d %q", code, errOut)
	}
}

// CRSP384, github-integration R5.14: GitHub answers device_flow_disabled when
// "Enable Device Flow" is off; the CLI says so and names the settings page.
func TestLoginDeviceFlowOffNamesTheSetting(t *testing.T) {
	cases := []struct {
		name, settings, want string
	}{
		{"with the app page", "https://github.com/settings/apps/crsp-home", "https://github.com/settings/apps/crsp-home"},
		{"without a slug", "", "Developer settings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.dir = t.TempDir()
			f.secrets = &memSecrets{}
			mux := http.NewServeMux()
			mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"error": "device_flow_disabled", "error_description": "Device flow must be explicitly enabled for this App"})
			})
			gh := httptest.NewServer(mux)
			t.Cleanup(gh.Close)
			cfg := map[string]any{"client_id": "Iv23liTest", "device_code_url": gh.URL + "/login/device/code", "verification_url": "https://github.com/login/device"}
			if tc.settings != "" {
				cfg["settings_url"] = tc.settings
			}
			f.reply("GET /api/v1/auth/cli/config", 200, cfg)
			code, _, e := f.run("login", f.srv.URL, "--fingerprint", f.fingerprint(), "--name", "home")
			if code != ExitFailed {
				t.Fatalf("exit %d, want %d: %q", code, ExitFailed, e)
			}
			if !strings.Contains(e, "Device Flow is off") || !strings.Contains(e, "Enable Device Flow") || !strings.Contains(e, tc.want) {
				t.Fatalf("message %q", e)
			}
			if strings.Contains(e, "Try again in a minute") {
				t.Fatalf("the old words are back: %q", e)
			}
		})
	}
}
