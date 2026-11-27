package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
)

// The two job reads of the Job page faults (docs/features/ci-run-insights R5.40, R5.41). Both are background
// calls: they stop when the installation is at half its hourly limit (github-integration R6.6).

var _ reconcile.GitHub = (*REST)(nil)

type apiRemoteStep struct {
	Number      int        `json:"number"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

type apiRemoteJob struct {
	ID              int64           `json:"id"`
	RunID           int64           `json:"run_id"`
	RunAttempt      int             `json:"run_attempt"`
	Status          string          `json:"status"`
	Conclusion      string          `json:"conclusion"`
	RunnerName      string          `json:"runner_name"`
	RunnerGroupName string          `json:"runner_group_name"`
	Labels          []string        `json:"labels"`
	CreatedAt       *time.Time      `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at"`
	CompletedAt     *time.Time      `json:"completed_at"`
	Steps           []apiRemoteStep `json:"steps"`
}

func zeroTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (j apiRemoteJob) remote() reconcile.RemoteJob {
	out := reconcile.RemoteJob{ID: j.ID, RunID: j.RunID, RunAttempt: j.RunAttempt, Status: j.Status,
		Conclusion: j.Conclusion, RunnerName: j.RunnerName, RunnerGroupName: j.RunnerGroupName, Labels: j.Labels,
		CreatedAt: zeroTime(j.CreatedAt), StartedAt: zeroTime(j.StartedAt), CompletedAt: zeroTime(j.CompletedAt)}
	for _, s := range j.Steps {
		out.Steps = append(out.Steps, reconcile.RemoteStep{Number: s.Number, Name: s.Name, Status: s.Status,
			Conclusion: s.Conclusion, StartedAt: zeroTime(s.StartedAt), CompletedAt: zeroTime(s.CompletedAt)})
	}
	return out
}

func repoActionsPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions"
}

// gone turns a 404 into reconcile.ErrGone.
func gone(err error) error {
	if errors.Is(err, ErrNotFound) {
		return reconcile.ErrGone
	}
	return err
}

// Job reads one job (GET /repos/{owner}/{repo}/actions/jobs/{job_id}), steps included (R5.40).
func (c *REST) Job(ctx context.Context, installationID int64, owner, repo string, jobID int64) (reconcile.RemoteJob, error) {
	r, err := c.withInstallation(ctx, installationID, req{method: "GET",
		path: fmt.Sprintf("%s/jobs/%d", repoActionsPath(owner, repo), jobID)})
	if err != nil {
		return reconcile.RemoteJob{}, err
	}
	var j apiRemoteJob
	if _, err := c.do(Background(ctx), r, &j); err != nil {
		return reconcile.RemoteJob{}, gone(err)
	}
	return j.remote(), nil
}

// RunJobs reads every job of one run attempt, all pages (R5.41).
func (c *REST) RunJobs(ctx context.Context, installationID int64, owner, repo string, runID int64, attempt int) ([]reconcile.RemoteJob, error) {
	r, err := c.withInstallation(ctx, installationID, req{
		path: fmt.Sprintf("%s/runs/%d/attempts/%d/jobs", repoActionsPath(owner, repo), runID, attempt)})
	if err != nil {
		return nil, err
	}
	var out []reconcile.RemoteJob
	err = c.getAll(Background(ctx), r, func(raw json.RawMessage) error {
		var page struct {
			Jobs []apiRemoteJob `json:"jobs"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, j := range page.Jobs {
			out = append(out, j.remote())
		}
		return nil
	})
	if err != nil {
		return nil, gone(err)
	}
	return out, nil
}

// ParseRunnerFields reads the runner name, runner group and labels of a workflow_job webhook body (R5.39).
// The plain Job of ParseWorkflowJobEvent carries no group, so the ingest reads it here.
func ParseRunnerFields(body []byte) (name, group string, labels []string, err error) {
	var ev struct {
		WorkflowJob struct {
			RunnerName      string   `json:"runner_name"`
			RunnerGroupName string   `json:"runner_group_name"`
			Labels          []string `json:"labels"`
		} `json:"workflow_job"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", "", nil, fmt.Errorf("github: unreadable workflow_job: %w", err)
	}
	w := ev.WorkflowJob
	return w.RunnerName, w.RunnerGroupName, w.Labels, nil
}
