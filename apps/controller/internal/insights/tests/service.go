package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

const (
	pageSize     = 2000
	maxRows      = 500000
	slowPerRepo  = 5
	defaultLimit = 50
	// FlakyWindow is how far back flaky tests are looked for: 14 days, wider than the 7 day window of the other
	// kinds, because the same commit seldom gives two results within a week (owner decision D21, R5.35).
	FlakyWindow = 14 * 24 * time.Hour
)

// Service is the tests analyser (insights.Analyser) and the reader of the Tests segment (Reader).
type Service struct {
	Store Store
	Jobs  insights.JobSource // names the repositories that ran jobs in a window
	Now   func() time.Time   // the clock; time.Now when nil
	Red   *redact.Redactor   // findings pass it (R5.4); a plain one when nil
}

var (
	_ insights.Analyser = (*Service)(nil)
	_ Reader            = (*Service)(nil)
)

// Name is the analyser's name in logs and insight_analyser_runs.
func (s *Service) Name() string { return "tests" }

// Kinds are the finding kinds the analyser owns.
func (s *Service) Kinds() []insights.Kind { return Kinds }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) redact(v string) string {
	if s.Red == nil {
		s.Red = redact.New()
	}
	return s.Red.Redact(v)
}

// repositories maps the ids of the repositories with jobs in w to their names.
func (s *Service) repositories(ctx context.Context, w insights.Window, only int64) (map[int64]string, error) {
	jobs, err := s.Jobs.JobsBetween(ctx, w, only)
	if err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	out := map[int64]string{}
	for _, j := range jobs {
		out[j.RepositoryID] = j.Repository
	}
	return out, nil
}

func (s *Service) load(ctx context.Context, repo int64, w insights.Window) ([]Row, error) {
	var rows []Row
	for off := 0; off < maxRows; off += pageSize {
		page, err := s.Store.Between(ctx, repo, w, pageSize, off)
		if err != nil {
			return nil, fmt.Errorf("read test results: %w", err)
		}
		rows = append(rows, page...)
		if len(page) < pageSize {
			break
		}
	}
	return rows, nil
}

func sortedIDs(m map[int64]string) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func minutes(d time.Duration) int { return int(math.Round(d.Minutes())) }

// Analyse returns the slow, flaky, duplicated and assertion-free findings of every repository in w.
func (s *Service) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	repos, err := s.repositories(ctx, w, 0)
	if err != nil {
		return nil, err
	}
	var out []insights.Finding
	for _, id := range sortedIDs(repos) {
		wide := insights.Window{From: w.To.Add(-FlakyWindow), To: w.To}
		if w.From.Before(wide.From) {
			wide.From = w.From
		}
		all, err := s.load(ctx, id, wide)
		if err != nil {
			return nil, err
		}
		out = append(out, s.findings(id, repos[id], inWindow(all, w), all)...)
	}
	return out, nil
}

// inWindow keeps the rows finished in w; a row with no finish time is kept (unknown is never "old").
func inWindow(rows []Row, w insights.Window) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if r.FinishedAt.IsZero() || !r.FinishedAt.Before(w.From) && r.FinishedAt.Before(w.To) {
			out = append(out, r)
		}
	}
	return out
}

