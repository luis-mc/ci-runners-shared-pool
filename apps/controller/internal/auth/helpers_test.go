package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

const devConsole = "http://127.0.0.1:8460"

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key() *rsa.PrivateKey {
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

type env struct {
	t        *testing.T
	svc      *Service
	store    *MemStore
	fake     *fake.Server
	gh       *github.REST
	secrets  *secrets.Memory
	mu       sync.Mutex
	now      time.Time
	consoles []string
	apps     []AppRecord
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := fake.New()
	t.Cleanup(f.Close)
	k := key()
	f.AppPublicKey = &k.PublicKey
	f.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	e := &env{t: t, store: NewMemStore(), fake: f, secrets: secrets.NewMemory(), now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		consoles: []string{devConsole}}
	f.Now = e.clock
	e.gh = github.New(github.Config{APIBase: f.URL, WebBase: f.URL, Secrets: e.secrets, Now: e.clock})
	e.svc = New(e.store, Config{GitHub: e.gh, Secrets: e.secrets, WebBase: f.URL, Now: e.clock,
		ConsoleURLs: func() []string { return e.consoles },
		OnApp: func(a AppRecord) {
			e.apps = append(e.apps, a)
			e.gh.UseApp(a.ID, a.ClientID)
		}})
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

// withApp runs the manifest flow against the fake so the App exists.
func (e *env) withApp() {
	e.t.Helper()
	ctx := context.Background()
	e.fake.IssueManifestCode("mc")
	form, err := e.svc.StartManifest(ctx, ManifestOptions{AppName: "crsp-test", ConsoleURLs: e.consoles})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.CompleteManifest(ctx, "mc", form.State); err != nil {
		e.t.Fatal(err)
	}
}

// ownerSignIn makes the owner the way the product does: setup code, cookie,
// sign-in with the cookie.
func (e *env) ownerSignIn(id int64, login string) SignedIn {
	e.t.Helper()
	ctx := context.Background()
	e.withApp()
	code, err := e.svc.NewSetupCode(ctx)
	if err != nil || code == "" {
		e.t.Fatalf("setup code %q %v", code, err)
	}
	cookie, err := e.svc.RedeemSetupCode(ctx, code)
	if err != nil {
		e.t.Fatal(err)
	}
	e.fake.AddUser(id, login)
	return e.signIn(id, cookie)
}

// signIn runs the web OAuth flow for a fake GitHub user.
func (e *env) signIn(id int64, setupCookie string) SignedIn {
	e.t.Helper()
	si, err := e.trySignIn(id, setupCookie)
	if err != nil {
		e.t.Fatalf("sign-in: %v", err)
	}
	return si
}

func (e *env) trySignIn(id int64, setupCookie string) (SignedIn, error) {
	ctx := context.Background()
	start, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole, ReturnTo: "/machines", SetupCookie: setupCookie})
	if err != nil {
		return SignedIn{}, err
	}
	u, _ := url.Parse(start.RedirectURL)
	q := u.Query()
	code := "oc-" + q.Get("state")[:8]
	e.fake.IssueOAuthCode(code, id, q.Get("code_challenge"))
	return e.svc.CompleteSignIn(ctx, code, q.Get("state"), devConsole)
}

func (e *env) addUser(owner Principal, id int64, login string, role Role) User {
	e.t.Helper()
	e.fake.AddUser(id, login)
	u, err := e.svc.AddUser(context.Background(), owner, login, role)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func hasAudit(s *MemStore, action string) bool {
	for _, a := range s.AuditEvents() {
		if a.Action == action {
			return true
		}
	}
	return false
}

func noSecretIn(t *testing.T, values []string, secrets ...string) {
	t.Helper()
	for _, v := range values {
		for _, s := range secrets {
			if s != "" && strings.Contains(v, s) {
				t.Fatalf("a secret value reached storage: %q", v)
			}
		}
	}
}
