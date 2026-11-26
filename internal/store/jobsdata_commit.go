package store

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// MaxCommitRuns caps the runs one commit view reads.
const MaxCommitRuns = 100

// RunsOfCommit returns the latest attempt of every run of one head commit, oldest run first (observability
// R5.13). headSHA is a full commit id or a hex prefix of at least 7 characters; the caller checks that. A zero
// repositoryID looks in every repository.
func (s *JobsStore) RunsOfCommit(ctx context.Context, repositoryID int64, headSHA string) ([]jobsdata.RunDetail, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	query := `SELECT run_id, MAX(run_attempt) FROM runs WHERE head_sha LIKE ? || '%'`
	args := []any{headSHA}
	if repositoryID != 0 {
		query += ` AND repository_id = ?`
		args = append(args, repositoryID)
	}
	rows, err := s.db.QueryContext(ctx, query+` GROUP BY repository_id, run_id ORDER BY MIN(created_at), run_id LIMIT ?`, append(args, MaxCommitRuns)...)
	if err != nil {
		return nil, fmt.Errorf("runs of commit: %w", err)
	}
	type ref struct{ run, attempt int64 }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.run, &r.attempt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("runs of commit: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("runs of commit: %w", err)
	}
	_ = rows.Close()
	out := make([]jobsdata.RunDetail, 0, len(refs))
	for _, r := range refs {
		d, ok, err := s.GetRun(ctx, r.run, int(r.attempt))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, d)
		}
	}
	return out, nil
}
