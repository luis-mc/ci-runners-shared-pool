package mirror

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches/toolcache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

// upstream makes a fake GitHub: <base>/owner/repo.git with one commit and a pull ref.
func upstream(t *testing.T) (base, sha string) {
	t.Helper()
	needGit(t)
	base = t.TempDir()
	bare := filepath.Join(base, "owner", "repo.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "--bare", "-b", "main")
	work := t.TempDir()
	git(t, work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "one")
	sha = git(t, work, "rev-parse", "HEAD")
	git(t, work, "push", bare, "main", "HEAD:refs/pull/1/head")
	return base, sha
}

func newGit(t *testing.T, base string) *Git {
	return &Git{Dir: filepath.Join(t.TempDir(), "git"), Run: sys.ExecCommands{}, GitHub: base, Timeout: time.Minute,
		Now: func() time.Time { return time.Unix(1000, 0) }}
}

func TestRefreshMirrorsBranchesTagsAndPullRefs(t *testing.T) {
	base, sha := upstream(t)
	g := newGit(t, base)
	if err := g.Refresh(context.Background(), "owner/repo"); err != nil {
		t.Fatal(err)
	}
	p, _ := g.Path("owner/repo")
	if got := git(t, p, "rev-parse", "refs/pull/1/head"); got != sha {
		t.Fatalf("pull ref %s want %s", got, sha)
	}
	st := g.States()
	if len(st) != 1 || st[0].Status != StatusOK || st[0].Size == 0 || st[0].LastRefresh.IsZero() {
		t.Fatalf("state %+v", st)
	}
}

func TestMissRefreshesThenFallsBack(t *testing.T) {
	base, sha := upstream(t)
	g := newGit(t, base)
	ctx := context.Background()
	if r := g.Resolve(ctx, "owner/repo", sha); !r.FromMirror || !r.Refreshed {
		t.Fatalf("first resolve %+v", r)
	}
	if r := g.Resolve(ctx, "owner/repo", sha); !r.FromMirror || r.Refreshed {
		t.Fatalf("second resolve should come from the mirror %+v", r)
	}
	// Upstream gone: a missing commit refreshes, fails, and the job goes to GitHub.
	if err := os.RemoveAll(filepath.Join(base, "owner")); err != nil {
		t.Fatal(err)
	}
	r := g.Resolve(ctx, "owner/repo", strings.Repeat("0", 40))
	if r.FromMirror || r.Reason == "" {
		t.Fatalf("expected fall back, got %+v", r)
	}
	if st := g.States(); st[0].Status != StatusFailed {
		t.Fatalf("status %s", st[0].Status)
	}
}

func TestRewriteSendsFetchToMirrorAndPushToGitHub(t *testing.T) {
	g := &Git{GitHub: addresses.DefaultGitHubURL}
	pairs, err := g.Rewrite("owner/repo", "https://10.0.0.5:8443/git/")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"url.https://10.0.0.5:8443/git/owner__repo.git.insteadOf", addresses.DefaultGitHubURL + "/owner/repo"},
		{"url." + addresses.DefaultGitHubURL + "/owner/repo.pushInsteadOf", addresses.DefaultGitHubURL + "/owner/repo"},
	}
	if len(pairs) != 2 || pairs[0] != want[0] || pairs[1] != want[1] {
		t.Fatalf("got %v", pairs)
	}
}

