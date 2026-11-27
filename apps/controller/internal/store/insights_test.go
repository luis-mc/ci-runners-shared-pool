package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

var tBase = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func fnd(kind insights.Kind, subject string, mins int) insights.Finding {
	return insights.Finding{
		Kind: kind, Scope: insights.ScopeRepo, RepositoryID: 10, Repository: "acme/api", Subject: subject,
		Area: insights.AreaWaits, Title: "t " + subject, Fix: "f " + subject, Snippet: "run: x",
		Evidence: json.RawMessage(`{"n":3}`), MinutesSavedWeek: mins, Guardrail: "G2", Action: insights.ActionLightLane,
	}
}

// R5.7: every field of a finding survives the store.
func TestFindingRoundTrip(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	in := fnd(insights.KindWaitHold, "ci/build", 42)
	res, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, []insights.Finding{in}, tBase)
	if err != nil || res.Opened != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	items, total, next, err := fs.List(ctx, insights.Filter{})
	if err != nil || total != 1 || next != "" || len(items) != 1 {
		t.Fatalf("%v %d %q %v", items, total, next, err)
	}
	got := items[0]
	if string(got.Evidence) != `{"n":3}` {
		t.Fatalf("evidence %s", got.Evidence)
	}
	if !reflect.DeepEqual(got.Finding, in) {
		t.Fatalf("got %+v want %+v", got.Finding, in)
	}
	if !got.FirstSeen.Equal(tBase) || !got.LastSeen.Equal(tBase) || !got.ResolvedAt.IsZero() {
		t.Fatalf("times %+v", got)
	}
	one, err := fs.Get(ctx, got.ID)
	if err != nil || one.Subject != "ci/build" {
		t.Fatalf("%v %v", one, err)
	}
}

// R5.8: seen again refreshes; no longer reported resolves; seen once more opens a new row; history stays.
func TestResolvedAndReopened(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	kinds := []insights.Kind{insights.KindWaitHold}
	a, b := fnd(insights.KindWaitHold, "a", 10), fnd(insights.KindWaitHold, "b", 20)
	if _, err := fs.Sync(ctx, kinds, []insights.Finding{a, b}, tBase); err != nil {
		t.Fatal(err)
	}
	a.MinutesSavedWeek = 15
	r, err := fs.Sync(ctx, kinds, []insights.Finding{a}, tBase.Add(time.Hour))
	if err != nil || r.Updated != 1 || r.Resolved != 1 || r.Opened != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	open, _, _, _ := fs.List(ctx, insights.Filter{})
	if len(open) != 1 || open[0].MinutesSavedWeek != 15 || !open[0].LastSeen.Equal(tBase.Add(time.Hour)) || !open[0].FirstSeen.Equal(tBase) {
		t.Fatalf("%+v", open)
	}
	done, total, _, _ := fs.List(ctx, insights.Filter{Resolved: true})
	if total != 1 || done[0].Subject != "b" || !done[0].ResolvedAt.Equal(tBase.Add(time.Hour)) {
		t.Fatalf("%+v", done)
	}
	r, err = fs.Sync(ctx, kinds, []insights.Finding{a, b}, tBase.Add(2*time.Hour))
	if err != nil || r.Opened != 1 || r.Updated != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{Resolved: true}); total != 1 {
		t.Fatal("history must stay")
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{}); total != 2 {
		t.Fatal("reopened b must be open as a new row")
	}
}

// A pass only resolves the kinds it owns.
func TestSyncOnlyOwnedKinds(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	other := fnd(insights.KindPoolSize, "x", 5)
	other.Scope, other.RepositoryID, other.Repository = insights.ScopePool, 0, ""
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindPoolSize}, []insights.Finding{other}, tBase); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, nil, tBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{}); total != 1 {
		t.Fatal("pool.size must stay open")
	}
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, []insights.Finding{other}, tBase); err == nil {
		t.Fatal("finding of a foreign kind must be refused")
	}
}

