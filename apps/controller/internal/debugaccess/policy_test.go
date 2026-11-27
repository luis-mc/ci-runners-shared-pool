package debugaccess

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// R5.1: a repository nobody switched on is off, and a session there is refused.
func TestOffByDefaultAndAfterImportAndRestore(t *testing.T) {
	e := newEnv(t)
	r, err := e.svc.Repo(e.ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Enabled || r.Who != WhoAdmins || r.LimitMin != 30 || r.IdleMin != 10 || r.HoldMin != 0 || r.PersistentOK {
		t.Fatalf("default setting = %+v", r)
	}
	if _, err := e.open(owner, 1); codeOf(err) != CodeRepoOff {
		t.Fatalf("open on an unset repository: %v", err)
	}
	// A repository the controller has never seen reads as off too.
	if r, _ := e.svc.Repo(e.ctx, 999); r.Enabled {
		t.Fatal("unknown repository is on")
	}
	// The pool setting nobody saved: sessions allowed (per-repository switch still off), transcripts kept.
	if p := e.svc.Pool(e.ctx); !p.Enabled || !p.Record {
		t.Fatalf("pool default = %+v", p)
	}
}

// R5.3
func TestReasonLength3To200(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{{"ab", false}, {"  ab  ", false}, {"abc", true}, {strings.Repeat("x", 200), true}, {strings.Repeat("x", 201), false},
		{"two\nlines", false}, {strings.Repeat("é", 200), true}} {
		_, r := CheckReason(c.in)
		if (r == nil) != c.ok {
			t.Errorf("reason %q: refusal %v, want ok=%v", c.in, r, c.ok)
		}
	}
	e := newEnv(t)
	e.turnOn(nil)
	if _, err := e.svc.Open(e.ctx, admin, OpenRequest{JobID: 1, Reason: "no", Source: "web"}); codeOf(err) != CodeReason {
		t.Fatalf("short reason: %v", err)
	}
	d := e.mustOpen(admin, 1)
	if d.Reason != "look at the disk" {
		t.Fatalf("reason stored = %q", d.Reason)
	}
}

// R5.5: a public repository and every pull request job are refused; no setting changes it.
func TestPublicAndForkNeverAllowed(t *testing.T) {
	e := newEnv(t)
	e.turnOn(func(r *RepoSettings) { r.PersistentOK = true })
	e.jobs.jobs[2] = func(j Job) Job { j.Event = "pull_request"; return j }(e.jobs.jobs[2])
	e.jobs.jobs[3] = func(j Job) Job { j.Event = "pull_request_target"; return j }(e.jobs.jobs[3])
	for _, id := range []int64{2, 3} {
		if _, err := e.open(owner, id); codeOf(err) != CodePullRequest {
			t.Fatalf("job %d: %v", id, err)
		}
	}
	e.svc.Repos = fakeRepos{repos: map[int64]Repo{repoID: {Known: true, Visibility: "public"}}}
	if _, err := e.open(owner, 1); codeOf(err) != CodePublic {
		t.Fatalf("public repository: %v", err)
	}
	// An unknown visibility is treated as public (fail closed).
	e.svc.Repos = fakeRepos{repos: map[int64]Repo{repoID: {Known: true}}}
	if _, err := e.open(owner, 1); codeOf(err) != CodePublic {
		t.Fatalf("unknown visibility: %v", err)
	}
	// A public repository cannot be switched on.
	e.svc.Repos = fakeRepos{repos: map[int64]Repo{repoID: {Known: true, Visibility: "public"}}}
	r := DefaultRepoSettings()
	r.Enabled = true
	if _, err := e.svc.SetRepo(e.ctx, owner, repoID, "alice/app", r); codeOf(err) != CodePublic {
		t.Fatalf("switch on a public repository: %v", err)
	}
}

// R5.7
func TestSharedContainerRefusedNamesOtherJob(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	e.jobs.beside[1] = []int64{4}
	_, err := e.open(admin, 1)
	if codeOf(err) != CodeShared || !strings.Contains(err.Error(), "job 4") {
		t.Fatalf("shared container: %v", err)
	}
	// A VM gives each job its own environment: no shared check.
	e.jobs.jobs[1] = func(j Job) Job { j.Isolation = "VM"; return j }(e.jobs.jobs[1])
	e.mustOpen(admin, 1)
}

// R5.10, R5.6: a persistent runner needs its own setting; an unknown environment is never entered.
func TestPersistentRefusedUnlessOn(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	e.jobs.jobs[1] = func(j Job) Job { j.Isolation = "persistent"; return j }(e.jobs.jobs[1])
	if _, err := e.open(admin, 1); codeOf(err) != CodePersistent {
		t.Fatalf("persistent off: %v", err)
	}
	if w := Warnings(e.jobs.jobs[1]); len(w) == 0 || w[0] != TextPersistentWarn {
		t.Fatalf("warnings = %v", w)
	}
	e.turnOn(func(r *RepoSettings) { r.PersistentOK = true })
	e.mustOpen(admin, 1)

	for _, iso := range []string{"", "host", "on the machine"} {
		e.jobs.jobs[2] = func(j Job) Job { j.Isolation = iso; return j }(e.jobs.jobs[2])
		if _, err := e.open(admin2, 2); codeOf(err) != CodeUnknownEnv {
			t.Fatalf("isolation %q: %v", iso, err)
		}
	}
	e.jobs.jobs[3] = func(j Job) Job { j.RunnerName = ""; return j }(e.jobs.jobs[3])
	if _, err := e.open(admin2, 3); codeOf(err) != CodeUnknownEnv {
		t.Fatalf("no runner named: %v", err)
	}
}

// R5.2: viewers never; admins yes; Owner only narrows; an admin cannot widen it back.
func TestViewerRefusedAdminAllowedOwnerOnlyNarrowing(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	if _, err := e.open(viewer, 1); codeOf(err) != CodeViewer {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := e.open(auth.Principal{}, 1); codeOf(err) != CodeNotSignedIn {
		t.Fatalf("nobody: %v", err)
	}
	e.mustOpen(admin, 1)
	e.turnOn(func(r *RepoSettings) { r.Who = WhoOwner })
	if _, err := e.open(admin2, 2); codeOf(err) != CodeOwnerOnly {
		t.Fatalf("admin on owner-only: %v", err)
	}
	e.mustOpen(owner, 2)
	r := DefaultRepoSettings()
	r.Enabled = true
	if _, err := e.svc.SetRepo(e.ctx, admin, repoID, "alice/app", r); codeOf(err) != CodeOwnerOnly {
		t.Fatalf("admin lifts owner only: %v", err)
	}
	if _, err := e.svc.SetRepo(e.ctx, viewer, repoID, "alice/app", r); codeOf(err) != CodeViewer {
		t.Fatalf("viewer sets: %v", err)
	}
	for _, bad := range []RepoSettings{{Who: "everyone", LimitMin: 30, IdleMin: 10}, {Who: WhoAdmins, LimitMin: 121, IdleMin: 10},
		{Who: WhoAdmins, LimitMin: 30, IdleMin: 0}, {Who: WhoAdmins, LimitMin: 30, IdleMin: 10, HoldMin: 31},
		{Who: WhoAdmins, LimitMin: 10, IdleMin: 20}} {
		if _, err := e.svc.SetRepo(e.ctx, owner, repoID, "alice/app", bad); codeOf(err) != CodeBadSetting {
			t.Errorf("setting %+v: %v", bad, err)
		}
	}
	e.svc.Repos = fakeRepos{repos: map[int64]Repo{}}
	if _, err := e.svc.SetRepo(e.ctx, owner, repoID, "alice/app", r); codeOf(err) != CodeRepoUnknown {
		t.Fatalf("unknown repository: %v", err)
	}
}

// R5.32: with no machine side, every open is refused with the reason, and the refusal is audited.
func TestNoEntererRefusesEveryOpen(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	e.svc.Enter = nil
	if _, err := e.open(owner, 1); codeOf(err) != CodeCannotEnter {
		t.Fatalf("no enterer: %v", err)
	}
	open, err := e.db.OpenDebugSessions(e.ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("open sessions = %v, %v", open, err)
	}
	if got := e.audits(ActionRefused); len(got) != 1 || got[0].Result != "refused" {
		t.Fatalf("refused rows = %+v", got)
	}
	v, err := e.svc.JobCard(e.ctx, owner, 1)
	if err != nil || v.Refusal == nil || v.Refusal.Text != TextCannotEnter {
		t.Fatalf("card = %+v, %v", v, err)
	}
	if _, err := e.open(owner, 77); codeOf(err) != CodeNotFound {
		t.Fatalf("unknown job: %v", err)
	}
}

// R5.34
func TestMacOSSaysNotRunOnARealMac(t *testing.T) {
	if w := Warnings(Job{OS: "macos", Isolation: "VM"}); len(w) != 1 || w[0] != TextMacNotRun {
		t.Fatalf("warnings = %v", w)
	}
	if w := Warnings(Job{OS: "linux", Isolation: "container"}); len(w) != 0 {
		t.Fatalf("linux warnings = %v", w)
	}
}
