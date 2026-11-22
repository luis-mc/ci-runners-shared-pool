package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func scopePath(scope RunnerScope) string {
	if scope.Repo == "" {
		return "/orgs/" + url.PathEscape(scope.Owner)
	}
	return "/repos/" + url.PathEscape(scope.Owner) + "/" + url.PathEscape(scope.Repo)
}

func (c *REST) installationReq(ctx context.Context, id int64, r req) (req, error) {
	return c.withInstallation(ctx, id, r)
}

// CreateRegistrationToken implements InstallationClient (R5.31). The token is
// returned to the caller and never stored or logged.
func (c *REST) CreateRegistrationToken(ctx context.Context, id int64, scope RunnerScope) (string, error) {
	return c.runnerToken(ctx, id, scope, "registration-token")
}

// CreateRemoveToken implements InstallationClient (R5.32).
func (c *REST) CreateRemoveToken(ctx context.Context, id int64, scope RunnerScope) (string, error) {
	return c.runnerToken(ctx, id, scope, "remove-token")
}

func (c *REST) runnerToken(ctx context.Context, id int64, scope RunnerScope, kind string) (string, error) {
	r, err := c.installationReq(ctx, id, req{method: "POST", path: scopePath(scope) + "/actions/runners/" + kind, write: true})
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if _, err := c.do(ctx, r, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("github: empty runner token")
	}
	return out.Token, nil
}

// ListRunners implements InstallationClient.
func (c *REST) ListRunners(ctx context.Context, id int64, scope RunnerScope) ([]Runner, error) {
	r, err := c.installationReq(ctx, id, req{path: scopePath(scope) + "/actions/runners"})
	if err != nil {
		return nil, err
	}
	var all []Runner
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Runners []struct {
				ID     int64  `json:"id"`
				Name   string `json:"name"`
				OS     string `json:"os"`
				Status string `json:"status"`
				Busy   bool   `json:"busy"`
				Labels []struct {
					Name string `json:"name"`
				} `json:"labels"`
			} `json:"runners"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, x := range page.Runners {
			rn := Runner{ID: x.ID, Name: x.Name, OS: x.OS, Status: x.Status, Busy: x.Busy}
			for _, l := range x.Labels {
				rn.Labels = append(rn.Labels, l.Name)
			}
			all = append(all, rn)
		}
		return nil
	})
	return all, err
}

// DeleteRunner implements InstallationClient (R5.33).
func (c *REST) DeleteRunner(ctx context.Context, id int64, scope RunnerScope, runnerID int64) error {
	r, err := c.installationReq(ctx, id, req{method: "DELETE", path: fmt.Sprintf("%s/actions/runners/%d", scopePath(scope), runnerID), write: true})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}

func varsPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/variables"
}

// ListVariables implements InstallationClient (R5.28).
func (c *REST) ListVariables(ctx context.Context, id int64, owner, repo string) ([]Variable, error) {
	r, err := c.installationReq(ctx, id, req{path: varsPath(owner, repo)})
	if err != nil {
		return nil, err
	}
	var all []Variable
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, v := range page.Variables {
			all = append(all, Variable{Name: v.Name, Value: v.Value})
		}
		return nil
	})
	return all, err
}

// SetVariable implements InstallationClient: update, and create when missing.
func (c *REST) SetVariable(ctx context.Context, id int64, owner, repo string, v Variable) error {
	r, err := c.installationReq(ctx, id, req{method: "PATCH", path: varsPath(owner, repo) + "/" + url.PathEscape(v.Name), write: true, body: map[string]string{"name": v.Name, "value": v.Value}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	if errors.Is(err, ErrNotFound) {
		r.method, r.path = "POST", varsPath(owner, repo)
		_, err = c.do(ctx, r, nil)
	}
	return err
}

// DeleteVariable implements InstallationClient (R5.29). A variable that is
// already gone is not an error.
func (c *REST) DeleteVariable(ctx context.Context, id int64, owner, repo, name string) error {
	r, err := c.installationReq(ctx, id, req{method: "DELETE", path: varsPath(owner, repo) + "/" + url.PathEscape(name), write: true})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ListWorkflowFiles implements InstallationClient (R5.30).
func (c *REST) ListWorkflowFiles(ctx context.Context, id int64, owner, repo string, etags map[string]string) ([]WorkflowFile, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/contents/"
	r, err := c.installationReq(ctx, id, req{method: "GET", path: base + ".github/workflows"})
	if err != nil {
		return nil, err
	}
	var dir []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if _, err := c.do(ctx, r, &dir); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil // no workflows directory
		}
		return nil, err
	}
	var files []WorkflowFile
	for _, e := range dir {
		ext := strings.ToLower(path.Ext(e.Path))
		if e.Type != "file" || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		fr := r
		fr.path = base + e.Path
		fr.etag = etags[e.Path]
		var f struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		resp, err := c.do(ctx, fr, &f)
		if err != nil {
			return nil, err
		}
		wf := WorkflowFile{Path: e.Path, ETag: resp.etag}
		if resp.status != 304 {
			if f.Encoding != "base64" {
				return nil, fmt.Errorf("github: %s: unexpected encoding %q", e.Path, f.Encoding)
			}
			b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
			if err != nil {
				return nil, fmt.Errorf("github: %s: %w", e.Path, err)
			}
			wf.Content = b
		} else {
			wf.ETag = fr.etag
		}
		files = append(files, wf)
	}
	return files, nil
}

type apiJob struct {
	ID           int64      `json:"id"`
	RunID        int64      `json:"run_id"`
	RunAttempt   int        `json:"run_attempt"`
	Name         string     `json:"name"`
	WorkflowName string     `json:"workflow_name"`
	HeadSHA      string     `json:"head_sha"`
	HeadBranch   string     `json:"head_branch"`
	Status       string     `json:"status"`
	Conclusion   string     `json:"conclusion"`
	RunnerName   string     `json:"runner_name"`
	Labels       []string   `json:"labels"`
	HTMLURL      string     `json:"html_url"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
}