func TestBadRepoNamesRefused(t *testing.T) {
	g := &Git{Dir: t.TempDir()}
	for _, n := range []string{"", "a", "../x/y", "a/../b", "a/b/c", "/a/b", "a/b.git", "-a/b", "a\\b/c"} {
		if _, err := g.Path(n); !errors.Is(err, ErrBadRepo) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestWarmthFallsAndResets(t *testing.T) {
	now := time.Unix(0, 0)
	w := &Warmth{Now: func() time.Time { return now }, Hours: func() int { return 10 }}
	w.Record("m1", "o/r", []string{WarmImage, WarmTools, WarmGit, WarmActions, "bogus"})
	if v := w.Value("m1", "o/r"); v != 100 {
		t.Fatalf("fresh %d", v)
	}
	now = now.Add(5 * time.Hour)
	if v := w.Value("m1", "o/r"); v != 50 {
		t.Fatalf("half %d", v)
	}
	now = now.Add(5 * time.Hour)
	if v := w.Value("m1", "o/r"); v != 0 {
		t.Fatalf("expired %d", v)
	}
	now = time.Unix(0, 0)
	w.Record("m1", "o/r", []string{WarmImage, WarmActions})
	w.Reset("m1", WarmActions)
	if v := w.Value("m1", "o/r"); v != 75 {
		t.Fatalf("after reset %d", v)
	}
	if w.Value("m2", "o/r") != 0 {
		t.Fatal("other machine is cold")
	}
}

type fakeJobs struct{ left []string }

func (f *fakeJobs) Using(context.Context, string, string) ([]string, error) { return f.left, nil }

type rec struct{ actions []string }

func (r *rec) Record(_ context.Context, _ audit.Actor, a string, _ audit.Target, _ map[string]any) error {
	r.actions = append(r.actions, a)
	return nil
}
func (r *rec) RecordRefused(context.Context, audit.Actor, string, audit.Target, map[string]any) error {
	return nil
}

func actionsHolder(t *testing.T) (*lancache.Server, *lancache.Store) {
	t.Helper()
	st, err := lancache.Open(t.TempDir(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := lancache.NewServer(st, nil, "https://10.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func TestWipePlansWaitsChecksAgainAndAudits(t *testing.T) {
	srv, st := actionsHolder(t)
	for _, n := range []string{"r1/k1/v", "r1/k2/v", "r2/k1/v"} {
		name, _ := lancache.Name(lancache.NSActions, strings.Split(n, "/")[0], strings.Join(strings.Split(n, "/")[1:], "/"))
		if _, err := st.Put(name, "", strings.NewReader("data-"+n)); err != nil {
			t.Fatal(err)
		}
	}
	jobs := &fakeJobs{left: []string{"job-7"}}
	now := time.Unix(0, 0)
	w := &Warmth{Now: func() time.Time { return now }}
	w.Record("m", "o/r", []string{WarmActions, WarmImage})
	r := &rec{}
	wp := &Wiper{Targets: TargetsFor(srv, st, nil, nil, ""), Jobs: jobs, Warmth: w, Audit: r, Machine: "m",
		Now: func() time.Time { return now }, Poll: time.Second, Wait: time.Minute,
		Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); jobs.left = nil; return nil }}
	actor := audit.Actor{Login: "luis"}
	p, _, err := wp.Run(context.Background(), actor, true, false, ScopeActionsRepo, "r1")
	if !errors.Is(err, ErrNeedsYes) || len(p.Items) != 1 || p.Total == 0 || len(p.Jobs) != 1 {
		t.Fatalf("plan %+v err %v", p, err)
	}
	if len(st.Entries("actions/")) != 3 {
		t.Fatal("nothing may be deleted before the yes")
	}
	if _, _, err := wp.Run(context.Background(), actor, false, true, ScopeActionsRepo, "r1"); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer %v", err)
	}
	_, freed, err := wp.Run(context.Background(), actor, true, true, ScopeActionsRepo, "r1")
	if err != nil || freed == 0 {
		t.Fatalf("wipe %v freed %d", err, freed)
	}
	if left := st.Entries("actions/"); len(left) != 1 {
		t.Fatalf("left %v", left)
	}
	if len(r.actions) != 1 || r.actions[0] != "cache.wipe" {
		t.Fatalf("audit %v", r.actions)
	}
	if v := w.Value("m", "o/r"); v != 25 {
		t.Fatalf("warmth after wipe %d", v)
	}
}

func TestWipeStillDrainingDeletesNothing(t *testing.T) {
	srv, st := actionsHolder(t)
	name, _ := lancache.Name(lancache.NSActions, "r1", "k/v")
	_, _ = st.Put(name, "", strings.NewReader("x"))
	now := time.Unix(0, 0)
	wp := &Wiper{Targets: TargetsFor(srv, st, nil, nil, ""), Jobs: &fakeJobs{left: []string{"a", "b"}},
		Now: func() time.Time { return now }, Poll: time.Second, Wait: 3 * time.Second,
		Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }}
	_, _, err := wp.Run(context.Background(), audit.Actor{}, true, true, ScopeActionsAll, "")
	var sd *StillDraining
	if !errors.As(err, &sd) || !strings.HasPrefix(err.Error(), "still draining, 2 jobs running, waited") {
		t.Fatalf("err %v", err)
	}
	if len(st.Entries("actions/")) != 1 {
		t.Fatal("deleted while draining")
	}
}

func TestRestoreOfWipedEntryIsAMissAndCountsStats(t *testing.T) {
	srv, st := actionsHolder(t)
	name, _ := lancache.Name(lancache.NSActions, "r1", "k/v")
	_, _ = st.Put(name, "", strings.NewReader("x"))
	srv.Stats().Hit("r1", time.Unix(5, 0))
	srv.Stats().Miss("r1")
	got := srv.RepoStats()
	if len(got) != 1 || got[0].Entries != 1 || got[0].Hits != 1 || got[0].Misses != 1 {
		t.Fatalf("stats %+v", got)
	}
	if _, _, err := srv.WipeRepo("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Lookup(name); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("want miss, got %v", err)
	}
	if len(srv.RepoStats()) != 0 {
		t.Fatal("counters kept")
	}
}

type memSettings struct{ v Settings }

func (m *memSettings) GetMirrorSettings(context.Context) (Settings, error) { return m.v, nil }
func (m *memSettings) SetMirrorSettings(_ context.Context, s Settings) error {
	m.v = s
	return nil
}

func TestSettingsDefaultsValidateAndAudit(t *testing.T) {
	ms := &memSettings{}
	s := &Service{Settings: ms}
	got, _ := s.Get(context.Background())
	if got.ActionsCache || got.GitMirror || got.RegistryMirror || got.GitRefreshMin != 15 || got.WarmthHours != 24 {
		t.Fatalf("defaults %+v", got)
	}
	r := &rec{}
	if _, err := s.Put(context.Background(), audit.Actor{}, r, Settings{GitRefreshMin: 5000}); err == nil {
		t.Fatal("too long refresh accepted")
	}
	if _, err := s.Put(context.Background(), audit.Actor{}, r, Settings{GitMirror: true, GitRefreshMin: 5}); err != nil {
		t.Fatal(err)
	}
	if !ms.v.GitMirror || ms.v.GitRefreshMin != 5 || len(r.actions) != 1 {
		t.Fatalf("stored %+v audit %v", ms.v, r.actions)
	}
}

func TestRowsFollowSwitchesAndToolCache(t *testing.T) {
	base, _ := upstream(t)
	g := newGit(t, base)
	_ = g.Refresh(context.Background(), "owner/repo")
	srv, st := actionsHolder(t)
	tool := &toolcache.Store{Root: t.TempDir()}
	ms := &memSettings{}
	s := &Service{Settings: ms, Git: g, Actions: srv, ActStore: st, Tool: tool, Layout: LayoutFor(tool.Root)}
	rows, _ := s.Rows(context.Background())
	if len(rows) != 1 || rows[0].Kind != "tool_cache" {
		t.Fatalf("all off: %+v", rows)
	}
	ms.v = Settings{ActionsCache: true, GitMirror: true, RegistryMirror: true}
	rows, _ = s.Rows(context.Background())
	kinds := map[string]bool{}
	for _, r := range rows {
		kinds[r.Kind] = true
	}
	for _, k := range []string{"actions_cache", "git_mirror", "tool_cache", "registry_mirror"} {
		if !kinds[k] {
			t.Errorf("missing %s in %+v", k, rows)
		}
	}
	for _, id := range []string{"mirror:git_mirror:owner/repo", "mirror:actions_all", "mirror:tool_cache", "mirror:registry_mirror", "mirror:actions_repo:123"} {
		if _, _, ok := ParseID(id); !ok {
			t.Errorf("id %s", id)
		}
	}
	for _, id := range []string{"x", "mirror:", "mirror:git_mirror", "mirror:tool_cache:x"} {
		if _, _, ok := ParseID(id); ok {
			t.Errorf("bad id %s accepted", id)
		}
	}
}

func TestLayoutIsSiblingOfToolCache(t *testing.T) {
	for goos, want := range map[string]string{"linux": "/var/lib/crsp", "darwin": "/Users/Shared/crsp"} {
		if got := filepath.ToSlash(LayoutFor(toolcache.DefaultRoot(goos)).Base); got != want {
			t.Errorf("%s: %s", goos, got)
		}
	}
}
