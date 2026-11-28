package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/debugaccess"
)

// debugStub answers like the service for the routes; the service's own rules
// are proven in internal/debugaccess.
type debugStub struct {
	pool    debugaccess.PoolSettings
	open    int
	refuse  *debugaccess.Refusal
	opened  []debugaccess.OpenRequest
	ended   []string
	setPool []bool
}

var t0 = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func (d *debugStub) Pool(context.Context) debugaccess.PoolSettings { return d.pool }
func (d *debugStub) OpenCount(context.Context) (int, error)        { return d.open, nil }
func (d *debugStub) SetPool(_ context.Context, _ auth.Principal, next debugaccess.PoolSettings, confirm bool) (int, error) {
	if !next.Enabled && d.open > 0 && !confirm {
		return 0, &debugaccess.Refusal{Code: debugaccess.CodeNeedsConfirm, Text: "1 debug sessions are open. They end at once."}
	}
	d.setPool = append(d.setPool, next.Enabled)
	d.pool = next
	n := d.open
	d.open = 0
	return n, nil
}
func (d *debugStub) Repo(context.Context, int64) (debugaccess.RepoSettings, error) {
	return debugaccess.DefaultRepoSettings(), nil
}
func (d *debugStub) RepoInfo(_ context.Context, id int64) (debugaccess.Repo, error) {
	if id == 7 {
		return debugaccess.Repo{Known: true, FullName: "alice/site", Visibility: "public"}, nil
	}
	return debugaccess.Repo{Known: id == 100, FullName: "alice/app", Visibility: "private"}, nil
}
func (d *debugStub) SetRepo(_ context.Context, _ auth.Principal, _ int64, next debugaccess.RepoSettings) (debugaccess.RepoSettings, error) {
	if err := next.Validate(); err != nil {
		return debugaccess.RepoSettings{}, err
	}
	return next, nil
}
func (d *debugStub) JobCard(_ context.Context, _ auth.Principal, id int64) (debugaccess.JobView, error) {
	return debugaccess.JobView{Job: debugaccess.Job{ID: id}, Repo: debugaccess.DefaultRepoSettings(),
		Refusal: &debugaccess.Refusal{Code: debugaccess.CodeRepoOff, Text: debugaccess.TextRepoOff}}, nil
}
func (d *debugStub) Open(_ context.Context, p auth.Principal, req debugaccess.OpenRequest) (debugaccess.Session, error) {
	if d.refuse != nil {
		return debugaccess.Session{}, d.refuse
	}
	d.opened = append(d.opened, req)
	return debugaccess.Session{ID: "dbg_1", JobID: req.JobID, Repository: "alice/app", UserLogin: p.Login, Reason: req.Reason,
		Source: req.Source, OpenedAt: t0, EndsAt: t0.Add(30 * time.Minute), Recorded: true}, nil
}
func (d *debugStub) Session(_ context.Context, id string) (debugaccess.Session, error) {
	if id != "dbg_1" {
		return debugaccess.Session{}, &debugaccess.Refusal{Code: debugaccess.CodeNotFound, Text: "No such debug session."}
	}
	return debugaccess.Session{ID: id, JobID: 5, Repository: "alice/app", UserLogin: "tester", Reason: "disk", Source: "web",
		SourceAddr: "192.0.2.9", OpenedAt: t0, EndsAt: t0.Add(30 * time.Minute), EndedAt: t0.Add(time.Minute), EndReason: debugaccess.EndUser}, nil
}
func (d *debugStub) Sessions(ctx context.Context, _ int) ([]debugaccess.Session, error) {
	s, _ := d.Session(ctx, "dbg_1")
	return []debugaccess.Session{s}, nil
}
func (d *debugStub) Extend(context.Context, auth.Principal, string) (debugaccess.Session, error) {
	return debugaccess.Session{}, &debugaccess.Refusal{Code: debugaccess.CodeExtended, Text: "A session can be extended once."}
}
func (d *debugStub) End(ctx context.Context, _ auth.Principal, id string) (debugaccess.Session, error) {
	d.ended = append(d.ended, id)
	return d.Session(ctx, id)
}
func (d *debugStub) Transcript(context.Context, auth.Principal, string) (debugaccess.Transcript, bool, error) {
	return debugaccess.Transcript{SessionID: "dbg_1", Text: "$ ls\n", ExpiresAt: t0.Add(debugaccess.TranscriptKeep)}, true, nil
}

func debugMux(stub *debugStub, role auth.Role) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Debug: stub, Authn: asRole(role)})
	return mux
}

