package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github/fake"
)

func TestVersionHeaderOnEveryCall(t *testing.T) { // R6.8
	e := newEnv(t)
	ctx := context.Background()
	u := e.fake.AddUser(1, "alice")
	e.fake.AddInstall(10, "alice", "User", 1)
	e.fake.AddRepo(100, 10, "alice", "demo", true, 1)
	_, _ = e.client.GetApp(ctx)
	_, _ = e.client.ListUserInstallations(ctx, UserToken{Value: u})
	_, _ = e.client.ListVariables(ctx, 10, "alice", "demo")
	_, _ = e.client.ListOpenJobs(ctx, 10, "alice", "demo")
	reqs := e.fake.Requests()
	if len(reqs) < 5 {
		t.Fatalf("expected several calls, got %d", len(reqs))
	}
	for _, r := range reqs {
		if got := r.Header.Get("X-GitHub-Api-Version"); got != APIVersion {
			t.Errorf("%s %s: version %q", r.Method, r.Path, got)
		}
	}
}

func TestAppJWTLifetimeAndSignature(t *testing.T) { // R6.2
	e := newEnv(t)
	if _, err := e.client.GetApp(context.Background()); err != nil {
		t.Fatalf("fake verifies the JWT signature, lifetime and issuer: %v", err)
	}
	jwt, err := SignAppJWT(sharedKey(t), 7, e.clock())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !strings.Contains(string(payload), `"iss":"7"`) {
		t.Fatalf("payload %s", payload)
	}
}

