package guardrails

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/tests"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

const (
	pageSize  = 2000
	maxRows   = 500000
	maxBreaks = 10
	// reportFresh is how long a guard report covers a guardrail before crsp's own check takes over again.
	reportFresh = 7 * 24 * time.Hour
)

// Service is the guardrails analyser (insights.Analyser): it fills crsp's own state of the guardrails a guard
// report does not cover (R5.32) and gives one finding per repository and breaking guardrail.
type Service struct {
	Store     Store
	Tests     tests.Store
	Workflows workflows.Store
	Jobs      insights.JobSource
	Now       func() time.Time // the clock; time.Now when nil
	Red       *redact.Redactor // plain one when nil
}

var _ insights.Analyser = (*Service)(nil)

// Name is the analyser's name in logs and insight_analyser_runs.
func (s *Service) Name() string { return "guardrails" }

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

// Analyse writes crsp's own states and returns the findings of the breaking guardrails.
func (s *Service) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	jobs, err := s.Jobs.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	repos := map[int64]string{}
	for _, j := range jobs {
		repos[j.RepositoryID] = j.Repository
	}
	ids := make([]int64, 0, len(repos))
	for id := range repos {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := s.now()
	var out []insights.Finding
	for _, id := range ids {
		entries, err := s.Evaluate(ctx, id, repos[id], w, now)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.State == Broken {
				out = append(out, s.finding(id, repos[id], e))
			}
		}
	}
	return out, nil
}

// StateOf is one guardrail's final state with its source.
type StateOf struct {
	Entry
	Source Source
}

// Evaluate returns the state of G1 to G7 of one repository: from a fresh guard report when it covers the
// guardrail, else from crsp's own check, which it also saves (R5.32).
func (s *Service) Evaluate(ctx context.Context, repoID int64, repo string, w insights.Window, now time.Time) ([]StateOf, error) {
	covered := map[insights.Guardrail]StateOf{}
	status, err := s.Store.Status(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("read guardrail status: %w", err)
	}
	for _, st := range status {
		if st.RepositoryID != repoID {
			continue
		}
		for _, e := range st.Guardrails {
			if e.Source == FromGuardReport && now.Sub(e.ReportedAt) <= reportFresh {
				covered[e.Guardrail] = StateOf{Entry: e.Entry, Source: FromGuardReport}
			}
		}
	}
	rows, err := s.testRows(ctx, repoID, w)
	if err != nil {
		return nil, err
	}
	_, checks, err := s.Workflows.Latest(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("read workflow checks: %w", err)
	}
	var out []StateOf
	for _, g := range All {
		if c, ok := covered[g]; ok {
			out = append(out, c)
			continue
		}
		e := OwnCheck(g, rows, checks)
		for i := range e.Breaks {
			e.Breaks[i].Message = s.redact(e.Breaks[i].Message)
		}
		if err := s.Store.SaveOwn(ctx, repoID, e, now); err != nil {
			return nil, fmt.Errorf("save %s of %s: %w", g, repo, err)
		}
		out = append(out, StateOf{Entry: e, Source: FromCrsp})
	}
	return out, nil
}

