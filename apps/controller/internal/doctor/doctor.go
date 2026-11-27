package doctor

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Severity is the result of a check, best to worst: ok, info, skip, warn, fail (R5.3).
type Severity string

// Severities.
const (
	OK   Severity = "ok"
	Info Severity = "info"
	Skip Severity = "skip"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

// Rank orders severities: a higher rank is worse. An unknown value ranks as ok.
func (s Severity) Rank() int {
	switch s {
	case Info:
		return 1
	case Skip:
		return 2
	case Warn:
		return 3
	case Fail:
		return 4
	}
	return 0
}

// Valid says whether s is one of the five severities.
func (s Severity) Valid() bool {
	switch s {
	case OK, Info, Skip, Warn, Fail:
		return true
	}
	return false
}

// Worst returns the worst of the severities, ok for none.
func Worst(sev ...Severity) Severity {
	w := OK
	for _, s := range sev {
		if s.Rank() > w.Rank() {
			w = s
		}
	}
	return w
}

// Group is the filter group of a check (listDoctorFindings `group`).
type Group string

// Groups.
const (
	GroupMachine    Group = "machine"
	GroupBackend    Group = "backend"
	GroupRunner     Group = "runner"
	GroupGitHub     Group = "github"
	GroupRepository Group = "repo"
	GroupWorkflow   Group = "workflow"
	GroupController Group = "controller"
)

// Subject is what a finding is about. Kinds in use: machine, runner,
// repository, installation, controller.
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Fix says what to do (R5.4).
type Fix struct {
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
	Link    string `json:"link,omitempty"`
}

// Item is one thing a finding lists, such as a runner leftover of a machine (R5.48).
type Item struct {
	// Kind is service, folder, account or download.
	Kind string `json:"kind"`
	// Name is the service or account name, or the full path.
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Account   string `json:"account,omitempty"`
	Why       string `json:"why"`
}

// Action is the one button a finding offers. The doctor only names it; the
// console runs it on a screen with its own confirm (UX-52, R5.48).
type Action struct {
	// ID is remove_runner_leftovers, remove_github_runner or remove_runner_record.
	ID    string `json:"id"`
	Label string `json:"label"`
	// Args are what the call of the button needs back, for the two registration
	// actions (R5.50). The server checks them again before it removes anything.
	Args *ActionArgs `json:"args,omitempty"`
}

// ActionArgs names the one registration an action removes: a GitHub runner no
// local record owns (remove_github_runner), or a local record GitHub does not
// list (remove_runner_record, RunnerID).
type ActionArgs struct {
	RunnerID       string `json:"runner_id,omitempty"`
	GitHubRunnerID int64  `json:"github_runner_id,omitempty"`
	Name           string `json:"name,omitempty"`
	InstallationID int64  `json:"installation_id,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Organization   string `json:"organization,omitempty"`
}

// Action IDs.
const (
	ActionRemoveRunnerLeftovers = "remove_runner_leftovers"
	ActionRemoveGitHubRunner    = "remove_github_runner"
	ActionRemoveRunnerRecord    = "remove_runner_record"
)

// Finding is one result for one check and subject (R5.2).
type Finding struct {
	Check      string   `json:"check"`
	Subject    Subject  `json:"subject"`
	Severity   Severity `json:"severity"`
	Title      string   `json:"title"`
	Detail     string   `json:"detail,omitempty"`
	Fix        Fix      `json:"fix"`
	NeedsAdmin bool     `json:"needs_admin"`
	// Items and Action are set by a finding that lists things and offers a button (R5.48).
	Items     []Item    `json:"items,omitempty"`
	Action    *Action   `json:"action,omitempty"`
	Milestone int       `json:"milestone"`
	CheckedAt time.Time `json:"checked_at"`
}

// Scope limits a run (R5.1). Empty means everything. Checks are ID prefixes
// ("runner", "runner.reg"). Machines are IDs or names.
type Scope struct {
	Machines     []string `json:"machines,omitempty"`
	Repositories []int64  `json:"repositories,omitempty"`
	Checks       []string `json:"checks,omitempty"`
}

// Key is the canonical form used to join runs of the same scope (R5.8).
func (s Scope) Key() string {
	c := Scope{Machines: sortedCopy(s.Machines), Checks: sortedCopy(s.Checks)}
	c.Repositories = append([]int64(nil), s.Repositories...)
	sort.Slice(c.Repositories, func(i, j int) bool { return c.Repositories[i] < c.Repositories[j] })
	b, _ := json.Marshal(c)
	return string(b)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// IsAll is true when nothing narrows the run.
func (s Scope) IsAll() bool {
	return len(s.Machines) == 0 && len(s.Repositories) == 0 && len(s.Checks) == 0
}

// MatchCheck says whether a check ID is in scope.
func (s Scope) MatchCheck(id string) bool {
	if len(s.Checks) == 0 {
		return true
	}
	for _, p := range s.Checks {
		if id == p || strings.HasPrefix(id, p+".") {
			return true
		}
	}
	return false
}

// MatchMachine says whether a machine (by ID or name) is in scope.
func (s Scope) MatchMachine(id, name string) bool {
	if len(s.Machines) == 0 {
		return true
	}
	for _, m := range s.Machines {
		if m == id || strings.EqualFold(m, name) {
			return true
		}
	}
	return false
}

// MatchRepository says whether a repository is in scope.
func (s Scope) MatchRepository(id int64) bool {
	if len(s.Repositories) == 0 {
		return true
	}
	for _, r := range s.Repositories {
		if r == id {
			return true
		}
	}
	return false
}

// Check is one health check (spec 7.1).
type Check interface {
	// ID is the stable ID, such as "runner.registered". Findings carry it.
	ID() string
	Group() Group
	// Milestone is the milestone that added the check.
	Milestone() int
	// NeedsAdmin is true when the check can only run with administrator
	// rights on the machine; it then answers skip with needs_admin when it lacks them (R5.13).
	NeedsAdmin() bool
	Run(ctx context.Context, env Env) []Finding
}

// Func is the function form of a check body.
type Func func(ctx context.Context, env Env) []Finding

type funcCheck struct {
	id        string
	group     Group
	milestone int
	admin     bool
	fn        Func
}

// NewCheck makes a Check from a function.
func NewCheck(id string, group Group, milestone int, needsAdmin bool, fn Func) Check {
	return &funcCheck{id: id, group: group, milestone: milestone, admin: needsAdmin, fn: fn}
}

func (c *funcCheck) ID() string                                 { return c.id }
func (c *funcCheck) Group() Group                               { return c.group }
func (c *funcCheck) Milestone() int                             { return c.milestone }
func (c *funcCheck) NeedsAdmin() bool                           { return c.admin }
func (c *funcCheck) Run(ctx context.Context, env Env) []Finding { return c.fn(ctx, env) }

// Registry holds the checks. Areas register theirs at start-up.
type Registry struct {
	checks []Check
	byID   map[string]int
}

// NewRegistry makes an empty registry.
func NewRegistry() *Registry { return &Registry{byID: map[string]int{}} }

// Register adds a check. A second check with the same ID replaces the first.
func (r *Registry) Register(c Check) {
	if i, ok := r.byID[c.ID()]; ok {
		r.checks[i] = c
		return
	}
	r.byID[c.ID()] = len(r.checks)
	r.checks = append(r.checks, c)
}

// All returns the checks in registration order.
func (r *Registry) All() []Check { return append([]Check(nil), r.checks...) }

// Selected returns the checks the scope asks for.
func (r *Registry) Selected(s Scope) []Check {
	var out []Check
	for _, c := range r.checks {
		if s.MatchCheck(c.ID()) {
			out = append(out, c)
		}
	}
	return out
}
