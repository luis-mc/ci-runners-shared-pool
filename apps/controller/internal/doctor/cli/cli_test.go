package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

func sp(s string) *string { return &s }

func finding(check string, sev gen.Severity, name, title string) gen.DoctorFinding {
	f := gen.DoctorFinding{Check: check, Severity: sev, Title: title, Milestone: 1, CheckedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	f.Subject.Kind, f.Subject.Id, f.Subject.Name = "machine", "m1", name
	f.Fix.Text = "do the thing"
	return f
}

func runWith(worst gen.Severity, fs ...gen.DoctorFinding) gen.DoctorRun {
	w := worst
	counts := map[string]int{}
	for _, f := range fs {
		counts[string(f.Severity)]++
	}
	return gen.DoctorRun{Id: "dr_1", Trigger: "cli", State: gen.DoctorRunStateDone, StartedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		Worst: &w, Counts: counts, Findings: fs}
}

type fake struct {
	run      gen.DoctorRun
	startErr error
	scope    gen.DoctorScope
	polls    int
	resolve  func([]string) ([]int64, error)
}

func (f *fake) StartRun(_ context.Context, s gen.DoctorScope) (gen.DoctorRun, error) {
	f.scope = s
	if f.startErr != nil {
		return gen.DoctorRun{}, f.startErr
	}
	r := f.run
	if f.polls > 0 {
		r.State = gen.DoctorRunStateRunning
	}
	return r, nil
}
func (f *fake) GetRun(context.Context, string) (gen.DoctorRun, error) {
	f.polls--
	r := f.run
	if f.polls > 0 {
		r.State = gen.DoctorRunStateRunning
	}
	return r, nil
}
func (f *fake) ResolveRepositories(_ context.Context, n []string) ([]int64, error) {
	if f.resolve != nil {
		return f.resolve(n)
	}
	return []int64{1}, nil
}

func do(c Client, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, &out, &errb, c, Options{Poll: time.Millisecond})
	return code, out.String(), errb.String()
}

// Port of test_cli_exit_codes_and_output; the brief decides: fail exits 1, warn exits 0.
func TestExitCodes(t *testing.T) {
	cases := []struct {
		worst gen.Severity
		want  int
	}{{gen.SeverityOk, 0}, {gen.SeverityInfo, 0}, {gen.SeveritySkip, 0}, {gen.SeverityWarn, 0}, {gen.SeverityFail, 1}}
	for _, tc := range cases {
		code, _, _ := do(&fake{run: runWith(tc.worst, finding("machine.disk", tc.worst, "alpha", "x"))})
		if code != tc.want {
			t.Errorf("worst %s: exit %d, want %d", tc.worst, code, tc.want)
		}
	}
	if code, _, e := do(&fake{startErr: errors.New("not reachable")}); code != ExitCannotRun || !strings.Contains(e, "not reachable") {
		t.Errorf("start error: %d %q", code, e)
	}
	if code, _, _ := do(&fake{}, "--min-severity", "loud"); code != ExitCannotRun {
		t.Errorf("bad min severity: %d", code)
	}
	if code, _, _ := do(&fake{}, "--nope"); code != ExitCannotRun {
		t.Errorf("bad flag: %d", code)
	}
	failed := runWith(gen.SeverityOk)
	failed.State = gen.DoctorRunStateFailed
	if code, _, _ := do(&fake{run: failed}); code != ExitCannotRun {
		t.Errorf("failed run: %d", code)
	}
}

func TestFlagsAndTextOutput(t *testing.T) {
	f := &fake{run: runWith(gen.SeverityFail,
		finding("machine.online", gen.SeverityOk, "good", "good is online"),
		finding("machine.info", gen.SeverityInfo, "inf", "just so you know"),
		finding("machine.skip", gen.SeveritySkip, "skp", "not checked"),
		finding("machine.disk", gen.SeverityWarn, "warned", "low disk"),
		finding("runner.service", gen.SeverityFail, "failed", "service stopped"))}
	f.run.Findings[4].Fix.Command = sp("crsp x")
	f.run.Findings[4].Fix.Link = sp("/runners")
	f.run.Findings[4].Detail = sp("line one\nline two")
	code, out, _ := do(f, "--machine", "alpha", "--machine", "beta", "--repo", "o/r", "--check", "runner", "--check", "machine.disk")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if got := *f.scope.Machines; len(got) != 2 || got[1] != "beta" {
		t.Errorf("machines = %v", got)
	}
	if got := *f.scope.Checks; len(got) != 2 || got[0] != "runner" {
		t.Errorf("checks = %v", got)
	}
	if got := *f.scope.Repositories; len(got) != 1 || got[0] != 1 {
		t.Errorf("repos = %v", got)
	}
	for _, want := range []string{"FAIL  runner.service  failed", "service stopped", "line two", "Fix: do the thing", "Run: crsp x", "See: /runners", "WARN  machine.disk", "1 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"good is online", "just so you know", "not checked"} {
		if strings.Contains(out, hidden) {
			t.Errorf("default output shows %q", hidden)
		}
	}
	if strings.Index(out, "FAIL") > strings.Index(out, "WARN") {
		t.Error("worst must come first")
	}
	_, out, _ = do(f, "--min-severity", "fail")
	if strings.Contains(out, "WARN") || !strings.Contains(out, "FAIL") {
		t.Errorf("min-severity fail:\n%s", out)
	}
	_, out, _ = do(f, "--min-severity", "info")
	if !strings.Contains(out, "just so you know") || !strings.Contains(out, "not checked") {
		t.Errorf("min-severity info:\n%s", out)
	}
}

