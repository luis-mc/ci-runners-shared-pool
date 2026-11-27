package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// job makes a finished job that waited queue seconds and ran run seconds.
func job(name string, labels []string, queue, run int, conclusion string) Job {
	return Job{Name: name, Labels: labels, Conclusion: conclusion, CreatedAt: t0,
		StartedAt: t0.Add(time.Duration(queue) * time.Second), CompletedAt: t0.Add(time.Duration(queue+run) * time.Second)}
}

var (
	crspL   = []string{"self-hosted", "linux", "crsp"}
	hostedL = []string{"ubuntu-latest"}
	winL    = []string{"windows-latest"}
)

func prices(t *testing.T) Prices {
	t.Helper()
	p, err := DefaultPrices()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMedianAndP95(t *testing.T) {
	odd := stat([]float64{50, 10, 30})
	if odd.Median != 30 || odd.Count != 3 {
		t.Fatalf("odd median: %+v", odd)
	}
	even := stat([]float64{40, 10, 20, 30})
	if even.Median != 25 {
		t.Fatalf("even median: %+v", even)
	}
	var v []float64
	for i := 1; i <= 100; i++ {
		v = append(v, float64(i))
	}
	if got := stat(v).P95; got != 95 {
		t.Fatalf("p95 of 1..100 = %v, want 95", got)
	}
	if got := stat([]float64{7}).P95; got != 7 {
		t.Fatalf("p95 of one = %v", got)
	}
	if got := stat(nil); got.Count != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

func TestMatchingByNameAcrossKinds(t *testing.T) {
	jobs := []Job{
		job("build", crspL, 5, 100, "success"),
		job("build", hostedL, 20, 200, "success"),
		job("lint", crspL, 5, 30, "success"),     // crsp only
		job("deploy", hostedL, 9, 60, "success"), // hosted only
		job("build", crspL, 5, 100, "cancelled"), // ignored
		job("build", hostedL, 5, 100, "skipped"), // ignored
	}
	rows, unmatched := Build(jobs, prices(t))
	if len(rows) != 1 || rows[0].Job != "build" {
		t.Fatalf("rows: %+v", rows)
	}
	if unmatched != 2 {
		t.Fatalf("unmatched = %d, want 2", unmatched)
	}
	if rows[0].Crsp.Run.Count != 1 || rows[0].Hosted.Run.Count != 1 {
		t.Fatalf("cancelled or skipped jobs counted: %+v", rows[0])
	}
}

func TestBadTimestampsIgnored(t *testing.T) {
	bad := job("build", crspL, 5, 100, "success")
	bad.StartedAt = time.Time{}
	rev := job("build", hostedL, 5, 100, "success")
	rev.CompletedAt = rev.StartedAt.Add(-time.Second)
	rows, _ := Build([]Job{bad, rev}, prices(t))
	if len(rows) != 0 {
		t.Fatalf("rows from bad data: %+v", rows)
	}
}

func TestRowStatsAndMinutesSaved(t *testing.T) {
	var jobs []Job
	for _, run := range []int{100, 110, 120} {
		jobs = append(jobs, job("test", crspL, 10, run, "success"))
	}
	for _, run := range []int{300, 340, 400, 500} {
		jobs = append(jobs, job("test", hostedL, 30, run, "failure"))
	}
	rows, _ := Build(jobs, prices(t))
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.Crsp.Run.Median != 110 || r.Crsp.Run.P95 != 120 {
		t.Errorf("crsp run: %+v", r.Crsp.Run)
	}
	if r.Hosted.Run.Median != 370 || r.Hosted.Run.P95 != 500 {
		t.Errorf("hosted run: %+v", r.Hosted.Run)
	}
	if r.Hosted.Queue.Median != 30 || r.Crsp.Queue.Median != 10 {
		t.Errorf("queue: %+v %+v", r.Crsp.Queue, r.Hosted.Queue)
	}
	// (370+30) - (110+10) = 280 s per run, 3 crsp runs: 840 s = 14 min.
	if r.MinutesSaved != 14 {
		t.Errorf("minutes saved = %v, want 14", r.MinutesSaved)
	}
}

func TestSlowerCrspSavesNegative(t *testing.T) {
	rows, _ := Build([]Job{job("x", crspL, 0, 120, "success"), job("x", hostedL, 0, 60, "success")}, prices(t))
	if rows[0].MinutesSaved != -1 {
		t.Fatalf("saved = %v, want -1", rows[0].MinutesSaved)
	}
}

func TestCostMath(t *testing.T) {
	// crsp runs of 61 s (2 billed min), 30 s (1) and 120 s (2): 5 min at $0.008.
	jobs := []Job{
		job("a", crspL, 0, 61, "success"), job("a", crspL, 0, 30, "success"), job("a", crspL, 0, 120, "success"),
		job("a", hostedL, 0, 100, "success"),
		// windows price, 1 crsp run of 90 s = 2 min at $0.016.
		job("w", crspL, 0, 90, "success"), job("w", winL, 0, 100, "success"),
	}
	rows, _ := Build(jobs, prices(t))
	byName := map[string]Row{}
	for _, r := range rows {
		byName[r.Job] = r
	}
	if got := byName["a"].CostAvoided; got != 0.04 {
		t.Errorf("a cost = %v, want 0.04", got)
	}
	if got := byName["w"].CostAvoided; got != 0.03 { // 0.032 rounds to 0.03
		t.Errorf("w cost = %v, want 0.03", got)
	}
}

func TestUnknownLabelUsesDefault(t *testing.T) {
	p := prices(t)
	if got := p.For([]string{"some-new-image"}); got != p.DefaultPerMinute {
		t.Fatalf("default not used: %v", got)
	}
	if got := p.For([]string{"macos-latest"}); got != 0.08 {
		t.Fatalf("macos = %v", got)
	}
}

func TestParsePricesRefusesBadTable(t *testing.T) {
	for _, in := range []string{`{`, `{"default_per_minute":0}`, `{"default_per_minute":1,"per_minute":{"x":-1}}`} {
		if _, err := ParsePrices([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}

type fakeSource struct {
	vis     string
	jobs    []Job
	jobsErr error
	jobCall int
}

func (f *fakeSource) Visibility(context.Context, string, string) (string, error) { return f.vis, nil }
func (f *fakeSource) Jobs(context.Context, string, string, time.Time) ([]Job, error) {
	f.jobCall++
	return f.jobs, f.jobsErr
}

func svc(src Source, t *testing.T) *Service {
	return &Service{Source: src, Prices: prices(t), Now: func() time.Time { return t0 }}
}

func TestPublicRepositoryExcluded(t *testing.T) {
	src := &fakeSource{vis: VisibilityPublic, jobs: []Job{job("a", crspL, 0, 60, "success"), job("a", hostedL, 0, 60, "success")}}
	rep, err := svc(src, t).Compare(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Excluded || rep.Reason == "" || len(rep.Rows) != 0 || rep.TotalCostAvoided != 0 {
		t.Fatalf("public not excluded: %+v", rep)
	}
	if src.jobCall != 0 {
		t.Fatal("read jobs of a public repository")
	}
	var buf bytes.Buffer
	if err := WriteTable(&buf, rep); err != nil || !strings.Contains(buf.String(), "free") {
		t.Fatalf("table: %q %v", buf.String(), err)
	}
}

func TestInternalRepositoryIsBilled(t *testing.T) {
	src := &fakeSource{vis: VisibilityInternal, jobs: []Job{job("a", crspL, 0, 60, "success"), job("a", hostedL, 0, 120, "success")}}
	rep, err := svc(src, t).Compare(context.Background(), "o", "r", 7)
	if err != nil || rep.Excluded || len(rep.Rows) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.TotalCostAvoided != 0.01 || rep.TotalMinutesSaved != 1 {
		t.Fatalf("totals: %+v", rep)
	}
}

func TestDaysDefaultAndCap(t *testing.T) {
	s := svc(&fakeSource{vis: VisibilityPrivate}, t)
	for in, want := range map[int]int{0: DefaultDays, -3: DefaultDays, 30: 30, 500: MaxDays} {
		rep, err := s.Compare(context.Background(), "o", "r", in)
		if err != nil || rep.Days != want {
			t.Errorf("days %d: got %d %v", in, rep.Days, err)
		}
	}
	rep, _ := s.Compare(context.Background(), "o", "r", 10)
	if !rep.Since.Equal(t0.AddDate(0, 0, -10)) {
		t.Errorf("since = %v", rep.Since)
	}
	if rep.Rows == nil {
		t.Error("rows must be an empty list, not null")
	}
}

func TestSourceErrorPassesUp(t *testing.T) {
	boom := errors.New("boom")
	if _, err := svc(&fakeSource{vis: VisibilityPrivate, jobsErr: boom}, t).Compare(context.Background(), "o", "r", 7); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc(&fakeSource{}, t).Compare(context.Background(), "", "r", 7); err == nil {
		t.Fatal("empty owner accepted")
	}
}

func TestTable(t *testing.T) {
	rows, _ := Build([]Job{job("build", crspL, 5, 100, "success"), job("build", hostedL, 20, 200, "success")}, prices(t))
	rep := Report{Repository: "o/r", Days: 7, Rows: rows, TotalMinutesSaved: 1.5, TotalCostAvoided: 0.02}
	var buf bytes.Buffer
	if err := WriteTable(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"JOB", "build", "1/1", "1m40s", "3m20s", "$0.02", "o/r, last 7 days"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	_ = WriteTable(&buf, Report{Repository: "o/r", Days: 7, Unmatched: 3})
	if !strings.Contains(buf.String(), "no job ran on both") {
		t.Errorf("empty table: %q", buf.String())
	}
}

// fakeGitHub serves the three endpoints the API source reads.
func fakeGitHub(t *testing.T, visibility string, hits *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"visibility": visibility})
	})
	mux.HandleFunc("/repos/o/gone", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.URL.Path+"?"+r.URL.Query().Get("created")+"&"+r.URL.Query().Get("status"))
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []map[string]any{{"id": 1}, {"id": 2}}})
	})
	mux.HandleFunc("/repos/o/r/actions/runs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cred" || r.Header.Get("X-GitHub-Api-Version") == "" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var labels []string
		if strings.Contains(r.URL.Path, "/1/") {
			labels = crspL
		} else {
			labels = hostedL
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []map[string]any{{
			"name": "build", "labels": labels, "conclusion": "success",
			"created_at": t0.Format(time.RFC3339), "started_at": t0.Add(5 * time.Second).Format(time.RFC3339),
			"completed_at": t0.Add(65 * time.Second).Format(time.RFC3339),
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAPISourceEndToEnd(t *testing.T) {
	var hits []string
	srv := fakeGitHub(t, "private", &hits)
	api := &API{Base: srv.URL, Bearer: func(context.Context, string, string) (string, error) { return "cred", nil }}
	s := &Service{Source: api, Prices: prices(t), Now: func() time.Time { return t0 }}
	rep, err := s.Compare(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Crsp.Run.Median != 60 || rep.Rows[0].Hosted.Run.Count != 1 {
		t.Fatalf("report: %+v", rep)
	}
	if want := fmt.Sprintf("/repos/o/r/actions/runs?>=%s&completed", t0.AddDate(0, 0, -7).Format("2006-01-02")); !contains(hits, want) {
		t.Fatalf("window not sent: %v want %s", hits, want)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestAPISourceErrors(t *testing.T) {
	var hits []string
	srv := fakeGitHub(t, "public", &hits)
	cred := func(context.Context, string, string) (string, error) { return "cred", nil }
	api := &API{Base: srv.URL, Bearer: cred}
	if _, err := api.Visibility(context.Background(), "o", "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if v, err := api.Visibility(context.Background(), "o", "r"); err != nil || v != VisibilityPublic {
		t.Fatalf("visibility %q %v", v, err)
	}
	bad := &API{Base: srv.URL, Bearer: func(context.Context, string, string) (string, error) { return "wrong", nil }}
	if _, err := bad.Jobs(context.Background(), "o", "r", t0); err == nil {
		t.Fatal("401 not reported")
	}
	if _, err := (&API{Base: srv.URL}).Visibility(context.Background(), "o", "r"); err == nil {
		t.Fatal("missing credential source accepted")
	}
	failing := &API{Base: srv.URL, Bearer: func(context.Context, string, string) (string, error) { return "", errors.New("no install") }}
	if _, err := failing.Visibility(context.Background(), "o", "r"); err == nil {
		t.Fatal("credential error hidden")
	}
}

func TestPrivateFlagWithoutVisibility(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"private":true}`))
	}))
	defer srv.Close()
	api := &API{Base: srv.URL, Bearer: func(context.Context, string, string) (string, error) { return "c", nil }}
	if v, err := api.Visibility(context.Background(), "o", "r"); err != nil || v != VisibilityPrivate {
		t.Fatalf("%q %v", v, err)
	}
}
