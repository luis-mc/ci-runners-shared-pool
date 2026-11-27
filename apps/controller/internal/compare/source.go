package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Visibility values of a repository.
const (
	VisibilityPublic   = "public"
	VisibilityPrivate  = "private"
	VisibilityInternal = "internal"
)

// Job is one finished workflow-run job.
type Job struct {
	Name        string
	Labels      []string
	Conclusion  string
	CreatedAt   time.Time // queued
	StartedAt   time.Time
	CompletedAt time.Time
}

// Source reads what the comparison needs from GitHub. Tests use a fake.
type Source interface {
	// Visibility returns public, private or internal.
	Visibility(ctx context.Context, owner, repo string) (string, error)
	// Jobs returns the jobs of completed workflow runs created at or after since.
	Jobs(ctx context.Context, owner, repo string, since time.Time) ([]Job, error)
}

// API is a Source over the GitHub REST API.
type API struct {
	// Base is the API address; empty uses addresses.DefaultGitHubAPIURL.
	Base string
	HTTP *http.Client
	// Bearer returns the credential for a repository's calls.
	Bearer func(ctx context.Context, owner, repo string) (string, error)
	// MaxRuns bounds how many runs are read; zero uses 200.
	MaxRuns int
}

const apiVersion = "2022-11-28"

func (a *API) get(ctx context.Context, owner, repo, path string, q url.Values, out any) error {
	base := strings.TrimRight(a.Base, "/")
	if base == "" {
		base = addresses.DefaultGitHubAPIURL
	}
	h := a.HTTP
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	if a.Bearer == nil {
		return errors.New("compare: no credential source")
	}
	cred, err := a.Bearer(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("compare: credential: %w", err)
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "crsp")
	req.Header.Set("Authorization", "Bearer "+cred)
	resp, err := h.Do(req)
	if err != nil {
		return fmt.Errorf("compare: github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("compare: read github answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("compare: github answered %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("compare: read github answer: %w", err)
	}
	return nil
}

// ErrNotFound: the repository is not visible to crsp.
var ErrNotFound = errors.New("compare: repository not found")

// Visibility implements Source.
func (a *API) Visibility(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		Visibility string `json:"visibility"`
		Private    bool   `json:"private"`
	}
	if err := a.get(ctx, owner, repo, "/repos/"+owner+"/"+repo, nil, &r); err != nil {
		return "", err
	}
	if r.Visibility != "" {
		return r.Visibility, nil
	}
	if r.Private {
		return VisibilityPrivate, nil
	}
	return VisibilityPublic, nil
}

// Jobs implements Source: completed runs since the date, then each run's latest jobs.
func (a *API) Jobs(ctx context.Context, owner, repo string, since time.Time) ([]Job, error) {
	limit := a.MaxRuns
	if limit <= 0 {
		limit = 200
	}
	var ids []int64
	for page := 1; len(ids) < limit && page <= 20; page++ {
		var runs struct {
			Runs []struct {
				ID int64 `json:"id"`
			} `json:"workflow_runs"`
		}
		q := url.Values{
			"status":   {"completed"},
			"created":  {">=" + since.UTC().Format("2006-01-02")},
			"per_page": {"100"},
			"page":     {fmt.Sprint(page)},
		}
		if err := a.get(ctx, owner, repo, "/repos/"+owner+"/"+repo+"/actions/runs", q, &runs); err != nil {
			return nil, err
		}
		for _, r := range runs.Runs {
			if len(ids) < limit {
				ids = append(ids, r.ID)
			}
		}
		if len(runs.Runs) < 100 {
			break
		}
	}
	var out []Job
	for _, id := range ids {
		var jobs struct {
			Jobs []struct {
				Name        string    `json:"name"`
				Labels      []string  `json:"labels"`
				Conclusion  string    `json:"conclusion"`
				CreatedAt   time.Time `json:"created_at"`
				StartedAt   time.Time `json:"started_at"`
				CompletedAt time.Time `json:"completed_at"`
			} `json:"jobs"`
		}
		q := url.Values{"filter": {"latest"}, "per_page": {"100"}}
		if err := a.get(ctx, owner, repo, fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", owner, repo, id), q, &jobs); err != nil {
			return nil, err
		}
		for _, j := range jobs.Jobs {
			out = append(out, Job{Name: j.Name, Labels: j.Labels, Conclusion: j.Conclusion,
				CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, CompletedAt: j.CompletedAt})
		}
	}
	return out, nil
}