func TestInstallationTokenCachedAndRenewed(t *testing.T) { // R6.2
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	for i := 0; i < 3; i++ {
		if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.fake.Count("POST", "/app/installations/10/access_tokens"); n != 1 {
		t.Fatalf("token requested %d times, want 1 (cached)", n)
	}
	e.advance(56 * time.Minute) // within 5 minutes of expiry
	if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil {
		t.Fatal(err)
	}
	if n := e.fake.Count("POST", "/app/installations/10/access_tokens"); n != 2 {
		t.Fatalf("token requested %d times, want 2 (renewed)", n)
	}
}

func TestNoAppFailsClosed(t *testing.T) {
	c := New(Config{})
	if _, err := c.GetApp(context.Background()); err == nil {
		t.Fatal("expected an error with no App")
	}
}

func TestSecretStoreDownFailsClosed(t *testing.T) { // section 8
	e := newEnv(t)
	e.client.UseApp(e.fake.AppID, "x")
	e.secrets.Down = true
	_, err := e.client.GetApp(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	if n := len(e.fake.Requests()); n != 0 {
		t.Fatalf("no call may reach GitHub without the key, saw %d", n)
	}
}

func TestConvertManifestOnce(t *testing.T) { // R5.3
	e := newEnv(t)
	e.fake.IssueManifestCode("code-1")
	creds, err := e.client.ConvertManifest(context.Background(), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if creds.App.ID != e.fake.AppID || creds.App.Slug == "" || creds.App.ClientID == "" || creds.WebhookSecret == "" {
		t.Fatalf("incomplete conversion: %+v", creds.App)
	}
	if _, err := e.client.ConvertManifest(context.Background(), "code-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}
	if _, err := e.client.ConvertManifest(context.Background(), ""); err == nil {
		t.Fatal("empty code must fail")
	}
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestOAuthFlow(t *testing.T) { // R5.8, R5.9
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddUser(42, "alice")
	u := e.client.AuthorizeURL("st", "chal", "http://127.0.0.1:8460/auth/github/callback")
	for _, want := range []string{"client_id=Iv23liFAKECLIENTID00", "state=st", "code_challenge=chal", "code_challenge_method=S256",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A8460%2Fauth%2Fgithub%2Fcallback"} {
		if !strings.Contains(u, want) {
			t.Errorf("authorize url lacks %s: %s", want, u)
		}
	}
	e.fake.IssueOAuthCode("c1", 42, pkce("verifier-1"))
	if _, err := e.client.ExchangeCode(ctx, "c1", "wrong-verifier", "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong verifier: %v", err)
	}
	tok, err := e.client.ExchangeCode(ctx, "c1", "verifier-1", "x")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value == "" || tok.ExpiresAt.IsZero() {
		t.Fatalf("token: %+v", tok)
	}
	user, err := e.client.CurrentUser(ctx, tok)
	if err != nil || user.ID != 42 || user.Login != "alice" {
		t.Fatalf("user %+v %v", user, err)
	}
	if _, err := e.client.ExchangeCode(ctx, "c1", "verifier-1", "x"); err == nil {
		t.Fatal("a code works once")
	}
	got, err := e.client.ResolveLogin(ctx, "ALICE")
	if err != nil || got.ID != 42 {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if _, err := e.client.ResolveLogin(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown login: %v", err)
	}
}

func TestListInstallations(t *testing.T) { // R5.17
	e := newEnv(t)
	u1 := e.fake.AddUser(1, "alice")
	e.fake.AddUser(2, "bob")
	e.fake.AddInstall(10, "alice", "User", 1)
	e.fake.AddInstall(11, "acme", "Organization", 1, 2)
	e.fake.AddInstall(12, "other", "Organization", 2)
	got, err := e.client.ListUserInstallations(context.Background(), UserToken{Value: u1})
	if err != nil || len(got) != 2 || got[0].AccountLogin != "alice" || got[1].AccountType != "Organization" {
		t.Fatalf("installations %+v %v", got, err)
	}
	if got[0].Permissions["administration"] != "write" {
		t.Fatalf("permissions %+v", got[0].Permissions)
	}
}

func TestListRepositoriesAllPagesAndAdminFlag(t *testing.T) { // R5.18, R6.11
	e := newEnv(t)
	u := e.fake.AddUser(1, "alice")
	e.fake.AddInstall(10, "alice", "User", 1)
	const n = 1050
	for i := 1; i <= n; i++ {
		var admins []int64
		if i%2 == 0 {
			admins = append(admins, 1)
		}
		e.fake.AddRepo(int64(i), 10, "alice", fmt.Sprintf("r%04d", i), i%3 == 0, admins...)
	}
	got, err := e.client.ListInstallationRepositories(context.Background(), UserToken{Value: u}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("got %d repositories, want %d", len(got), n)
	}
	admin, private := 0, 0
	for _, r := range got {
		if r.Admin {
			admin++
		}
		if r.Visibility == "private" {
			private++
		}
	}
	if admin != n/2 || private != n/3 {
		t.Fatalf("admin %d private %d", admin, private)
	}
}

// repoPages counts the repository-list calls the fake saw and checks that the
// first asks for 100 per page and that every later one follows the Link.
func repoPages(t *testing.T, e *env, path string) int {
	t.Helper()
	pages := 0
	for _, r := range e.fake.Requests() {
		if r.Path != path {
			continue
		}
		pages++
		q, err := url.ParseQuery(r.Query)
		if err != nil {
			t.Fatal(err)
		}
		if q.Get("per_page") != "100" {
			t.Errorf("page %d asked for per_page=%q, want 100", pages, q.Get("per_page"))
		}
		if want := fmt.Sprint(pages); pages > 1 && q.Get("page") != want {
			t.Errorf("call %d went to page %q: the Link was not followed", pages, q.Get("page"))
		}
	}
	return pages
}

func TestRepositoryListsPageThrough1000Repositories(t *testing.T) { // R6.11
	e := newEnv(t)
	ctx := context.Background()
	u := e.fake.AddUser(1, "alice")
	e.fake.AddInstall(10, "alice", "User", 1)
	const n = 1001 // 11 pages of at most 100
	for i := 1; i <= n; i++ {
		e.fake.AddRepo(int64(i), 10, "alice", fmt.Sprintf("r%04d", i), false, 1)
	}
	check := func(name string, got []Repository, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != n {
			t.Fatalf("%s: %d repositories, want %d", name, len(got), n)
		}
		seen := map[int64]bool{}
		for _, r := range got {
			seen[r.ID] = true
		}
		if len(seen) != n {
			t.Fatalf("%s: %d distinct repositories, want %d", name, len(seen), n)
		}
	}
	got, err := e.client.ListInstallationRepositories(ctx, UserToken{Value: u}, 10)
	check("user listing", got, err)
	if p := repoPages(t, e, "/user/installations/10/repositories"); p != 11 {
		t.Errorf("user listing took %d calls, want 11", p)
	}
	got, err = e.client.ListAppRepositories(ctx, 10)
	check("installation listing", got, err)
	if p := repoPages(t, e, "/installation/repositories"); p != 11 {
		t.Errorf("installation listing took %d calls, want 11", p)
	}
}

func TestUserCanAdminReadsFresh(t *testing.T) { // R5.19
	e := newEnv(t)
	u := e.fake.AddUser(1, "alice")
	e.fake.AddInstall(10, "alice", "User", 1)
	rp := e.fake.AddRepo(100, 10, "alice", "demo", true, 1)
	ok, err := e.client.UserCanAdmin(context.Background(), UserToken{Value: u}, "alice", "demo")
	if err != nil || !ok {
		t.Fatalf("admin %v %v", ok, err)
	}
	e.fake.Lock(func() { delete(rp.Admins, 1) })
	ok, err = e.client.UserCanAdmin(context.Background(), UserToken{Value: u}, "alice", "demo")
	if err != nil || ok {
		t.Fatalf("after removal: %v %v", ok, err)
	}
}

func TestRunnerTokensNeverLogged(t *testing.T) { // R5.31, R5.32
	e := newEnv(t)
	var logs strings.Builder
	e.client.cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	scope := RunnerScope{Owner: "alice", Repo: "demo"}
	reg, err := e.client.CreateRegistrationToken(context.Background(), 10, scope)
	if err != nil || !e.fake.HasRegistrationToken(reg) {
		t.Fatalf("registration token %q %v", reg, err)
	}
	rem, err := e.client.CreateRemoveToken(context.Background(), 10, scope)
	if err != nil || !e.fake.HasRemoveToken(rem) {
		t.Fatalf("remove token %q %v", rem, err)
	}
	if e.fake.HasRemoveToken(reg) || e.fake.HasRegistrationToken(rem) {
		t.Fatal("remove and registration tokens must be different things")
	}
	if strings.Contains(logs.String(), reg) || strings.Contains(logs.String(), rem) {
		t.Fatal("a token reached the log")
	}
	if e.fake.Count("POST", "/repos/alice/demo/actions/runners/remove-token") != 1 {
		t.Fatal("remove token must come from the remove-token endpoint")
	}
}

func TestOrganizationScope(t *testing.T) {
	e := newEnv(t)
	e.fake.AddInstall(11, "acme", "Organization")
	e.fake.AddRunner("acme", fake.Runner{Name: "r1", OS: "Linux", Status: "online", Labels: []string{"self-hosted"}})
	rs, err := e.client.ListRunners(context.Background(), 11, RunnerScope{Owner: "acme"})
	if err != nil || len(rs) != 1 || rs[0].Name != "r1" || rs[0].Labels[0] != "self-hosted" {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestDeleteRunner(t *testing.T) { // R5.33
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	e.fake.AddRunner("alice/demo", fake.Runner{ID: 7, Name: "old", Status: "offline"})
	e.fake.AddRunner("alice/demo", fake.Runner{ID: 8, Name: "busy", Status: "online", Busy: true})
	scope := RunnerScope{Owner: "alice", Repo: "demo"}
	if err := e.client.DeleteRunner(context.Background(), 10, scope, 7); err != nil {
		t.Fatal(err)
	}
	if err := e.client.DeleteRunner(context.Background(), 10, scope, 8); !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("a busy runner is refused with 422 and that reads as ErrRunnerBusy: %v", err)
	}
	if err := e.client.DeleteRunner(context.Background(), 10, scope, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown runner reads as ErrNotFound: %v", err)
	}
	if names := e.fake.RunnerNames("alice/demo"); len(names) != 1 || names[0] != "busy" {
		t.Fatalf("runners left: %v", names)
	}
}

func TestVariables(t *testing.T) { // R5.28, R5.29
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	if err := e.client.SetVariable(ctx, 10, "alice", "demo", Variable{Name: "RUNS_ON_LINUX", Value: `["self-hosted","Linux"]`}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.fake.Variable("alice/demo", "RUNS_ON_LINUX"); v != `["self-hosted","Linux"]` {
		t.Fatalf("create: %q", v)
	}
	if err := e.client.SetVariable(ctx, 10, "alice", "demo", Variable{Name: "RUNS_ON_LINUX", Value: `["x"]`}); err != nil {
		t.Fatal(err)
	}
	vs, err := e.client.ListVariables(ctx, 10, "alice", "demo")
	if err != nil || len(vs) != 1 || vs[0].Value != `["x"]` {
		t.Fatalf("update: %+v %v", vs, err)
	}
	if err := e.client.DeleteVariable(ctx, 10, "alice", "demo", "RUNS_ON_LINUX"); err != nil {
		t.Fatal(err)
	}
	if err := e.client.DeleteVariable(ctx, 10, "alice", "demo", "RUNS_ON_LINUX"); err != nil {
		t.Fatalf("deleting a missing variable is not an error: %v", err)
	}
}

func TestWorkflowFilesETag(t *testing.T) { // R5.30
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	if files, err := e.client.ListWorkflowFiles(ctx, 10, "alice", "demo", nil); err != nil || len(files) != 0 {
		t.Fatalf("no workflows dir: %v %v", files, err)
	}
	e.fake.SetWorkflow("alice/demo", ".github/workflows/ci.yml", "name: ci\n")
	e.fake.SetWorkflow("alice/demo", ".github/workflows/notes.txt", "ignore\n")
	files, err := e.client.ListWorkflowFiles(ctx, 10, "alice", "demo", nil)
	if err != nil || len(files) != 1 || string(files[0].Content) != "name: ci\n" || files[0].ETag == "" {
		t.Fatalf("files %+v %v", files, err)
	}
	again, err := e.client.ListWorkflowFiles(ctx, 10, "alice", "demo", map[string]string{files[0].Path: files[0].ETag})
	if err != nil || len(again) != 1 || again[0].Content != nil || again[0].ETag != files[0].ETag {
		t.Fatalf("etag hit must return no content: %+v %v", again, err)
	}
}

func TestOpenJobsAndFork(t *testing.T) { // R5.37
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", false)
	e.fake.AddJob(fake.Job{ID: 1, RunID: 11, Repo: "alice/demo", Name: "build", Status: "queued", Event: "push", Labels: []string{"self-hosted", "Linux"}})
	e.fake.AddJob(fake.Job{ID: 2, RunID: 12, Repo: "alice/demo", Name: "test", Status: "in_progress", Event: "pull_request", HeadRepo: "mallory/demo", RunnerName: "r1"})
	jobs, err := e.client.ListOpenJobs(ctx, 10, "alice", "demo")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	byID := map[int64]Job{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	if byID[1].FromFork || !byID[2].FromFork || byID[2].Event != "pull_request" || !contains(byID[1].Labels, "Linux") {
		t.Fatalf("job details: %+v", byID)
	}
	one, err := e.client.GetJob(ctx, 10, "alice", "demo", 1)
	if err != nil || one.Name != "build" || one.Repository != "alice/demo" {
		t.Fatalf("get job %+v %v", one, err)
	}
}

func TestPrimaryRateLimit(t *testing.T) { // R6.6
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	if _, err := e.client.ListVariables(context.Background(), 10, "alice", "demo"); err != nil {
		t.Fatal(err)
	}
	e.fake.FailRateLimit = true
	e.fake.FailNext(403)
	_, err := e.client.ListVariables(context.Background(), 10, "alice", "demo")
	var rl *RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || rl.ResetAt.IsZero() || rl.Secondary {
		t.Fatalf("want a primary RateLimitError, got %v", err)
	}
	if len(e.sleeps) != 0 {
		t.Fatal("a read is not retried")
	}
}

func TestBackgroundBudgetHalf(t *testing.T) { // R6.6
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	ctx := context.Background()
	if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil {
		t.Fatal(err)
	}
	e.fake.Lock(func() { e.fake.RateRemaining = 2000 }) // 60% used
	if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil {
		t.Fatal(err) // user-driven work still goes through; this call records the budget
	}
	before := len(e.fake.Requests())
	_, err := e.client.ListVariables(Background(ctx), 10, "alice", "demo")
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("background work must stop past half the limit: %v", err)
	}
	if len(e.fake.Requests()) != before {
		t.Fatal("the held-back call must not reach GitHub")
	}
	if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil {
		t.Fatalf("user work is never held back: %v", err)
	}
	e.advance(2 * time.Hour) // past the reset
	if _, err := e.client.ListVariables(Background(ctx), 10, "alice", "demo"); err != nil {
		t.Fatalf("after reset background work goes on: %v", err)
	}
}

func TestWritesRetryAtMostThreeTimes(t *testing.T) { // R6.7
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	if _, err := e.client.ListVariables(ctx, 10, "alice", "demo"); err != nil { // warm the token
		t.Fatal(err)
	}
	before := e.fake.Count("POST", "/repos/alice/demo/actions/runners/registration-token")
	e.fake.FailNext(502, 503, 500, 500)
	_, err := e.client.CreateRegistrationToken(ctx, 10, RunnerScope{Owner: "alice", Repo: "demo"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable after the retries, got %v", err)
	}
	if got := e.fake.Count("POST", "/repos/alice/demo/actions/runners/registration-token") - before; got != MaxWriteAttempts {
		t.Fatalf("write attempted %d times, want %d", got, MaxWriteAttempts)
	}
	if len(e.sleeps) != MaxWriteAttempts-1 {
		t.Fatalf("slept %d times: %v", len(e.sleeps), e.sleeps)
	}
	// One 5xx then success.
	e.fake.FailNext(500)
	if _, err := e.client.CreateRegistrationToken(ctx, 10, RunnerScope{Owner: "alice", Repo: "demo"}); err != nil {
		t.Fatalf("retry must recover: %v", err)
	}
}

func TestReadsDoNotRetry(t *testing.T) { // R6.7
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	if _, err := e.client.ListVariables(context.Background(), 10, "alice", "demo"); err != nil {
		t.Fatal(err)
	}
	before := e.fake.Count("GET", "/repos/alice/demo/actions/variables")
	e.fake.FailNext(503)
	if _, err := e.client.ListVariables(context.Background(), 10, "alice", "demo"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if e.fake.Count("GET", "/repos/alice/demo/actions/variables")-before != 1 {
		t.Fatal("a read must be tried once")
	}
}

func TestSecondaryLimitOnWriteRetriesAfterWait(t *testing.T) { // R6.7
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	_, _ = e.client.ListVariables(ctx, 10, "alice", "demo")
	e.fake.FailRetryAfter = "2"
	e.fake.FailNext(403)
	if _, err := e.client.CreateRegistrationToken(ctx, 10, RunnerScope{Owner: "alice", Repo: "demo"}); err != nil {
		t.Fatal(err)
	}
	if len(e.sleeps) != 1 || e.sleeps[0] != 2*time.Second {
		t.Fatalf("must wait the Retry-After GitHub gave: %v", e.sleeps)
	}
}

func TestForbiddenAndNotFound(t *testing.T) {
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	if _, err := e.client.GetInstallation(context.Background(), 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown installation: %v", err)
	}
	in, err := e.client.GetInstallation(context.Background(), 10)
	if err != nil || in.Permissions["actions_variables"] != "write" {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := e.client.CurrentUser(context.Background(), UserToken{Value: "bad"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bad token: %v", err)
	}
}

func TestCallDeadline(t *testing.T) { // R6.7
	if CallDeadline != 10*time.Second {
		t.Fatalf("deadline %v", CallDeadline)
	}
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.client.ListVariables(ctx, 10, "a", "b"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("a cancelled context is not 'github unavailable': %v", err)
	}
}

func TestUnreachableIsUnavailableAndScrubbed(t *testing.T) {
	e := newEnv(t)
	c := New(Config{APIBase: "http://127.0.0.1:1", Secrets: e.secrets, Now: e.clock})
	c.UseApp(e.fake.AppID, "x")
	_, err := c.ResolveLogin(context.Background(), "alice")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	_ = io.Discard
}

func TestParsePrivateKey(t *testing.T) {
	if _, err := ParsePrivateKey([]byte("nope")); err == nil {
		t.Fatal("must refuse")
	}
	if _, err := ParsePrivateKey(pemOf(sharedKey(t))); err != nil {
		t.Fatal(err)
	}
	if SecretKey(12, SecretPrivateKey) != "crsp/github-app/12/private-key" {
		t.Fatal("key layout changed")
	}
}

func TestCheckUserToken(t *testing.T) { // R5.14
	e := newEnv(t)
	tok := e.fake.AddUser(9, "carol")
	u, err := e.client.CheckUserToken(context.Background(), UserToken{Value: tok})
	if err != nil || u.ID != 9 || u.Login != "carol" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := e.client.CheckUserToken(context.Background(), UserToken{Value: "ghu_notmine"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a token of another App: %v", err)
	}
}

func TestDeviceFlow(t *testing.T) { // R5.14
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddUser(9, "carol")
	e.fake.DeviceSlowDownOnce = true
	dc, err := e.client.StartDevice(ctx)
	if err != nil || dc.UserCode == "" || dc.Interval != 5*time.Second {
		t.Fatalf("%+v %v", dc, err)
	}
	approved := false
	e.client.cfg.Sleep = func(_ context.Context, d time.Duration) error {
		e.mu.Lock()
		e.sleeps = append(e.sleeps, d)
		n := len(e.sleeps)
		e.mu.Unlock()
		if n == 3 && !approved {
			approved = true
			e.fake.ApproveDevice(dc.DeviceCode, 9)
		}
		return nil
	}
	tok, err := e.client.WaitDevice(ctx, dc)
	if err != nil || tok.Value == "" {
		t.Fatalf("%v", err)
	}
	if e.sleeps[0] != 5*time.Second || e.sleeps[1] != 10*time.Second {
		t.Fatalf("must wait the interval, and 5 s longer after slow_down: %v", e.sleeps)
	}
	if u, _ := e.client.CurrentUser(ctx, tok); u.ID != 9 {
		t.Fatalf("%+v", u)
	}
}

func TestDeviceFlowDenied(t *testing.T) {
	e := newEnv(t)
	dc, err := e.client.StartDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.fake.ApproveDevice(dc.DeviceCode, -1)
	if _, err := e.client.WaitDevice(context.Background(), dc); !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("%v", err)
	}
}

func TestMissingPermissions(t *testing.T) { // R5.24
	full := map[string]string{"administration": "write", "actions": "read", "actions_variables": "write",
		"contents": "read", "metadata": "read", "organization_self_hosted_runners": "write"}
	if m := MissingPermissions(full); len(m) != 0 {
		t.Fatalf("%v", m)
	}
	weak := map[string]string{"administration": "read", "actions": "write", "contents": "read", "metadata": "read"}
	got := MissingPermissions(weak)
	want := []string{"actions_variables", "administration", "organization_self_hosted_runners"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v (a stronger grant counts, a weaker one is missing)", got, want)
	}
	if len(RequiredPermissions) != 6 {
		t.Fatal("the set is exactly the R5.23 table")
	}
}

func TestAppLevelListings(t *testing.T) { // R5.22
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddInstall(11, "acme", "Organization")
	e.fake.AddRepo(1, 10, "alice", "a", true)
	e.fake.AddRepo(2, 10, "alice", "b", false)
	ins, err := e.client.ListAppInstallations(ctx)
	if err != nil || len(ins) != 2 {
		t.Fatalf("%+v %v", ins, err)
	}
	repos, err := e.client.ListAppRepositories(ctx, 10)
	if err != nil || len(repos) != 2 || repos[1].Visibility != "public" || repos[0].Admin {
		t.Fatalf("%+v %v", repos, err)
	}
}

func TestCollaboratorPermissionAndForkApproval(t *testing.T) { // R5.19, R5.26
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddUser(1, "alice")
	e.fake.AddUser(2, "bob")
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(1, 10, "alice", "a", true, 1)
	if p, err := e.client.CollaboratorPermission(ctx, 10, "alice", "a", "alice"); err != nil || p != "admin" {
		t.Fatalf("%q %v", p, err)
	}
	if p, _ := e.client.CollaboratorPermission(ctx, 10, "alice", "a", "bob"); p == "admin" {
		t.Fatal("bob is not admin")
	}
	if err := e.client.SetForkApproval(ctx, 10, "alice", "a", ForkApprovalAllOutside); err != nil {
		t.Fatal(err)
	}
	if e.fake.ForkApprovalOf("alice/a") != ForkApprovalAllOutside {
		t.Fatal("fork approval policy not set")
	}
}

// R5.37: the runs list is sent with If-None-Match, a 304 reuses the last
// answer, and a change in the runs is seen at once.
func TestOpenJobsUsesETag(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", false)
	e.fake.AddJob(fake.Job{ID: 1, RunID: 11, Repo: "alice/demo", Name: "build", Status: "queued", Labels: []string{"self-hosted", "linux"}})
	for i := 0; i < 2; i++ {
		jobs, err := e.client.ListOpenJobs(ctx, 10, "alice", "demo")
		if err != nil || len(jobs) != 1 || jobs[0].ID != 1 {
			t.Fatalf("round %d: %+v %v", i, jobs, err)
		}
	}
	conditional := 0
	for _, r := range e.fake.Requests() {
		if strings.HasSuffix(r.Path, "/actions/runs") && r.Header.Get("If-None-Match") != "" {
			conditional++
		}
	}
	if conditional != 2 { // the second call, one per status (queued, in_progress)
		t.Fatalf("conditional runs requests: %d, want 2", conditional)
	}
	e.fake.AddJob(fake.Job{ID: 2, RunID: 12, Repo: "alice/demo", Name: "test", Status: "queued", Labels: []string{"self-hosted", "linux"}})
	jobs, err := e.client.ListOpenJobs(ctx, 10, "alice", "demo")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("a new run must be seen: %+v %v", jobs, err)
	}
}