// R5.2, R5.30: viewers read but never open, extend, end or read a transcript; the kill switch is the owner's.
func TestViewerRefusedAdminAllowedOwnerOnlyNarrowing(t *testing.T) {
	stub := &debugStub{pool: debugaccess.DefaultPoolSettings()}
	v := debugMux(stub, auth.RoleViewer)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/jobs/5/debug-sessions", `{"reason":"look at the disk"}`},
		{http.MethodPost, "/api/v1/debug-sessions/dbg_1/extend", ""},
		{http.MethodPost, "/api/v1/debug-sessions/dbg_1/end", ""},
		{http.MethodGet, "/api/v1/debug-sessions/dbg_1/transcript", ""},
		{http.MethodPut, "/api/v1/repositories/100/debug-access", `{"enabled":true,"who":"admins","limit_min":30,"idle_min":10,"hold_min":0,"persistent_ok":false}`},
		{http.MethodPut, "/api/v1/debug-access", `{"enabled":false,"record":true}`},
	} {
		if rec := do(v, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d", c.method, c.path, rec.Code)
		}
	}
	for _, path := range []string{"/api/v1/debug-sessions", "/api/v1/debug-sessions/dbg_1", "/api/v1/jobs/5/debug", "/api/v1/debug-access"} {
		if rec := do(v, http.MethodGet, path, ""); rec.Code != 200 {
			t.Errorf("viewer GET %s = %d %s", path, rec.Code, rec.Body)
		}
	}
	if len(stub.opened)+len(stub.ended) != 0 {
		t.Fatal("a viewer reached the service")
	}
	a := debugMux(stub, auth.RoleAdmin)
	if rec := do(a, http.MethodPut, "/api/v1/debug-access", `{"enabled":false,"record":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("admin kill switch = %d", rec.Code)
	}
	rec := do(a, http.MethodPost, "/api/v1/jobs/5/debug-sessions", `{"reason":"look at the disk"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"state":"open"`) {
		t.Fatalf("admin open = %d %s", rec.Code, rec.Body)
	}
	if len(stub.opened) != 1 || stub.opened[0].Source != "cli" || stub.opened[0].Reason != "look at the disk" {
		t.Fatalf("opened = %+v", stub.opened)
	}
	// A refusal keeps the service's words and maps to its status.
	stub.refuse = &debugaccess.Refusal{Code: debugaccess.CodeOwnerOnly, Text: debugaccess.TextOwnerOnly}
	if rec := do(a, http.MethodPost, "/api/v1/jobs/5/debug-sessions", `{"reason":"look"}`); rec.Code != 403 || !strings.Contains(rec.Body.String(), debugaccess.TextOwnerOnly) {
		t.Fatalf("owner only = %d %s", rec.Code, rec.Body)
	}
	stub.refuse = &debugaccess.Refusal{Code: debugaccess.CodeJobCap, Text: "admin2 has a session open on this job."}
	if rec := do(a, http.MethodPost, "/api/v1/jobs/5/debug-sessions", `{"reason":"look"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "admin2") {
		t.Fatalf("job cap = %d %s", rec.Code, rec.Body)
	}
	if rec := do(a, http.MethodPost, "/api/v1/debug-sessions/dbg_1/extend", ""); rec.Code != 409 {
		t.Fatalf("second extend = %d", rec.Code)
	}
	if rec := do(a, http.MethodGet, "/api/v1/debug-sessions/nope", ""); rec.Code != 404 {
		t.Fatalf("unknown session = %d", rec.Code)
	}
}

// R5.26: turning sessions off asks first when one is open, then ends them.
func TestKillSwitchEndsAll(t *testing.T) {
	stub := &debugStub{pool: debugaccess.DefaultPoolSettings(), open: 1}
	o := debugMux(stub, auth.RoleOwner)
	if rec := do(o, http.MethodPut, "/api/v1/debug-access", `{"enabled":false,"record":true}`); rec.Code != 409 ||
		!strings.Contains(rec.Body.String(), "needs_confirmation") {
		t.Fatalf("no confirm = %d %s", rec.Code, rec.Body)
	}
	rec := do(o, http.MethodPut, "/api/v1/debug-access", `{"enabled":false,"record":true,"confirm":true}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ended":1`) || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("kill switch = %d %s", rec.Code, rec.Body)
	}
}

// The repository setting says what it allows and never allows (R5.14); a public repository says why it is off.
func TestRepoDebugAccessWords(t *testing.T) {
	o := debugMux(&debugStub{}, auth.RoleOwner)
	rec := do(o, http.MethodGet, "/api/v1/repositories/100/debug-access", "")
	for _, want := range []string{`"enabled":false`, `"who":"admins"`, `"limit_min":30`, `not its secrets`, `does not widen that`} {
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("get lacks %s: %d %s", want, rec.Code, rec.Body)
		}
	}
	if rec := do(o, http.MethodGet, "/api/v1/repositories/7/debug-access", ""); !strings.Contains(rec.Body.String(), debugaccess.TextPublic) {
		t.Fatalf("public = %s", rec.Body)
	}
	if rec := do(o, http.MethodGet, "/api/v1/repositories/55/debug-access", ""); rec.Code != 404 {
		t.Fatalf("unknown repository = %d", rec.Code)
	}
	bad := `{"enabled":true,"who":"admins","limit_min":500,"idle_min":10,"hold_min":0,"persistent_ok":false}`
	if rec := do(o, http.MethodPut, "/api/v1/repositories/100/debug-access", bad); rec.Code != 400 {
		t.Fatalf("limit 500 = %d", rec.Code)
	}
}

// R6.5: no source address and no secret in an API answer; the transcript says it is cleaned as far as possible.
func TestDebugAnswersCarryNoAddress(t *testing.T) {
	o := debugMux(&debugStub{}, auth.RoleOwner)
	for _, path := range []string{"/api/v1/debug-sessions", "/api/v1/debug-sessions/dbg_1"} {
		if rec := do(o, http.MethodGet, path, ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "192.0.2.9") {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
	}
	rec := do(o, http.MethodGet, "/api/v1/debug-sessions/dbg_1/transcript", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Cleaned as far as possible") {
		t.Fatalf("transcript = %d %s", rec.Code, rec.Body)
	}
	off := http.NewServeMux()
	Register(off, Options{Version: "t", DB: fakeDB{}, Authn: asRole(auth.RoleOwner)})
	if rec := do(off, http.MethodGet, "/api/v1/debug-sessions", ""); rec.Code != 501 {
		t.Fatalf("not wired = %d", rec.Code)
	}
}