// R5.48: the items of a finding are listed under it in the text and carried in the JSON document.
func TestItemsAreListedAndCarried(t *testing.T) {
	f := finding("machine.runner_leftovers", gen.SeverityFail, "alpha", "1 runner leftover no controller owns on alpha")
	f.Detail = sp("Service actions.runner.old: no owner")
	items := []gen.RunnerLeftover{
		{Kind: gen.Service, Name: "actions.runner.old", Why: "no owner"},
		{Kind: gen.Folder, Name: `C:\crsp\runners\old`, Why: "no runner uses it"},
	}
	f.Items = &items
	f.Action = &struct {
		Args  *gen.RegistrationActionArgs `json:"args,omitempty"`
		Id    gen.DoctorFindingActionId   `json:"id"`
		Label string                      `json:"label"`
	}{Id: gen.DoctorFindingActionIdRemoveRunnerLeftovers, Label: "Remove"}
	run := runWith(gen.SeverityFail, f)
	var buf bytes.Buffer
	Text(&buf, run, "warn")
	out := buf.String()
	if !strings.Contains(out, `- folder C:\crsp\runners\old: no runner uses it`) || strings.Contains(out, "- service actions.runner.old") {
		t.Fatalf("text:\n%s", out)
	}
	b, err := json.Marshal(JSON(run))
	if err != nil || !strings.Contains(string(b), `"items":[`) || !strings.Contains(string(b), `"action":{"id":"remove_runner_leftovers","label":"Remove"}`) {
		t.Fatalf("json %s %v", b, err)
	}
}

func TestNothingToShow(t *testing.T) {
	_, out, _ := do(&fake{run: runWith(gen.SeverityOk, finding("machine.online", gen.SeverityOk, "a", "fine"))})
	if !strings.Contains(out, "Nothing at warn or worse.") || !strings.Contains(out, "1 ok") {
		t.Errorf("output:\n%s", out)
	}
}

func TestJSONHasEveryFinding(t *testing.T) {
	f := &fake{run: runWith(gen.SeverityWarn,
		finding("machine.online", gen.SeverityOk, "a", "fine"),
		finding("machine.disk", gen.SeverityWarn, "b", "low"))}
	code, out, _ := do(f, "--json", "--min-severity", "fail")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	if doc["schema"] != float64(1) || doc["run_id"] != "dr_1" || doc["worst"] != "warn" || len(doc["findings"].([]any)) != 2 {
		t.Fatalf("doc = %v", doc)
	}
	first := doc["findings"].([]any)[0].(map[string]any)
	fix := first["fix"].(map[string]any)
	if fix["command"] != nil || fix["link"] != nil || first["subject"].(map[string]any)["name"] != "a" {
		t.Errorf("finding = %v", first)
	}
	if _, ok := doc["scope"].(map[string]any)["machines"].([]any); !ok {
		t.Errorf("scope lists must be arrays: %v", doc["scope"])
	}
}

func TestWaitsForRunningRun(t *testing.T) {
	f := &fake{run: runWith(gen.SeverityOk), polls: 3}
	if code, _, _ := do(f); code != 0 || f.polls > 0 {
		t.Fatalf("exit %d polls left %d", code, f.polls)
	}
}

func TestBadRepoAndLocal(t *testing.T) {
	f := &fake{run: runWith(gen.SeverityOk), resolve: func([]string) ([]int64, error) { return nil, errors.New("no repository") }}
	if code, _, e := do(f, "--repo", "o/x"); code != ExitCannotRun || !strings.Contains(e, "no repository") {
		t.Errorf("repo: %d %q", code, e)
	}
	if code, _, e := do(f, "--local"); code != ExitCannotRun || !strings.Contains(e, "not built yet") {
		t.Errorf("local: %d %q", code, e)
	}
}

func TestHTTPClient(t *testing.T) {
	var gotAuth, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/doctor/runs", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		gotBody = b.String()
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"id":"dr_9","trigger":"cli","state":"done","started_at":"2026-11-14T10:00:00Z","counts":{},"findings":[]}`))
	})
	mux.HandleFunc("GET /api/v1/github/repositories", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":42,"full_name":"Owner/Repo"}]}`))
	})
	mux.HandleFunc("GET /api/v1/doctor/runs/nope", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL + "/", Token: "tok"}
	run, err := c.StartRun(context.Background(), gen.DoctorScope{})
	if err != nil || run.Id != "dr_9" || gotAuth != "Bearer tok" || gotBody != "{}" {
		t.Fatalf("run=%+v err=%v auth=%q body=%q", run, err, gotAuth, gotBody)
	}
	ids, err := c.ResolveRepositories(context.Background(), []string{"owner/repo"})
	if err != nil || len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if _, err := c.ResolveRepositories(context.Background(), []string{"nope"}); err == nil {
		t.Error("want error for a name without owner")
	}
	if _, err := c.GetRun(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("401: %v", err)
	}
	dead := &HTTPClient{BaseURL: "http://127.0.0.1:1"}
	if _, err := dead.StartRun(context.Background(), gen.DoctorScope{}); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("unreachable: %v", err)
	}
}
