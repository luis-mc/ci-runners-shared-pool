package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// SplitJob splits the Result.Job a Store returns, "<path>#<job>" (R5.29: the stored workflow path comes
// with the job; the contract Result has no path field). Without "#" the whole value is the path.
func SplitJob(s string) (path, job string) {
	p, j, _ := strings.Cut(s, "#")
	return p, j
}

// JoinJob is the inverse of SplitJob.
func JoinJob(path, job string) string {
	if job == "" {
		return path
	}
	return path + "#" + job
}

// Repo is one watched repository.
type Repo struct {
	ID   int64
	Name string // owner/repo
}

// Repos lists the repositories crsp watches.
type Repos interface {
	Repositories(ctx context.Context) ([]Repo, error)
}

// Analyser implements insights.Analyser (R5.30): one finding per repository and failing check of the latest
// default-branch commit checked, with the jobs and lines in the evidence.
type Analyser struct {
	Repos Repos
	Store Store
	Jobs  insights.JobSource // optional: proves minutes of timed-out jobs
}

var _ insights.Analyser = Analyser{}

// Name is the analyser name for logs.
func (Analyser) Name() string { return "workflows" }

// Kinds are the kinds the analyser owns.
func (Analyser) Kinds() []insights.Kind { return Kinds }

// evidenceItem is one failing job in a finding's evidence.
type evidenceItem struct {
	Workflow string `json:"workflow"`
	Job      string `json:"job"`
	Line     int    `json:"line"`
	Detail   string `json:"detail"`
}

// Analyse reads the latest results of every repository.
func (a Analyser) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	repos, err := a.Repos.Repositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	var out []insights.Finding
	for _, r := range repos {
		sha, results, err := a.Store.Latest(ctx, r.ID)
		if err != nil {
			return nil, fmt.Errorf("read workflow checks of %s: %w", r.Name, err)
		}
		if sha == "" {
			continue
		}
		over := map[string]int{}
		if a.Jobs != nil {
			over, err = a.minutesOver30(ctx, w, r.ID)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, findings(r, results, over)...)
	}
	return out, nil
}

// minutesOver30 sums, per "<path>#<job>", the minutes the jobs of the window ran past the limit (R5.30: a
// timed-out job that ran past 30 min).
func (a Analyser) minutesOver30(ctx context.Context, w insights.Window, repoID int64) (map[string]int, error) {
	jobs, err := a.Jobs.JobsBetween(ctx, w, repoID)
	if err != nil {
		return nil, fmt.Errorf("read jobs of repository %d: %w", repoID, err)
	}
	over := map[string]int{}
	for _, j := range jobs {
		if j.StartedAt.IsZero() || j.CompletedAt.IsZero() {
			continue
		}
		if d := j.CompletedAt.Sub(j.StartedAt); d > MaxTimeoutMinutes*time.Minute {
			id := j.NodeID
			if id == "" {
				id = j.JobName
			}
			over[JoinJob(j.Workflow, id)] += int((d - MaxTimeoutMinutes*time.Minute).Minutes())
		}
	}
	return over, nil
}

func findings(r Repo, results []Result, over map[string]int) []insights.Finding {
	byCheck := map[CheckID][]Result{}
	for _, res := range results {
		if res.Status == Fail {
			byCheck[res.Check] = append(byCheck[res.Check], res)
		}
	}
	var out []insights.Finding
	for _, c := range AllChecks {
		fails := byCheck[c]
		if len(fails) == 0 {
			continue
		}
		sort.Slice(fails, func(i, j int) bool {
			if fails[i].Job != fails[j].Job {
				return fails[i].Job < fails[j].Job
			}
			return fails[i].Line < fails[j].Line
		})
		var items []evidenceItem
		minutes := 0
		for _, f := range fails {
			path, job := SplitJob(f.Job)
			items = append(items, evidenceItem{Workflow: path, Job: job, Line: f.Line, Detail: f.Detail})
			if c == CheckTimeout {
				minutes += over[f.Job]
			}
		}
		ev, _ := json.Marshal(map[string]any{"checks": items})
		out = append(out, insights.Finding{
			Kind: insights.Kind(c), Scope: insights.ScopeRepo, RepositoryID: r.ID, Repository: r.Name,
			Subject: string(c), Area: insights.AreaWorkflows, Title: title(c, r.Name, len(fails)),
			Fix: fails[0].Detail, Evidence: ev, MinutesSavedWeek: minutes, Guardrail: guardrailOf(c),
		})
	}
	return out
}

func title(c CheckID, repo string, n int) string {
	what := map[CheckID]string{
		CheckTimeout:       "without timeout-minutes or over 60",
		CheckRetry:         "that retry tests",
		CheckMatrixAxis:    "with a matrix that grew",
		CheckStepOrder:     "that run lint or type checks too late",
		CheckCache:         "that set up a package manager without its cache",
		CheckSlowTestsOnPR: "that run slow tests on pull requests",
	}[c]
	noun := "jobs"
	if n == 1 {
		noun = "job"
	}
	return fmt.Sprintf("%s: %d %s %s", repo, n, noun, what)
}
