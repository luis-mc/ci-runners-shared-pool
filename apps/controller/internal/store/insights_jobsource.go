package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// This file implements insights.JobSource over the jobs and runs tables (R5.1): every job attempt created in
// a window, of every runner origin, with unknown times as zero values (observability R5.9).

var _ insights.JobSource = (*InsightJobSource)(nil)

// InsightJobSource reads job facts for the analysers.
type InsightJobSource struct{ s *Store }

// InsightJobSource returns the job source of s.
func (s *Store) InsightJobSource() *InsightJobSource { return &InsightJobSource{s: s} }

// boundText formats a window bound so that it compares correctly with stored times whether or not they carry
// milliseconds: stored "…:05Z" and "…:05.000Z" are both >= "…:05" and < "…:06".
func boundText(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05") }

func decodeLabels(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// JobsBetween returns the job attempts created in [w.From, w.To), oldest first.
func (j *InsightJobSource) JobsBetween(ctx context.Context, w insights.Window, repositoryID int64) ([]insights.JobFact, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := j.s.db.QueryContext(ctx, `
SELECT j.id, j.repository_id, j.repository, j.run_id, j.run_attempt, j.workflow, j.job_ident, j.name, j.branch,
       j.head_sha, j.event, COALESCE(r.pull_request, 0), COALESCE(repo.default_branch, ''), j.labels,
       j.runner_origin, j.runner_name, j.runner_group, j.runner_labels, j.machine_id, j.light, j.result,
       j.wait_reason, j.created_at, j.queued_at, j.slot_at, j.started_at, j.completed_at
FROM jobs j
LEFT JOIN runs r ON r.repository_id = j.repository_id AND r.run_id = j.run_id AND r.run_attempt = j.run_attempt
LEFT JOIN repositories repo ON repo.id = j.repository_id
WHERE j.created_at >= ? AND j.created_at < ? AND (? = 0 OR j.repository_id = ?)
ORDER BY j.created_at ASC, j.id ASC`, boundText(w.From), boundText(w.To), repositoryID, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("insight jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []insights.JobFact
	for rows.Next() {
		var f insights.JobFact
		var defBranch, labels, runnerLabels, origin, created, queued, slot, started, completed string
		var light int
		if err := rows.Scan(&f.ID, &f.RepositoryID, &f.Repository, &f.RunID, &f.RunAttempt, &f.Workflow, &f.JobIdent,
			&f.JobName, &f.Branch, &f.HeadSHA, &f.Event, &f.PullRequest, &defBranch, &labels, &origin, &f.RunnerName,
			&f.RunnerGroup, &runnerLabels, &f.MachineID, &light, &f.Result, &f.WaitReason, &created, &queued, &slot,
			&started, &completed); err != nil {
			return nil, fmt.Errorf("insight jobs scan: %w", err)
		}
		f.DefaultBranch = defBranch != "" && f.Branch == defBranch
		f.Labels = decodeLabels(labels)
		f.RunnerLabels = decodeLabels(runnerLabels)
		f.Origin = insights.Origin(origin)
		f.Light = light != 0
		f.CreatedAt, f.QueuedAt, f.SlotAt = parseTimeOpt(created), parseTimeOpt(queued), parseTimeOpt(slot)
		f.StartedAt, f.CompletedAt = parseTimeOpt(started), parseTimeOpt(completed)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insight jobs rows: %w", err)
	}
	return out, nil
}