// R5.11: ranked by minutes, then first seen, then id; filters; paging by cursor.
func TestListRankFilterPage(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	kinds := []insights.Kind{insights.KindWaitHold, insights.KindGuardrailG4}
	var all []insights.Finding
	for i := 0; i < 5; i++ {
		all = append(all, fnd(insights.KindWaitHold, fmt.Sprintf("s%d", i), []int{5, 30, 5, 30, 1}[i]))
	}
	z := fnd(insights.KindGuardrailG4, "g4", 0)
	z.Guardrail, z.Area, z.RepositoryID = "G4", insights.AreaGuardrails, 11
	all = append(all, z)
	if _, err := s.InsightFindings().Sync(ctx, kinds, all[:2], tBase); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Sync(ctx, kinds, all, tBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, total, next, err := fs.List(ctx, insights.Filter{Limit: 4})
	if err != nil || total != 6 || next != "4" {
		t.Fatalf("%d %q %v", total, next, err)
	}
	var subs []string
	for _, it := range items {
		subs = append(subs, it.Subject)
	}
	// 30 min: s1 (older), s3; then 5 min: s0, s2.
	if fmt.Sprint(subs) != "[s1 s3 s0 s2]" {
		t.Fatalf("order %v", subs)
	}
	rest, _, next, _ := fs.List(ctx, insights.Filter{Limit: 4, Cursor: "4"})
	if len(rest) != 2 || next != "" || rest[0].Subject != "s4" || rest[1].Subject != "g4" {
		t.Fatalf("%+v next=%q", rest, next)
	}
	if g, total, _, _ := fs.List(ctx, insights.Filter{Guardrail: "G4"}); total != 1 || g[0].Subject != "g4" {
		t.Fatalf("guardrail filter %+v", g)
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{RepositoryID: 11}); total != 1 {
		t.Fatal("repository filter")
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{Area: insights.AreaGuardrails}); total != 1 {
		t.Fatal("area filter")
	}
	if _, _, _, err := fs.List(ctx, insights.Filter{Cursor: "x"}); err == nil {
		t.Fatal("bad cursor")
	}
	sum, err := fs.Summary(ctx)
	if err != nil || sum.Open != 6 || sum.MinutesSavedWeek != 71 || sum.EnoughData {
		t.Fatalf("%+v %v", sum, err)
	}
}

// R6.3, R6.8: the run log keeps start, end, ok and error.
func TestRunLogRoundTrip(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	id, err := fs.Begin(ctx, "waits", insights.Window{From: tBase.Add(-7 * 24 * time.Hour), To: tBase}, tBase)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.End(ctx, id, tBase.Add(time.Second), false, 2, 1, "read failed"); err != nil {
		t.Fatal(err)
	}
	var ok, found, resolved int
	var errText, fin string
	if err := s.db.QueryRowContext(context.Background(), `SELECT ok, found, resolved, error, finished_at FROM insight_analyser_runs WHERE id = ?`, id).Scan(&ok, &found, &resolved, &errText, &fin); err != nil {
		t.Fatal(err)
	}
	if ok != 0 || found != 2 || resolved != 1 || errText != "read failed" || fin == "" {
		t.Fatalf("%d %d %d %q %q", ok, found, resolved, errText, fin)
	}
}

// R6.9: times are stored in UTC whatever zone the caller used.
func TestStoredUTC(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	local := tBase.In(time.FixedZone("x", 5*3600))
	if _, err := s.InsightFindings().Sync(ctx, []insights.Kind{insights.KindWaitHold}, []insights.Finding{fnd(insights.KindWaitHold, "a", 1)}, local); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := s.db.QueryRowContext(context.Background(), `SELECT first_seen FROM insight_findings`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first != "2026-11-20T12:00:00.000Z" {
		t.Fatalf("stored %q", first)
	}
}

func seedJobs(t *testing.T, s *Store, n int) {
	t.Helper()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(context.Background(), q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO installations (id, account_login, account_type) VALUES (1,'acme','Organization')`)
	exec(`INSERT INTO repositories (id, installation_id, full_name, owner, name, default_branch) VALUES (10,1,'acme/api','acme','api','main')`)
	exec(`INSERT INTO runs (repository_id, run_id, run_attempt, repository, pull_request, created_at) VALUES (10, 1, 1, 'acme/api', 7, '2026-11-19T10:00:00.000Z')`)
	for i := 0; i < n; i++ {
		origin := []string{"crsp", "other", "hosted", ""}[i%4]
		c := tBase.Add(-time.Duration(i+1) * time.Minute)
		exec(`INSERT INTO jobs (id, repository_id, repository, run_id, run_attempt, workflow, name, job_ident, branch, event, labels, state, result,
			created_at, slot_at, started_at, completed_at, runner_origin, runner_labels)
			VALUES (?,10,'acme/api',1,1,'ci.yml','build','acme/api ci.yml build','main','push','["self-hosted","linux"]','done','success',?,?,?,?,?,'["x"]')`,
			i+1, fmtTime(c), fmtTime(c.Add(time.Minute)), fmtTime(c.Add(2*time.Minute)), fmtTime(c.Add(5*time.Minute)), origin)
	}
}

// R5.1: the job source returns jobs of every origin inside the window, with their facts.
func TestJobSourceAllOrigins(t *testing.T) {
	s := openTemp(t)
	seedJobs(t, s, 8)
	src := s.InsightJobSource()
	ctx := context.Background()
	facts, err := src.JobsBetween(ctx, insights.Window{From: tBase.Add(-7 * 24 * time.Hour), To: tBase}, 0)
	if err != nil || len(facts) != 8 {
		t.Fatalf("%d %v", len(facts), err)
	}
	seen := map[insights.Origin]int{}
	for _, f := range facts {
		seen[f.Origin]++
	}
	if len(seen) != 4 || seen[insights.OriginOther] != 2 || seen[insights.OriginHosted] != 2 {
		t.Fatalf("origins %v", seen)
	}
	f := facts[0] // oldest first
	if f.ID != 8 || f.PullRequest != 7 || !f.DefaultBranch || len(f.Labels) != 2 || f.CompletedAt.IsZero() || f.QueuedAt != (time.Time{}) {
		t.Fatalf("%+v", f)
	}
	if got, _ := src.JobsBetween(ctx, insights.Window{From: tBase.Add(-3 * time.Minute), To: tBase}, 0); len(got) != 3 {
		t.Fatalf("window: %d", len(got))
	}
	if got, _ := src.JobsBetween(ctx, insights.Window{From: tBase.Add(-time.Hour), To: tBase}, 99); len(got) != 0 {
		t.Fatalf("repository filter: %d", len(got))
	}
}

// R5.38: the digest store feeds the writer and keeps the index rows.
func TestDigestStoreRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	fs := s.InsightFindings()
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, []insights.Finding{fnd(insights.KindWaitHold, "a", 3), fnd(insights.KindWaitHold, "b", 4)}, tBase); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, []insights.Finding{fnd(insights.KindWaitHold, "a", 3)}, tBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	d := s.InsightDigest()
	op, err := d.OpenedBetween(ctx, tBase.Add(-time.Minute), tBase.Add(time.Minute))
	if err != nil || len(op) != 2 || op[0].Subject != "b" {
		t.Fatalf("%v %v", op, err)
	}
	rs, err := d.ResolvedBetween(ctx, tBase, tBase.Add(2*time.Hour))
	if err != nil || len(rs) != 1 || rs[0].Subject != "b" {
		t.Fatalf("%v %v", rs, err)
	}
	if all, err := d.Open(ctx); err != nil || len(all) != 1 {
		t.Fatalf("%v %v", all, err)
	}
	for _, w := range []string{"2026-W46", "2026-W47"} {
		if err := d.Put(ctx, digestRow(w)); err != nil {
			t.Fatal(err)
		}
	}
	last, err := d.Last(ctx, 12)
	if err != nil || len(last) != 2 || last[0].Week != "2026-W47" || len(last[0].TopIDs) != 1 {
		t.Fatalf("%+v %v", last, err)
	}
	if err := d.Delete(ctx, "2026-W46"); err != nil {
		t.Fatal(err)
	}
	if last, _ := d.Last(ctx, 12); len(last) != 1 {
		t.Fatal("delete")
	}
}

// D17: a dismissed finding leaves the list and the count for 7 days and is back after that; the engine keeps
// refreshing it meanwhile; a finding that is no longer true is resolved as usual while hidden.
func TestDismissHidesForSevenDays(t *testing.T) {
	s := openTemp(t)
	fs := s.InsightFindings()
	ctx := context.Background()
	kinds := []insights.Kind{insights.KindWaitHold}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := fs.Sync(ctx, kinds, []insights.Finding{fnd(insights.KindWaitHold, "hidden", 30), fnd(insights.KindWaitHold, "back", 20),
		fnd(insights.KindWaitHold, "visible", 10)}, now); err != nil {
		t.Fatal(err)
	}
	items, _, _, _ := fs.List(ctx, insights.Filter{})
	id := map[string]int64{}
	for _, it := range items {
		id[it.Subject] = it.ID
	}
	until, err := fs.Dismiss(ctx, id["hidden"], now)
	if err != nil || !until.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("until = %v, %v", until, err)
	}
	// "back" was dismissed 8 days ago: its 7 days are over.
	if _, err := fs.Dismiss(ctx, id["back"], now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, total, _, _ := fs.List(ctx, insights.Filter{})
	if total != 2 || len(items) != 2 || items[0].Subject != "back" || items[1].Subject != "visible" {
		t.Fatalf("list = %d %+v", total, items)
	}
	if sum, _ := fs.Summary(ctx); sum.Open != 2 || sum.MinutesSavedWeek != 30 {
		t.Fatalf("summary = %+v", sum)
	}
	got, _ := fs.Get(ctx, id["hidden"])
	if !got.DismissedUntil.Equal(until) {
		t.Errorf("dismissed until = %v", got.DismissedUntil)
	}
	// Seen again: still hidden, still one row, refreshed.
	if _, err := fs.Sync(ctx, kinds, []insights.Finding{fnd(insights.KindWaitHold, "hidden", 31), fnd(insights.KindWaitHold, "back", 20),
		fnd(insights.KindWaitHold, "visible", 10)}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, total, _, _ := fs.List(ctx, insights.Filter{}); total != 2 {
		t.Fatalf("a hidden finding came back before its time: %d", total)
	}
	// No longer true: resolved while hidden.
	if _, err := fs.Sync(ctx, kinds, []insights.Finding{fnd(insights.KindWaitHold, "back", 20), fnd(insights.KindWaitHold, "visible", 10)}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Dismiss(ctx, id["hidden"], now); err != insights.ErrResolved {
		t.Errorf("dismiss a resolved finding = %v", err)
	}
	if _, err := fs.Dismiss(ctx, 9999, now); err != insights.ErrNotFound {
		t.Errorf("dismiss an unknown finding = %v", err)
	}
}