func (j apiJob) job(repo string, run apiRun) Job {
	out := Job{ID: j.ID, RunID: j.RunID, RunAttempt: j.RunAttempt, Repository: repo, Workflow: j.WorkflowName,
		Name: j.Name, HeadSHA: j.HeadSHA, HeadBranch: j.HeadBranch, Labels: j.Labels, Status: j.Status,
		Conclusion: j.Conclusion, RunnerName: j.RunnerName, CreatedAt: j.CreatedAt, HTMLURL: j.HTMLURL,
		Event: run.Event, WorkflowPath: workflowFilePath(run.Path), FromFork: run.HeadRepository.FullName != "" && run.HeadRepository.FullName != repo}
	if j.StartedAt != nil {
		out.StartedAt = *j.StartedAt
	}
	if j.CompletedAt != nil {
		out.CompletedAt = *j.CompletedAt
	}
	return out
}

type apiRun struct {
	ID             int64  `json:"id"`
	Event          string `json:"event"`
	Path           string `json:"path"`
	HeadRepository struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
}

// cachedRuns is the last answer of one runs list with its ETag (R5.37).
type cachedRuns struct {
	etag string
	runs []apiRun
}

// listRuns lists the workflow runs of one status. It sends If-None-Match with
// the ETag of the last answer, and a 304 reuses that answer; GitHub does not
// count a 304 against the rate limit. Only a list that fits one page is
// cached: a later page could change without the first page changing.
func (c *REST) listRuns(ctx context.Context, r req) ([]apiRun, error) {
	r.method = "GET"
	q := url.Values{}
	for k, v := range r.query {
		q[k] = v
	}
	q.Set("per_page", "100")
	r.query = q
	key := fmt.Sprintf("%d %s?%s", r.installation, r.path, q.Encode())
	c.mu.Lock()
	prev, hadPrev := c.runCache[key]
	c.mu.Unlock()
	if hadPrev {
		r.etag = prev.etag
	}
	var raw json.RawMessage
	resp, err := c.do(ctx, r, &raw)
	if err != nil {
		return nil, err
	}
	if resp.status == http.StatusNotModified && hadPrev {
		return prev.runs, nil
	}
	var page struct {
		Runs []apiRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("github: unreadable answer: %w", err)
	}
	runs := page.Runs
	if resp.next != "" {
		more := req{method: "GET", path: resp.next, abs: true, bearer: r.bearer, installation: r.installation}
		err := c.getAll(ctx, more, func(raw json.RawMessage) error {
			var p struct {
				Runs []apiRun `json:"workflow_runs"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			runs = append(runs, p.Runs...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	if resp.next == "" && resp.etag != "" {
		if c.runCache == nil {
			c.runCache = map[string]cachedRuns{}
		}
		c.runCache[key] = cachedRuns{etag: resp.etag, runs: runs}
	} else {
		delete(c.runCache, key)
	}
	c.mu.Unlock()
	return runs, nil
}

// cachedJobs is the last answer of the jobs list of one run with its ETag.
type cachedJobs struct {
	etag string
	jobs []apiJob
}

// listRunJobs lists the jobs of one run. Like listRuns it sends If-None-Match
// with the ETag of the last answer and reuses it on a 304, which GitHub does
// not count against the rate limit (github-integration R5.110). Only a list
// that fits one page is cached.
func (c *REST) listRunJobs(ctx context.Context, r req) ([]apiJob, error) {
	r.method = "GET"
	q := url.Values{}
	for k, v := range r.query {
		q[k] = v
	}
	q.Set("per_page", "100")
	r.query = q
	key := fmt.Sprintf("%d %s?%s", r.installation, r.path, q.Encode())
	c.mu.Lock()
	prev, hadPrev := c.jobCache[key]
	c.mu.Unlock()
	if hadPrev {
		r.etag = prev.etag
	}
	var raw json.RawMessage
	resp, err := c.do(ctx, r, &raw)
	if err != nil {
		return nil, err
	}
	if resp.status == http.StatusNotModified && hadPrev {
		return prev.jobs, nil
	}
	var page struct {
		Jobs []apiJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("github: unreadable answer: %w", err)
	}
	jobs := page.Jobs
	if resp.next != "" {
		more := req{method: "GET", path: resp.next, abs: true, bearer: r.bearer, installation: r.installation}
		err := c.getAll(ctx, more, func(raw json.RawMessage) error {
			var p struct {
				Jobs []apiJob `json:"jobs"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			jobs = append(jobs, p.Jobs...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	if resp.next == "" && resp.etag != "" {
		if c.jobCache == nil {
			c.jobCache = map[string]cachedJobs{}
		}
		if len(c.jobCache) > 4096 {
			c.jobCache = map[string]cachedJobs{} // bound the memory of a long run
		}
		c.jobCache[key] = cachedJobs{etag: resp.etag, jobs: jobs}
	} else {
		delete(c.jobCache, key)
	}
	c.mu.Unlock()
	return jobs, nil
}

// ListOpenJobs implements InstallationClient (R5.37, R5.38): the jobs of
// queued and in-progress runs.
func (c *REST) ListOpenJobs(ctx context.Context, id int64, owner, repo string) ([]Job, error) {
	full := owner + "/" + repo
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runs"
	var out []Job
	seen := map[int64]bool{}
	for _, status := range []string{"queued", "in_progress"} {
		r, err := c.installationReq(ctx, id, req{path: base, query: url.Values{"status": {status}}})
		if err != nil {
			return nil, err
		}
		runs, err := c.listRuns(ctx, r)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			jr := r
			jr.path = fmt.Sprintf("%s/%d/jobs", base, run.ID)
			jr.query = url.Values{"filter": {"latest"}}
			jobs, err := c.listRunJobs(ctx, jr)
			if err != nil {
				return nil, err
			}
			for _, j := range jobs {
				if seen[j.ID] || j.Status == "completed" {
					continue
				}
				seen[j.ID] = true
				out = append(out, j.job(full, run))
			}
		}
	}
	return out, nil
}

// GetJob implements InstallationClient.
func (c *REST) GetJob(ctx context.Context, id int64, owner, repo string, jobID int64) (Job, error) {
	r, err := c.installationReq(ctx, id, req{method: "GET", path: fmt.Sprintf("/repos/%s/%s/actions/jobs/%d", url.PathEscape(owner), url.PathEscape(repo), jobID)})
	if err != nil {
		return Job{}, err
	}
	var j apiJob
	if _, err := c.do(ctx, r, &j); err != nil {
		return Job{}, err
	}
	return j.job(owner+"/"+repo, apiRun{}), nil
}

var _ Client = (*REST)(nil)
