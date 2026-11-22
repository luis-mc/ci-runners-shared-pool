package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
)

// Repo is the repository a call is about.
type Repo struct {
	Installation  int64
	Owner, Name   string
	DefaultBranch string
}

// Full is owner/name.
func (r Repo) Full() string { return r.Owner + "/" + r.Name }

// Errors.
var (
	// ErrNotOwned: crsp opens pull requests only on the owner's repositories.
	ErrNotOwned = errors.New("routing: pull requests are opened on the owner's repositories only")
	// ErrLabelCap: the runner would pass the label cap.
	ErrLabelCap = errors.New("routing: the runner would pass the label cap")
)

// Dependencies, each small so a test passes a fake.
type (
	// Scans scans a commit and reads the stored rows (jobsize, OPS67).
	Scans interface {
		ScanRepo(ctx context.Context, repository, commit string) error
		ScanRows(ctx context.Context, repository string) ([]jobsize.ScanRow, error)
	}
	// Variables is the repository variables of GitHub.
	Variables interface {
		ListVariables(ctx context.Context, installationID int64, owner, repo string) ([]github.Variable, error)
		SetVariable(ctx context.Context, installationID int64, owner, repo string, v github.Variable) error
		DeleteVariable(ctx context.Context, installationID int64, owner, repo, name string) error
	}
	// Files reads the workflow files of the default branch.
	Files interface {
		WorkflowFiles(ctx context.Context, r Repo) (map[string][]byte, error)
	}
	// Runs says when each run that is queued or running was created.
	Runs interface {
		OpenRunStarts(ctx context.Context, r Repo) ([]time.Time, error)
	}
)

// Service keeps the per-job variables and labels of repositories.
type Service struct {
	Store    Store
	Scans    Scans
	Vars     Variables
	Files    Files
	Runs     Runs
	Labeler  github.RunnerLabeler
	PRs      github.PullRequester
	Throttle *Throttle
	Now      func() time.Time
	// Cap is the label cap of a runner; 0 means LabelCap. Reserved is how many
	// labels a runner carries before routing labels; 0 means 10.
	Cap, Reserved int
	// PROwners are the owners whose repositories get the pull request;
	// empty means "luis-mc".
	PROwners []string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) wait(ctx context.Context) error {
	if s.Throttle == nil {
		return ctx.Err()
	}
	return s.Throttle.Wait(ctx)
}

func (s *Service) reserved() int {
	if s.Reserved > 0 {
		return s.Reserved
	}
	return 10
}

// Result says what a sync did.
type Result struct {
	Written   []string // variables written
	Unchanged int      // variables already right, not written
	Deleted   []string // variables of jobs that are gone
	Skipped   []string // "job: reason" for jobs left on the fallback
	Dropped   []string // jobs left without a label by the cap
	ByClass   bool
	Retired   []string // labels removed from runners by the sweep
}

// OptIn runs when a repository is opted in: it reads the head of the default
// branch, scans it and writes the variables.
func (s *Service) OptIn(ctx context.Context, r Repo) (Result, error) {
	sha, err := s.PRs.BranchHead(ctx, r.Installation, r.Owner, r.Name, r.DefaultBranch)
	if err != nil {
		return Result{}, err
	}
	return s.Sync(ctx, r, sha)
}

// OnPush runs on each push webhook: a push to another branch does nothing; a
// push to the default branch scans again and updates only what changed.
func (s *Service) OnPush(ctx context.Context, r Repo, ref, sha string) (Result, error) {
	if ref != "refs/heads/"+r.DefaultBranch {
		return Result{}, nil
	}
	return s.Sync(ctx, r, sha)
}

func osVar(os string) string { return PoolVarPrefix + strings.ToUpper(os) }

