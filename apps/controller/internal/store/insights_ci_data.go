package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Index rows of the stored CI files (ci-run-insights R5.2, R5.3, R5.6). The files themselves are in the CI
// data folder; these rows say where, how big and until when. Times are UTC RFC3339 text, "" when unknown.
// A row never holds a log line, an env value or a secret (R5.4): only paths, sizes, hashes and states.
type (
	// CIJobLog is one ci_job_logs row.
	CIJobLog = sqlcgen.CiJobLog
	// CIReportFile is one ci_report_files row.
	CIReportFile = sqlcgen.CiReportFile
)

// CIDataBytes is the stored size of one repository's CI files.
type CIDataBytes struct {
	RepositoryID int64
	Bytes        int64
}

// SaveCIJobLog inserts or replaces the index row of one job log.
func (s *Store) SaveCIJobLog(ctx context.Context, l CIJobLog) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := s.q.UpsertCIJobLog(ctx, sqlcgen.UpsertCIJobLogParams(l))
	if err != nil {
		return fmt.Errorf("save the log row of job %d: %w", l.JobID, err)
	}
	return nil
}

// CIJobLogOf returns the row of one job, and false when the job has none.
func (s *Store) CIJobLogOf(ctx context.Context, jobID int64) (CIJobLog, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	l, err := s.q.GetCIJobLog(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return CIJobLog{}, false, nil
	}
	if err != nil {
		return CIJobLog{}, false, fmt.Errorf("read the log row of job %d: %w", jobID, err)
	}
	return l, true, nil
}

// CIJobLogsOldestFirst lists the stored logs of a repository, oldest fetched first.
func (s *Store) CIJobLogsOldestFirst(ctx context.Context, repositoryID int64, limit int) ([]CIJobLog, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListCIJobLogsOldestFirst(ctx, sqlcgen.ListCIJobLogsOldestFirstParams{RepositoryID: repositoryID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list the logs of repository %d: %w", repositoryID, err)
	}
	return rows, nil
}

// ExpiredCIJobLogs lists the stored logs whose expiry time is before now.
func (s *Store) ExpiredCIJobLogs(ctx context.Context, now string, limit int) ([]CIJobLog, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListExpiredCIJobLogs(ctx, sqlcgen.ListExpiredCIJobLogsParams{ExpiresAt: now, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list the expired logs: %w", err)
	}
	return rows, nil
}

// MarkCIJobLogExpired keeps the row of a log whose file was deleted.
func (s *Store) MarkCIJobLogExpired(ctx context.Context, jobID int64) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := s.q.MarkCIJobLogExpired(ctx, jobID); err != nil {
		return fmt.Errorf("mark the log of job %d expired: %w", jobID, err)
	}
	return nil
}

// SaveCIReportFile inserts or replaces the index row of one report file and returns its id.
func (s *Store) SaveCIReportFile(ctx context.Context, f CIReportFile) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	id, err := s.q.InsertCIReportFile(ctx, sqlcgen.InsertCIReportFileParams{
		RepositoryID: f.RepositoryID, RunID: f.RunID, RunAttempt: f.RunAttempt, ArtifactID: f.ArtifactID,
		ArtifactName: f.ArtifactName, Member: f.Member, Format: f.Format, Path: f.Path, Bytes: f.Bytes,
		State: f.State, Reason: f.Reason, FetchedAt: f.FetchedAt, ExpiresAt: f.ExpiresAt,
	})
	if err != nil {
		return 0, fmt.Errorf("save the report row of artifact %d: %w", f.ArtifactID, err)
	}
	return id, nil
}

// CIReportFileOf returns the row of one artifact member, and false when there is none.
func (s *Store) CIReportFileOf(ctx context.Context, repositoryID, artifactID int64, member string) (CIReportFile, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	f, err := s.q.GetCIReportFile(ctx, sqlcgen.GetCIReportFileParams{RepositoryID: repositoryID, ArtifactID: artifactID, Member: member})
	if errors.Is(err, sql.ErrNoRows) {
		return CIReportFile{}, false, nil
	}
	if err != nil {
		return CIReportFile{}, false, fmt.Errorf("read the report row of artifact %d: %w", artifactID, err)
	}
	return f, true, nil
}

// CIReportFilesForRun lists the report rows of one run.
func (s *Store) CIReportFilesForRun(ctx context.Context, repositoryID, runID int64) ([]CIReportFile, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListCIReportFilesForRun(ctx, sqlcgen.ListCIReportFilesForRunParams{RepositoryID: repositoryID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("list the reports of run %d: %w", runID, err)
	}
	return rows, nil
}

// CIReportFilesOldestFirst lists the stored report files of a repository, oldest fetched first.
func (s *Store) CIReportFilesOldestFirst(ctx context.Context, repositoryID int64, limit int) ([]CIReportFile, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListCIReportFilesOldestFirst(ctx, sqlcgen.ListCIReportFilesOldestFirstParams{RepositoryID: repositoryID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list the reports of repository %d: %w", repositoryID, err)
	}
	return rows, nil
}

// ExpiredCIReportFiles lists the stored report files whose expiry time is before now.
func (s *Store) ExpiredCIReportFiles(ctx context.Context, now string, limit int) ([]CIReportFile, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListExpiredCIReportFiles(ctx, sqlcgen.ListExpiredCIReportFilesParams{ExpiresAt: now, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list the expired reports: %w", err)
	}
	return rows, nil
}

// MarkCIReportFileExpired keeps the row of a report file that was deleted.
func (s *Store) MarkCIReportFileExpired(ctx context.Context, id int64) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := s.q.MarkCIReportFileExpired(ctx, id); err != nil {
		return fmt.Errorf("mark report %d expired: %w", id, err)
	}
	return nil
}

// CIDataBytesByRepository is the stored size of the CI files of every repository that has some.
func (s *Store) CIDataBytesByRepository(ctx context.Context) ([]CIDataBytes, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.CIDataBytesByRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("sum the CI data sizes: %w", err)
	}
	out := make([]CIDataBytes, 0, len(rows))
	for _, r := range rows {
		out = append(out, CIDataBytes{RepositoryID: r.RepositoryID, Bytes: r.Bytes})
	}
	return out, nil
}