func (s *Service) testRows(ctx context.Context, repoID int64, w insights.Window) ([]tests.Row, error) {
	var rows []tests.Row
	for off := 0; off < maxRows; off += pageSize {
		page, err := s.Tests.Between(ctx, repoID, w, pageSize, off)
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

// OwnCheck is crsp's own state of one guardrail from the test rows of the window and the latest workflow
// checks (R5.32): G1 from test times over the limit, G2 and G3 and G6 from workflow checks, G3 also from test
// reports that show retries, G7 from whether any test report was found. G4 and G5 are Unknown: they need the
// guard report.
func OwnCheck(g insights.Guardrail, rows []tests.Row, checks []workflows.Result) Entry {
	e := Entry{Guardrail: g, State: Unknown}
	switch g {
	case "G1":
		if len(rows) == 0 {
			return e
		}
		e.State = Pass
		for _, in := range tests.Times(rows, "") {
			if in.OverLimit > 0 {
				e.Breaks = append(e.Breaks, Break{File: in.File, Message: fmt.Sprintf("test %q: p95 %s is over the %s limit of a %s test",
					in.Name, in.P95.Round(time.Millisecond), in.OverLimit, in.Kind)})
			}
		}
	case "G2", "G6":
		e = fromChecks(e, checks)
	case "G3":
		e = fromChecks(e, checks)
		seen := map[string]bool{}
		for _, r := range rows {
			if r.Retries > 0 && r.Result == tests.Passed && !seen[r.TestID] {
				seen[r.TestID] = true
				if e.State == Unknown {
					e.State = Pass
				}
				e.Breaks = append(e.Breaks, Break{File: r.File, Message: fmt.Sprintf("test %q passed after %d retries", r.Name, r.Retries)})
			}
		}
		if len(rows) > 0 && e.State == Unknown {
			e.State = Pass
		}
	case "G7":
		e.State = Pass
		if len(rows) == 0 {
			e.State = Broken
			e.Breaks = []Break{{Message: "no test report found in the last 7 days"}}
		}
	}
	if len(e.Breaks) > 0 {
		e.State = Broken
	}
	e.Count = len(e.Breaks)
	if len(e.Breaks) > maxBreaks {
		e.Breaks = e.Breaks[:maxBreaks]
	}
	return e
}

// fromChecks reads the workflow checks of the entry's guardrail: any check that ran gives Pass or breaks; no
// check that ran leaves Unknown.
func fromChecks(e Entry, checks []workflows.Result) Entry {
	for _, c := range checks {
		if c.Guardrail != e.Guardrail || c.Status == workflows.Skip {
			continue
		}
		if e.State == Unknown {
			e.State = Pass
		}
		if c.Status == workflows.Fail {
			e.Breaks = append(e.Breaks, Break{File: c.Job, Line: c.Line, Message: c.Detail})
		}
	}
	return e
}

var titles = map[insights.Guardrail]string{
	"G1": "per-test timeout", "G2": "job timeout-minutes", "G3": "no retries in CI", "G4": "no sleeps in tests",
	"G5": "at most 20 cases per test", "G6": "no mutation or many-run property tests on pull requests", "G7": "test times reported",
}

var fixes = map[insights.Guardrail]string{
	"G1": "Make the slow tests faster than their limit (unit 10 s, integration 60 s, end-to-end 2 min), or split them.",
	"G2": "Give every job a timeout-minutes of 30 or less.",
	"G3": "Remove the retries from the test commands and fix the flaky tests instead.",
	"G4": "Replace the sleeps in tests with fakes, fake clocks or waiting on an event.",
	"G5": "Split tables of more than 20 cases into separate tests.",
	"G6": "Run mutation and many-run property tests on a schedule, not on pull requests.",
	"G7": "Upload a test report (JUnit, JSON or go test -json) from CI so test times can be read.",
}

func (s *Service) finding(repoID int64, repo string, e StateOf) insights.Finding {
	type brk struct {
		File    string `json:"file,omitempty"`
		Line    int    `json:"line,omitempty"`
		Message string `json:"message"`
	}
	var shown []brk
	for i, b := range e.Breaks {
		if i == maxBreaks {
			break
		}
		shown = append(shown, brk{File: b.File, Line: b.Line, Message: s.redact(b.Message)})
	}
	ev, _ := json.Marshal(map[string]any{"guardrail": e.Guardrail, "state": e.State, "source": e.Source, "count": e.Count, "breaks": shown})
	kind := insights.Kind("guardrail." + lower(string(e.Guardrail)))
	return insights.Finding{Kind: kind, Scope: insights.ScopeRepo, RepositoryID: repoID, Repository: repo, Subject: repo,
		Area: insights.AreaGuardrails, Title: fmt.Sprintf("%s breaks %s (%s): %d place%s", repo, e.Guardrail, titles[e.Guardrail], e.Count, plural(e.Count)),
		Fix: fixes[e.Guardrail], Evidence: ev, Guardrail: e.Guardrail}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