// findings builds the findings of one repository: rows is the 7 day window, flakyRows the 14 day one (D21).
func (s *Service) findings(repoID int64, repo string, rows, flakyRows []Row) []insights.Finding {
	base := func(k insights.Kind, subject, title, fix string, mins int, ev any) insights.Finding {
		b, _ := json.Marshal(ev)
		return insights.Finding{Kind: k, Scope: insights.ScopeRepo, RepositoryID: repoID, Repository: repo,
			Subject: subject, Area: insights.AreaTests, Title: s.redact(title), Fix: fix, Evidence: b, MinutesSavedWeek: mins}
	}
	var out []insights.Finding
	for _, t := range SlowTests(rows, slowPerRepo) {
		fix := "Make this test faster: split it, or stub the slow part."
		if t.Excess > 0 {
			fix = fmt.Sprintf("Make this test faster than its %s limit: split it, or stub the slow part.", Limit(t.Kind))
		}
		out = append(out, base(insights.KindTestSlow, t.TestID,
			fmt.Sprintf("Test %q takes %s a week", t.Name, t.WeeklyTotal.Round(time.Second)), fix, minutes(t.Excess),
			map[string]any{"testId": t.TestID, "runs": t.Runs, "weeklySeconds": int(t.WeeklyTotal.Seconds()),
				"slowestSeconds": t.Slowest.Seconds(), "kind": t.Kind, "file": t.File}))
	}
	for _, f := range FlakyTests(flakyRows) {
		out = append(out, base(insights.KindTestFlaky, f.TestID,
			fmt.Sprintf("Test %q is flaky: same commit, different result", f.Name),
			"Fix the test so it gives one result for one commit (shared state, time, order, network). Do not add retries.",
			minutes(f.Rerun), map[string]any{"testId": f.TestID, "groups": f.Groups, "jobs": f.JobIdents, "runs": f.Runs}))
	}
	for _, d := range DuplicateTests(rows) {
		out = append(out, base(insights.KindTestDuplicate, d.TestID,
			fmt.Sprintf("Test %q runs in %d jobs of the same run", d.Name, len(d.JobIdents)),
			"Run this test in one job only: narrow the test selection of the others.",
			minutes(d.Extra), map[string]any{"testId": d.TestID, "jobs": d.JobIdents, "runs": d.Runs, "extraSeconds": int(d.Extra.Seconds())}))
	}
	for _, n := range FindNoAssertions(rows) {
		out = append(out, base(insights.KindTestNoAssertions, n.TestID,
			fmt.Sprintf("Test %q passes without checking anything", n.Name),
			"Add an assertion to this test, or delete it.", 0,
			map[string]any{"testId": n.TestID, "runs": n.Runs, "jobs": n.JobIdents}))
	}
	return out
}

// Tests answers listTestInsights: one view, one repository (0 for all), the last days days, one page. The
// cursor is the offset of the page as text.
func (s *Service) Tests(ctx context.Context, v View, repositoryID int64, days, limit int, cursor string) ([]Insight, int, string, bool, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	off := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return nil, 0, "", false, fmt.Errorf("bad cursor")
		}
		off = n
	}
	if min := int(FlakyWindow / (24 * time.Hour)); v == Flaky && days < min {
		days = min // the flaky window is 14 days whatever range the page asked for (D21)
	}
	now := s.now()
	w := insights.Window{From: now.AddDate(0, 0, -days), To: now}
	repos, err := s.repositories(ctx, w, repositoryID)
	if err != nil {
		return nil, 0, "", false, err
	}
	var all []Insight
	found := false
	for _, id := range sortedIDs(repos) {
		rows, err := s.load(ctx, id, w)
		if err != nil {
			return nil, 0, "", false, err
		}
		if len(rows) > 0 {
			found = true
		}
		all = append(all, view(v, repos[id], rows)...)
	}
	total := len(all)
	if off > total {
		off = total
	}
	end := off + limit
	next := ""
	if end < total {
		next = strconv.Itoa(end)
	} else {
		end = total
	}
	return all[off:end], total, next, found, nil
}

func view(v View, repo string, rows []Row) []Insight {
	switch v {
	case Slowest:
		return Times(rows, repo)
	case Flaky:
		var out []Insight
		for _, f := range FlakyTests(rows) {
			out = append(out, Insight{TestID: f.TestID, Name: f.Name, Suite: f.Suite, File: f.File, Repository: repo,
				JobIdents: f.JobIdents, Kind: f.Kind, Runs: len(f.Runs), FlakyRuns: f.Groups, Assertions: -1, LastSeen: f.LastSeen})
		}
		return out
	case Duplicated:
		var out []Insight
		for _, d := range DuplicateTests(rows) {
			out = append(out, Insight{TestID: d.TestID, Name: d.Name, Suite: d.Suite, File: d.File, Repository: repo,
				JobIdents: d.JobIdents, Kind: d.Kind, Runs: d.Runs, Assertions: -1, LastSeen: d.LastSeen})
		}
		return out
	case NoAssertions:
		var out []Insight
		for _, n := range FindNoAssertions(rows) {
			out = append(out, Insight{TestID: n.TestID, Name: n.Name, Suite: n.Suite, File: n.File, Repository: repo,
				JobIdents: n.JobIdents, Kind: n.Kind, Runs: n.Runs, Assertions: 0, LastSeen: n.LastSeen})
		}
		return out
	}
	return nil
}
