package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/guardrails"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// WorkflowChecks is the workflow_checks store (R5.29) over queries/insights_checks.sql.
func (s *Store) WorkflowChecks() workflows.Store { return workflowChecks{s: s} }

// GuardrailReports is the guardrail_reports store (R5.31, R5.32) over queries/insights_checks.sql.
func (s *Store) GuardrailReports() guardrails.Store { return guardrailStore{s: s} }

const checksTimeLayout = "2006-01-02T15:04:05.000Z"

type workflowChecks struct{ s *Store }

var _ workflows.Store = workflowChecks{}

// Save stores the results of one file at one commit. Results of one commit and file are replaced by the new
// ones that share their key (check, job).
func (w workflowChecks) Save(ctx context.Context, f workflows.File, results []workflows.Result) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	at := time.Now().UTC().Format(checksTimeLayout)
	def := int64(0)
	if f.DefaultBranch {
		def = 1
	}
	for _, r := range results {
		err := w.s.q.UpsertWorkflowCheck(ctx, sqlcgen.UpsertWorkflowCheckParams{
			RepositoryID: f.RepositoryID, HeadSha: f.HeadSHA, Workflow: f.Path, CheckID: string(r.Check), Job: r.Job,
			Status: string(r.Status), Line: int64(r.Line), Detail: r.Detail, Guardrail: string(r.Guardrail),
			DefaultBranch: def, CheckedAt: at,
		})
		if err != nil {
			return fmt.Errorf("save workflow check %s of %s: %w", r.Check, f.Path, err)
		}
	}
	return nil
}

// Latest returns the results of the latest default-branch commit checked; headSHA is empty when none.
func (w workflowChecks) Latest(ctx context.Context, repositoryID int64) (string, []workflows.Result, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	sha, err := w.s.q.LatestDefaultBranchCheckedCommit(ctx, repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read the latest checked commit of repository %d: %w", repositoryID, err)
	}
	res, err := w.ForCommit(ctx, repositoryID, sha)
	return sha, res, err
}

// ForCommit returns the results of one commit. Result.Job is "<workflow path>#<job>" (see workflows.SplitJob).
func (w workflowChecks) ForCommit(ctx context.Context, repositoryID int64, headSHA string) ([]workflows.Result, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := w.s.q.ListWorkflowChecksForCommit(ctx, sqlcgen.ListWorkflowChecksForCommitParams{RepositoryID: repositoryID, HeadSha: headSHA})
	if err != nil {
		return nil, fmt.Errorf("read workflow checks of %s: %w", headSHA, err)
	}
	out := make([]workflows.Result, 0, len(rows))
	for _, r := range rows {
		out = append(out, workflows.Result{
			Check: workflows.CheckID(r.CheckID), Job: workflows.JoinJob(r.Workflow, r.Job), Status: workflows.Status(r.Status),
			Line: int(r.Line), Detail: r.Detail, Guardrail: insights.Guardrail(r.Guardrail),
		})
	}
	return out, nil
}

type guardrailStore struct{ s *Store }

var _ guardrails.Store = guardrailStore{}

