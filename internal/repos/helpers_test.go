package repos

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

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

type fakeAudit struct {
	mu     sync.Mutex
	events []string
}

func (a *fakeAudit) Audit(_ context.Context, p auth.Principal, action, _, subject, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, action+"|"+p.Login+"|"+subject+"|"+detail)
}

func (a *fakeAudit) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if len(e) >= len(action) && e[:len(action)] == action {
			return true
		}
	}
	return false
}

type tokens struct {
	m map[int64]github.UserToken
}

func (t *tokens) GitHubToken(id int64) (github.UserToken, error) {
	if tok, ok := t.m[id]; ok {
		return tok, nil
	}
	return github.UserToken{}, auth.ErrGitHubSessionExpired
}

type capacity struct{ m map[string][]string }

func (c *capacity) OSLabels(context.Context) (map[string][]string, error) { return c.m, nil }

type env struct {
	t      *testing.T
	svc    *Service
	store  *MemStore
	fake   *fake.Server
	gh     *github.REST
	audit  *fakeAudit
	tokens *tokens
	cap    *capacity
	mu     sync.Mutex
	now    time.Time
	// public is the setting Public repositories. It starts on because most
	// tests predate the setting and use repository 100, which is public; the
	// tests of the setting turn it off.
	public bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := fake.New()
	t.Cleanup(f.Close)
	k := key()
	f.AppPublicKey = &k.PublicKey
	sec := secrets.NewMemory()
	_ = sec.Put(context.Background(), github.SecretKey(f.AppID, github.SecretPrivateKey),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	e := &env{t: t, store: NewMemStore(), fake: f, audit: &fakeAudit{}, tokens: &tokens{m: map[int64]github.UserToken{}},
		cap: &capacity{m: map[string][]string{"Linux": {"X64", "docker", "crsp-home"}}}, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), public: true}
	f.Now = e.clock
	e.gh = github.New(github.Config{APIBase: f.URL, WebBase: f.URL, Secrets: sec, Now: e.clock})
	e.gh.UseApp(f.AppID, "Iv23liFAKECLIENTID00")
	e.svc = New(Config{Store: e.store, GitHub: e.gh, Tokens: e.tokens, Audit: e.audit, Capacity: e.cap, Now: e.clock,
		PublicRepositories: func(context.Context) (bool, error) { return e.publicOn(), nil }})
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

var (
	adminP  = auth.Principal{UserID: 1, Login: "alice", Role: auth.RoleAdmin, Source: "web"}
	viewerP = auth.Principal{UserID: 2, Login: "bob", Role: auth.RoleViewer, Source: "web"}
)

// world: user 1 (alice) admins repo 100 (public) and 101 (private); user 2 (bob)
// is a plain collaborator. Alice is signed in (token in memory) and has synced.
func (e *env) world() {
	e.t.Helper()
	tok := e.fake.AddUser(1, "alice")
	e.fake.AddUser(2, "bob")
	e.tokens.m[1] = github.UserToken{Value: tok}
	e.fake.AddInstall(10, "alice", "User", 1, 2)
	e.fake.AddRepo(100, 10, "alice", "web", false, 1)
	e.fake.AddRepo(101, 10, "alice", "secret", true, 1)
	e.fake.AddRepo(102, 10, "alice", "other", true) // alice does not admin it
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		e.t.Fatal(err)
	}
}

// turnOn plans and applies the pool for repos.
func (e *env) turnOn(ids ...int64) Plan {
	e.t.Helper()
	var cs []Change
	for _, id := range ids {
		cs = append(cs, Change{RepositoryID: id, Pool: true})
	}
	plan, err := e.svc.PlanChanges(context.Background(), adminP, cs)
	if err != nil {
		e.t.Fatal(err)
	}
	plan, err = e.svc.ApplyPlan(context.Background(), adminP, plan.ID, nil)
	if err != nil || plan.State != PlanDone {
		e.t.Fatalf("apply: %v %+v", err, plan)
	}
	return plan
}

func (e *env) publicOn() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.public
}

func (e *env) setPublic(on bool) {
	e.mu.Lock()
	e.public = on
	e.mu.Unlock()
}

// count is how many audit events start with action.
func (a *fakeAudit) count(action string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.events {
		if strings.HasPrefix(e, action) {
			n++
		}
	}
	return n
}

// detail is the detail of the last event that starts with action.
func (a *fakeAudit) detail(action string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := ""
	for _, e := range a.events {
		if strings.HasPrefix(e, action) {
			parts := strings.SplitN(e, "|", 4)
			out = parts[len(parts)-1]
		}
	}
	return out
}
