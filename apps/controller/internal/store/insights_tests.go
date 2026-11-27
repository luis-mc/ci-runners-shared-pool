package store

import (
	"context"
	"fmt"
	"time"

	instests "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/tests"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// InsightTests is the store of parsed test results (tests.Store, test_results; R5.33 to R5.37).
type InsightTests struct{ s *Store }

var _ instests.Store = (*InsightTests)(nil)

// InsightTests returns the keeper of the parsed test results.
func (s *Store) InsightTests() *InsightTests { return &InsightTests{s: s} }

// ReplaceReport stores the rows of one report file in one transaction, replacing the earlier rows of the same
// report. A report id 0 is a job log: its rows are replaced per repository, run attempt, job and format.
func (t *InsightTests) ReplaceReport(ctx context.Context, reportID int64, rows []instests.Row) error {
	return t.s.WithTx(ctx, func(tx *Tx) error {
		if reportID != 0 {
			if err := tx.q.DeleteTestResultsForReport(tx.ctx, reportID); err != nil {
				return fmt.Errorf("delete the report's test results: %w", err)
			}
		} else {
			type key struct {
				repo, run, job int64
				attempt        int
				format         instests.Format
			}
			done := map[key]bool{}
			for _, r := range rows {
				k := key{r.RepositoryID, r.RunID, r.JobID, r.RunAttempt, r.Format}
				if done[k] {
					continue
				}
				done[k] = true
				if err := tx.q.DeleteTestResultsForJobLog(tx.ctx, sqlcgen.DeleteTestResultsForJobLogParams{
					RepositoryID: r.RepositoryID, RunID: r.RunID, RunAttempt: int64(r.RunAttempt), JobID: r.JobID, Format: string(r.Format)}); err != nil {
					return fmt.Errorf("delete the job log's test results: %w", err)
				}
			}
		}
		for _, r := range rows {
			kind := string(r.Kind)
			if kind == "" {
				kind = string(instests.Unit)
			}
			if err := tx.q.InsertTestResult(tx.ctx, sqlcgen.InsertTestResultParams{
				RepositoryID: r.RepositoryID, RunID: r.RunID, RunAttempt: int64(r.RunAttempt), JobID: r.JobID, JobIdent: r.JobIdent,
				HeadSha: r.HeadSHA, ReportID: reportID, Format: string(r.Format), TestID: r.TestID, Suite: r.Suite, Name: r.Name,
				File: r.File, Kind: kind, Result: string(r.Result), DurationMs: r.Duration.Milliseconds(), Retries: int64(r.Retries),
				Assertions: int64(r.Assertions), FinishedAt: fmtTimeOpt(r.FinishedAt),
			}); err != nil {
				return fmt.Errorf("insert a test result: %w", err)
			}
		}
		return nil
	})
}

// Between returns the rows of one repository finished in w, in id order, one page.
func (t *InsightTests) Between(ctx context.Context, repositoryID int64, w insights.Window, limit, offset int) ([]instests.Row, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	// Raw SQL: sqlc numbers the parameters of the generated ListTestResultsBetween so that LIMIT reads the
	// offset (LIMIT ?5 OFFSET ?4 with the arguments in the other order). Reported to the orchestrator.
	rs, err := t.s.db.QueryContext(ctx, `SELECT id, repository_id, run_id, run_attempt, job_id, job_ident, head_sha, report_id, format,
test_id, suite, name, file, kind, result, duration_ms, retries, assertions, finished_at FROM test_results
WHERE repository_id = ? AND finished_at >= ? AND finished_at < ? ORDER BY id LIMIT ? OFFSET ?`,
		repositoryID, fmtTime(w.From), fmtTime(w.To), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("read test results: %w", err)
	}
	defer func() { _ = rs.Close() }()
	var list []sqlcgen.TestResult
	for rs.Next() {
		var r sqlcgen.TestResult
		if err := rs.Scan(&r.ID, &r.RepositoryID, &r.RunID, &r.RunAttempt, &r.JobID, &r.JobIdent, &r.HeadSha, &r.ReportID, &r.Format,
			&r.TestID, &r.Suite, &r.Name, &r.File, &r.Kind, &r.Result, &r.DurationMs, &r.Retries, &r.Assertions, &r.FinishedAt); err != nil {
			return nil, fmt.Errorf("read a test result: %w", err)
		}
		list = append(list, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("read test results: %w", err)
	}
	out := make([]instests.Row, 0, len(list))
	for _, r := range list {
		out = append(out, instests.Row{
			RepositoryID: r.RepositoryID, RunID: r.RunID, RunAttempt: int(r.RunAttempt), JobID: r.JobID, JobIdent: r.JobIdent,
			HeadSHA: r.HeadSha, ReportID: r.ReportID, Format: instests.Format(r.Format), TestID: r.TestID, Kind: instests.TestKind(r.Kind),
			Case: instests.Case{Suite: r.Suite, Name: r.Name, File: r.File, Result: instests.Result(r.Result),
				Duration: time.Duration(r.DurationMs) * time.Millisecond, Retries: int(r.Retries), Assertions: int(r.Assertions)},
			FinishedAt: parseTimeOpt(r.FinishedAt),
		})
	}
	return out, nil
}