type breakJSON struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func (g guardrailStore) save(ctx context.Context, repo, run int64, sha string, source guardrails.Source, e guardrails.Entry, known int, at time.Time) error {
	bs := make([]breakJSON, 0, len(e.Breaks))
	for _, b := range e.Breaks {
		bs = append(bs, breakJSON{File: b.File, Line: b.Line, Message: b.Message})
	}
	raw, err := json.Marshal(bs)
	if err != nil {
		return fmt.Errorf("encode breaks of %s: %w", e.Guardrail, err)
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err = g.s.q.UpsertGuardrailReport(ctx, sqlcgen.UpsertGuardrailReportParams{
		RepositoryID: repo, RunID: run, HeadSha: sha, Guardrail: string(e.Guardrail), Source: string(source),
		State: string(e.State), Count: int64(e.Count), KnownCount: int64(known), Reason: e.Reason, Breaks: string(raw),
		ReportedAt: at.UTC().Format(checksTimeLayout),
	})
	if err != nil {
		return fmt.Errorf("save guardrail %s of repository %d: %w", e.Guardrail, repo, err)
	}
	return nil
}

// SaveReport stores one guard report of a run, one row per guardrail.
func (g guardrailStore) SaveReport(ctx context.Context, repositoryID, runID int64, headSHA string, r guardrails.Report, at time.Time) error {
	for _, e := range r.Entries {
		if err := g.save(ctx, repositoryID, runID, headSHA, guardrails.FromGuardReport, e, r.KnownCount, at); err != nil {
			return err
		}
	}
	return nil
}

// SaveOwn stores crsp's own state of one guardrail (run id 0).
func (g guardrailStore) SaveOwn(ctx context.Context, repositoryID int64, e guardrails.Entry, at time.Time) error {
	return g.save(ctx, repositoryID, 0, "", guardrails.FromCrsp, e, -1, at)
}

// Status returns the latest state of each guardrail per repository, with the known-list count per day of the
// last 30 days. On a tie in time the guard report wins over crsp's own check.
func (g guardrailStore) Status(ctx context.Context, repositoryID int64) ([]guardrails.Status, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	since := time.Now().UTC().AddDate(0, 0, -30).Truncate(24 * time.Hour).Format(checksTimeLayout)
	rows, err := g.s.q.ListGuardrailStatusRows(ctx, sqlcgen.ListGuardrailStatusRowsParams{RepositoryID: repositoryID, Since: since})
	if err != nil {
		return nil, fmt.Errorf("read guardrail status: %w", err)
	}
	type key struct {
		repo int64
		g    string
	}
	latest := map[key]guardrails.StatusEntry{}
	names := map[int64]string{}
	days := map[int64]map[string]int{}
	for _, r := range rows {
		at, err := time.Parse(time.RFC3339Nano, r.ReportedAt)
		if err != nil {
			return nil, fmt.Errorf("read guardrail status: bad time %q: %w", r.ReportedAt, err)
		}
		names[r.RepositoryID] = r.FullName
		var bs []breakJSON
		_ = json.Unmarshal([]byte(r.Breaks), &bs)
		e := guardrails.Entry{Guardrail: insights.Guardrail(r.Guardrail), State: guardrails.State(r.State), Count: int(r.Count), Reason: r.Reason}
		for _, b := range bs {
			e.Breaks = append(e.Breaks, guardrails.Break{File: b.File, Line: b.Line, Message: b.Message})
		}
		cur := guardrails.StatusEntry{Entry: e, Source: guardrails.Source(r.Source), ReportedAt: at}
		k := key{r.RepositoryID, r.Guardrail}
		if old, ok := latest[k]; !ok || at.After(old.ReportedAt) || (at.Equal(old.ReportedAt) && cur.Source == guardrails.FromGuardReport) {
			latest[k] = cur
		}
		if r.KnownCount >= 0 {
			if days[r.RepositoryID] == nil {
				days[r.RepositoryID] = map[string]int{}
			}
			days[r.RepositoryID][at.Format("2006-01-02")] = int(r.KnownCount) // rows come oldest first: the last of a day wins
		}
	}
	byRepo := map[int64]*guardrails.Status{}
	for k, e := range latest {
		st := byRepo[k.repo]
		if st == nil {
			st = &guardrails.Status{RepositoryID: k.repo, Repository: names[k.repo]}
			byRepo[k.repo] = st
		}
		st.Guardrails = append(st.Guardrails, e)
	}
	out := make([]guardrails.Status, 0, len(byRepo))
	for id, st := range byRepo {
		sort.Slice(st.Guardrails, func(i, j int) bool { return st.Guardrails[i].Guardrail < st.Guardrails[j].Guardrail })
		for d, n := range days[id] {
			st.KnownCountByDay = append(st.KnownCountByDay, guardrails.DayCount{Day: d, Count: n})
		}
		sort.Slice(st.KnownCountByDay, func(i, j int) bool { return st.KnownCountByDay[i].Day < st.KnownCountByDay[j].Day })
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RepositoryID < out[j].RepositoryID })
	return out, nil
}
