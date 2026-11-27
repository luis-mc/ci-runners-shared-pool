package github

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// JobStep is one step of a job as the workflow_job event lists it (observability D5).
type JobStep struct {
	Number      int
	Name        string
	Status      string // queued, in_progress or completed
	Conclusion  string // success, failure, cancelled, skipped, or "" while open
	StartedAt   time.Time
	CompletedAt time.Time
}

// JobEvent is a workflow_job webhook with the parts the plain Job leaves out:
// the steps and the run's own fields.
type JobEvent struct {
	Action string
	Job    Job
	Steps  []JobStep
	// RunURL is the API address of the run (workflow_job.run_url), "" when absent.
	RunURL string
	// RunHTMLURL is the run's page, cut from the job page address; "" when that is unknown.
	RunHTMLURL string
}

type apiStep struct {
	Number      int        `json:"number"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// ParseWorkflowJobDetail reads a workflow_job webhook body with its steps and
// run fields. The Job in it is the one ParseWorkflowJobEvent returns. The event
// carries no pull request number and no workflow file path, so neither is read.
func ParseWorkflowJobDetail(body []byte) (JobEvent, error) {
	var ev struct {
		Action      string `json:"action"`
		WorkflowJob struct {
			apiJob
			RunURL string    `json:"run_url"`
			Steps  []apiStep `json:"steps"`
		} `json:"workflow_job"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return JobEvent{}, err
	}
	if ev.WorkflowJob.ID == 0 || ev.Repository.FullName == "" {
		return JobEvent{}, errors.New("workflow_job: missing job or repository")
	}
	j := ev.WorkflowJob.job(ev.Repository.FullName, apiRun{})
	// Fork and event come from the run, which the event omits; the poller and
	// reconcile fill them in. A webhook never claims "not from a fork".
	j.FromFork, j.Partial = false, true
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	out := JobEvent{Action: ev.Action, Job: j, RunURL: ev.WorkflowJob.RunURL}
	if i := strings.Index(j.HTMLURL, "/job/"); i > 0 {
		out.RunHTMLURL = j.HTMLURL[:i]
	}
	for _, s := range ev.WorkflowJob.Steps {
		st := JobStep{Number: s.Number, Name: s.Name, Status: s.Status, Conclusion: s.Conclusion}
		if s.StartedAt != nil {
			st.StartedAt = *s.StartedAt
		}
		if s.CompletedAt != nil {
			st.CompletedAt = *s.CompletedAt
		}
		out.Steps = append(out.Steps, st)
	}
	return out, nil
}

// JobDetailEvents is implemented by an events receiver that wants the steps
// and run fields of a workflow_job event as well as the Job. The handler calls
// it after Job.
type JobDetailEvents interface {
	JobDetail(JobEvent)
}

// workflowFilePath turns the path of a run, ".github/workflows/ci.yml@refs/heads/main",
// into the file path.
func workflowFilePath(p string) string {
	p, _, _ = strings.Cut(p, "@")
	return p
}