// poolValue is the value of the pool variable of an OS; while an upgrade has
// not written CRSP_RUNS_ON_<OS> yet, the name older versions wrote
// (RUNS_ON_<OS>) stands in.
func poolValue(have map[string]string, os string) string {
	if v, ok := have[osVar(os)]; ok {
		return v
	}
	return have[strings.TrimPrefix(osVar(os), VarPrefix)]
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func parseList(v string) []string {
	var l []string
	if json.Unmarshal([]byte(v), &l) != nil {
		return nil
	}
	return l
}

func hasSelfHosted(l []string) bool {
	for _, x := range l {
		if strings.EqualFold(x, "self-hosted") {
			return true
		}
	}
	return false
}

// Sync scans the commit (when sha is not empty), builds the label plan and
// writes the variable of each routed job whose value changed. A job with no
// scan row or no pool variable keeps the fallback and is never blocked. A rate
// limit stops the run, keeps what was written and returns the error.
func (s *Service) Sync(ctx context.Context, r Repo, sha string) (Result, error) {
	var res Result
	if sha != "" {
		if err := s.Scans.ScanRepo(ctx, r.Full(), sha); err != nil {
			return res, err
		}
	}
	rows, err := s.Scans.ScanRows(ctx, r.Full())
	if err != nil {
		return res, err
	}
	files, err := s.Files.WorkflowFiles(ctx, r)
	if err != nil {
		return res, err
	}
	listed, err := s.Vars.ListVariables(github.Background(ctx), r.Installation, r.Owner, r.Name)
	if err != nil {
		return res, err
	}
	have := map[string]string{}
	for _, v := range listed {
		have[strings.ToUpper(v.Name)] = v.Value
	}
	prev, err := s.Store.Load(ctx, r.Full())
	if err != nil {
		return res, err
	}

	routed := Routed(files)
	var allKeys []string
	for _, j := range routed {
		allKeys = append(allKeys, j.Key)
	}
	names := VarNames(allKeys)
	scan := map[string]jobsize.ScanRow{}
	for _, row := range rows {
		scan[row.Job] = row
	}
	base := map[string][]string{}
	var jobs []Job
	seen := map[string]bool{}
	for _, j := range routed {
		if seen[j.Key] {
			continue
		}
		seen[j.Key] = true
		row, ok := scan[j.Key]
		if !ok {
			res.Skipped = append(res.Skipped, j.Key+": not scanned yet")
			continue
		}
		b, ok := base[j.OS]
		if !ok {
			b = StripLabels(parseList(poolValue(have, j.OS)))
			base[j.OS] = b
		}
		if !hasSelfHosted(b) {
			res.Skipped = append(res.Skipped, j.Key+": "+osVar(j.OS)+" does not name the pool")
			continue
		}
		jobs = append(jobs, Job{Key: j.Key, Class: SizeClassName(row.Class), OS: j.OS})
	}
	plan := MakePlan(r.Full(), jobs, names, s.reserved(), s.Cap)
	res.ByClass, res.Dropped = plan.ByClass, plan.Dropped

	prevJob := map[string]JobState{}
	for _, j := range prev.Jobs {
		prevJob[j.Key] = j
	}
	next := map[string]JobState{}
	var stop error
	for _, a := range plan.Jobs {
		want := append(append([]string(nil), base[a.Job.OS]...), a.Label)
		raw, _ := json.Marshal(want)
		st := JobState{Key: a.Job.Key, Var: a.Var, Label: a.Label, Class: a.Job.Class, OS: a.Job.OS, Value: string(raw)}
		if stop != nil {
			if p, ok := prevJob[a.Job.Key]; ok {
				next[a.Job.Key] = p
			}
			continue
		}
		if cur, ok := have[a.Var]; ok && sameList(parseList(cur), want) {
			res.Unchanged++
			next[a.Job.Key] = st
			continue
		}
		if err := s.wait(ctx); err != nil {
			return res, err
		}
		if err := s.Vars.SetVariable(github.Background(ctx), r.Installation, r.Owner, r.Name, github.Variable{Name: a.Var, Value: st.Value}); err != nil {
			stop = s.noteLimit(err)
			if p, ok := prevJob[a.Job.Key]; ok {
				next[a.Job.Key] = p
			}
			continue
		}
		res.Written = append(res.Written, a.Var)
		next[a.Job.Key] = st
	}
	// A job that is gone, or dropped by the cap: its variable goes, its label retires.
	if stop == nil {
		for _, p := range prev.Jobs {
			if _, ok := next[p.Key]; ok {
				continue
			}
			if IsPoolVar(p.Var) {
				continue // a pool variable is never a stale per-job one
			}
			if _, exists := have[p.Var]; exists {
				if err := s.wait(ctx); err != nil {
					return res, err
				}
				if err := s.Vars.DeleteVariable(github.Background(ctx), r.Installation, r.Owner, r.Name, p.Var); err != nil {
					stop = s.noteLimit(err)
					next[p.Key] = p
					continue
				}
				res.Deleted = append(res.Deleted, p.Var)
			}
		}
	} else {
		for _, p := range prev.Jobs {
			if _, ok := next[p.Key]; !ok {
				next[p.Key] = p
			}
		}
	}

	st := s.settle(prev, next)
	if err := s.Store.Save(ctx, r.Full(), st); err != nil {
		return res, err
	}
	if stop != nil {
		return res, stop
	}
	res.Retired, err = s.Sweep(ctx, r)
	return res, err
}

// noteLimit holds the throttle after a rate limit and returns the error to
// stop on; any other error also stops the run.
func (s *Service) noteLimit(err error) error {
	var rl *github.RateLimitError
	if errors.As(err, &rl) && s.Throttle != nil {
		s.Throttle.Hold(rl.ResetAt)
	}
	return err
}

// settle builds the new state: the active labels are those of the jobs now; a
// label a job left starts to retire at now, and one already retiring keeps its
// start.
func (s *Service) settle(prev State, next map[string]JobState) State {
	st := State{Runners: prev.Runners, PR: prev.PR}
	keys := make([]string, 0, len(next))
	for k := range next {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	active := map[string]*LabelState{}
	var order []string
	for _, k := range keys {
		j := next[k]
		st.Jobs = append(st.Jobs, j)
		if l, ok := active[j.Label]; ok {
			l.Jobs = append(l.Jobs, j.Key)
			continue
		}
		since := s.now()
		for _, p := range prev.Labels {
			if p.Label == j.Label && p.State == LabelActive {
				since = p.Since
			}
		}
		active[j.Label] = &LabelState{Label: j.Label, State: LabelActive, Since: since, Jobs: []string{j.Key}}
		order = append(order, j.Label)
	}
	retiring := map[string]*LabelState{}
	for _, p := range prev.Labels {
		if _, still := active[p.Label]; still {
			continue
		}
		if p.State == LabelRetiring {
			c := p
			retiring[p.Label] = &c
			continue
		}
		c := p
		c.State, c.Since = LabelRetiring, s.now()
		retiring[p.Label] = &c
	}
	for _, l := range order {
		st.Labels = append(st.Labels, *active[l])
	}
	var rk []string
	for l := range retiring {
		rk = append(rk, l)
	}
	sort.Strings(rk)
	for _, l := range rk {
		st.Labels = append(st.Labels, *retiring[l])
	}
	return st
}

// Sweep removes the retiring labels no run needs any more: a label goes when
// no run created before it began to retire is queued or running. The old label
// is served until then. It returns the labels it removed.
func (s *Service) Sweep(ctx context.Context, r Repo) ([]string, error) {
	st, err := s.Store.Load(ctx, r.Full())
	if err != nil {
		return nil, err
	}
	var retiring bool
	for _, l := range st.Labels {
		retiring = retiring || l.State == LabelRetiring
	}
	if !retiring {
		return nil, nil
	}
	starts, err := s.Runs.OpenRunStarts(ctx, r)
	if err != nil {
		return nil, err
	}
	var removed []string
	var keep []LabelState
	for _, l := range st.Labels {
		if l.State != LabelRetiring || blocked(starts, l.Since) {
			keep = append(keep, l)
			continue
		}
		var left []RunnerLabel
		failed := false
		for _, rl := range st.Runners {
			if rl.Label != l.Label {
				left = append(left, rl)
				continue
			}
			if failed {
				left = append(left, rl)
				continue
			}
			if err := s.wait(ctx); err != nil {
				return removed, err
			}
			scope := github.RunnerScope{Owner: rl.ScopeOwner, Repo: rl.ScopeRepo, Organization: rl.ScopeOrg}
			if err := s.Labeler.RemoveRunnerLabel(github.Background(ctx), r.Installation, scope, rl.GitHubID, rl.Label); err != nil {
				_ = s.noteLimit(err)
				failed = true
				left = append(left, rl)
			}
		}
		st.Runners = left
		if failed {
			keep = append(keep, l)
			continue
		}
		removed = append(removed, l.Label)
	}
	st.Labels = keep
	if err := s.Store.Save(ctx, r.Full(), st); err != nil {
		return removed, err
	}
	return removed, nil
}

// blocked says whether a run created before since is still open.
func blocked(starts []time.Time, since time.Time) bool {
	for _, t := range starts {
		if t.Before(since) {
			return true
		}
	}
	return false
}

// RunnerRef is a runner crsp wants a label on.
type RunnerRef struct {
	ID         string
	GitHubID   int64
	Persistent bool
	ScopeOwner string
	ScopeRepo  string
	ScopeOrg   string
}

// Assign puts a label on the runners crsp chose for a job. An ephemeral runner
// is skipped: it gets the label at creation (see ServedLabels). A persistent
// runner gets it as a custom label, once; the add that would pass the cap is
// refused with ErrLabelCap.
func (s *Service) Assign(ctx context.Context, r Repo, label string, runners []RunnerRef) error {
	st, err := s.Store.Load(ctx, r.Full())
	if err != nil {
		return err
	}
	cap := s.Cap
	if cap <= 0 {
		cap = LabelCap
	}
	for _, rn := range runners {
		if !rn.Persistent {
			continue
		}
		n, has := s.reserved(), false
		for _, rl := range st.Runners {
			if rl.RunnerID == rn.ID {
				n++
				has = has || rl.Label == label
			}
		}
		if has {
			continue
		}
		if n+1 > cap {
			_ = s.Store.Save(ctx, r.Full(), st)
			return fmt.Errorf("%w: %s", ErrLabelCap, rn.ID)
		}
		if err := s.wait(ctx); err != nil {
			return err
		}
		scope := github.RunnerScope{Owner: rn.ScopeOwner, Repo: rn.ScopeRepo, Organization: rn.ScopeOrg}
		if err := s.Labeler.AddRunnerLabels(github.Background(ctx), r.Installation, scope, rn.GitHubID, []string{label}); err != nil {
			_ = s.noteLimit(err)
			_ = s.Store.Save(ctx, r.Full(), st)
			return err
		}
		st.Runners = append(st.Runners, RunnerLabel{Repository: r.Full(), RunnerID: rn.ID, GitHubID: rn.GitHubID, Label: label,
			ScopeOwner: rn.ScopeOwner, ScopeRepo: rn.ScopeRepo, ScopeOrg: rn.ScopeOrg})
	}
	return s.Store.Save(ctx, r.Full(), st)
}

// Unassign takes a label off the runners that crsp no longer chose for it.
func (s *Service) Unassign(ctx context.Context, r Repo, label string, runnerIDs []string) error {
	st, err := s.Store.Load(ctx, r.Full())
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, id := range runnerIDs {
		drop[id] = true
	}
	var left []RunnerLabel
	var failure error
	for _, rl := range st.Runners {
		if rl.Label != label || !drop[rl.RunnerID] || failure != nil {
			left = append(left, rl)
			continue
		}
		if err := s.wait(ctx); err != nil {
			failure = err
			left = append(left, rl)
			continue
		}
		scope := github.RunnerScope{Owner: rl.ScopeOwner, Repo: rl.ScopeRepo, Organization: rl.ScopeOrg}
		if err := s.Labeler.RemoveRunnerLabel(github.Background(ctx), r.Installation, scope, rl.GitHubID, label); err != nil {
			failure = s.noteLimit(err)
			left = append(left, rl)
		}
	}
	st.Runners = left
	if err := s.Store.Save(ctx, r.Full(), st); err != nil {
		return err
	}
	return failure
}

// Label is the label crsp serves for a job now. ok is false for a job with no
// variable yet: it runs on the fallback and counts 1/1; it is never blocked.
func (s *Service) Label(ctx context.Context, repository, job string) (label string, ok bool, err error) {
	st, err := s.Store.Load(ctx, repository)
	if err != nil {
		return "", false, err
	}
	for _, j := range st.Jobs {
		if j.Key == job {
			return j.Label, true, nil
		}
	}
	return "", false, nil
}

// ServedLabels are the routing labels of one job an ephemeral runner gets at
// creation: the label now, and the retiring labels the job still asks for
// through runs created before the change.
func (s *Service) ServedLabels(ctx context.Context, repository, job string) ([]string, error) {
	st, err := s.Store.Load(ctx, repository)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range st.Labels {
		for _, j := range l.Jobs {
			if j == job {
				out = append(out, l.Label)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// CanOpenPR says whether crsp opens pull requests on this owner's repositories.
func (s *Service) CanOpenPR(owner string) bool { return s.canOpenPR(owner) }

func (s *Service) canOpenPR(owner string) bool {
	owners := s.PROwners
	if len(owners) == 0 {
		owners = []string{"luis-mc"}
	}
	for _, o := range owners {
		if strings.EqualFold(o, owner) {
			return true
		}
	}
	return false
}

// PatchFor builds the workflow patch of a repository, for the Doctor to show
// on a repository that is not the owner's.
func (s *Service) PatchFor(ctx context.Context, r Repo) (Patch, error) {
	files, err := s.Files.WorkflowFiles(ctx, r)
	if err != nil {
		return Patch{}, err
	}
	return MakePatch(files), nil
}

// PRResult says what OpenPR did.
type PRResult struct {
	Number  int
	URL     string
	Created bool
	Empty   bool // nothing to change
}

// Pull request text.
const (
	PRBranch  = "crsp/per-job-runs-on"
	PRTitle   = "Route each job to its own runner label"
	PRMessage = "Route each job to its own runner label"
)

func prBody(p Patch) string {
	var b strings.Builder
	b.WriteString("crsp gives each job its own routing label and repository variable, so it can place the job on a runner of the right size.\n\n")
	b.WriteString("Each routed `runs-on` now reads the variable of its job first, then `CRSP_RUNS_ON_<OS>`, then a hosted label, so a job with no variable yet runs as before:\n\n")
	for _, f := range p.Files {
		for _, c := range f.Changes {
			fmt.Fprintf(&b, "- `%s` job `%s` (line %d)\n", f.Path, c.Job, c.Line)
		}
	}
	if len(p.Skipped) > 0 {
		b.WriteString("\nNot changed (runs-on is not on one line): " + strings.Join(p.Skipped, ", ") + "\n")
	}
	return b.String()
}

// OpenPR opens ONE pull request on a repository of the owner that changes
// each routed runs-on to the per-job form. A second call opens no second pull
// request. Other owners get ErrNotOwned: the Doctor shows them PatchFor.
func (s *Service) OpenPR(ctx context.Context, r Repo) (PRResult, error) {
	if !s.canOpenPR(r.Owner) {
		return PRResult{}, ErrNotOwned
	}
	st, err := s.Store.Load(ctx, r.Full())
	if err != nil {
		return PRResult{}, err
	}
	if st.PR != nil {
		return PRResult{Number: st.PR.Number, URL: st.PR.URL}, nil
	}
	patch, err := s.PatchFor(ctx, r)
	if err != nil {
		return PRResult{}, err
	}
	if patch.Empty() {
		return PRResult{Empty: true}, nil
	}
	req := github.PullRequestRequest{Branch: PRBranch, Base: r.DefaultBranch, Title: PRTitle, Body: prBody(patch), Message: PRMessage}
	for _, f := range patch.Files {
		req.Files = append(req.Files, github.PullRequestFile{Path: f.Path, Content: f.New})
	}
	if err := s.wait(ctx); err != nil {
		return PRResult{}, err
	}
	pr, err := s.PRs.OpenPullRequest(ctx, r.Installation, r.Owner, r.Name, req)
	if err != nil {
		_ = s.noteLimit(err)
		return PRResult{}, err
	}
	st.PR = &PRState{Number: pr.Number, URL: pr.URL, OpenedAt: s.now()}
	if err := s.Store.Save(ctx, r.Full(), st); err != nil {
		return PRResult{}, err
	}
	return PRResult{Number: pr.Number, URL: pr.URL, Created: pr.Created}, nil
}
