// Package trial is the safe rollout of routing (github-integration R5.66): a
// repository can be watched, shadowed or partly switched before the pool
// takes all of its jobs. Modes:
//
//	observe  record which jobs crsp would take and how long they wait; nothing
//	         is written to GitHub
//	shadow   a copy of selected jobs runs on crsp through a workflow_dispatch of
//	         a shadow workflow; results are recorded; the real job stays hosted
//	canary   a percentage of the jobs, or named jobs, read crsp labels from
//	         CRSP_RUNS_ON_CANARY_<JOB> variables; the rest stay hosted
//	live     the repository is fully on the pool: the canary variables are put
//	         back and the owner's own CRSP_RUNS_ON variables rule every job
//	off      no trial; a canary is undone and the earlier variables come back
//
// A repository never moves two steps forward at once (off, observe, shadow,
// canary, live); going back to any earlier step is always allowed (operations R5.42).
//
// The package keeps its state in a small key-value store and reaches GitHub
// only through the Variables and Dispatcher interfaces; the CRSP_RUNS_ON variable
// writing stays in package repos.
package trial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode is a trial mode.
type Mode string

// The modes. Off is the same as no trial.
const (
	Off     Mode = "off"
	Observe Mode = "observe"
	Shadow  Mode = "shadow"
	Canary  Mode = "canary"
	Live    Mode = "live"
)

// Order is the position of a mode on the way to live.
func (m Mode) Order() int {
	switch m {
	case Observe:
		return 1
	case Shadow:
		return 2
	case Canary:
		return 3
	case Live:
		return 4
	}
	return 0
}

// ParseMode reads a mode word.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case Off, Observe, Shadow, Canary, Live:
		return m, nil
	}
	return "", fmt.Errorf("unknown trial mode %q: use observe, shadow, canary, live or off", s)
}

// Errors.
var (
	ErrPublic     = errors.New("public repositories are refused: crsp serves private repositories only")
	ErrNoJobs     = errors.New("no jobs known for canary: run observe first, or name the jobs")
	ErrBadPercent = errors.New("canary percent must be from 1 to 100")
	ErrTwoSteps   = errors.New("a repository moves one step at a time: off, observe, shadow, canary, live")
)

// Repo is what the trial needs to know about a repository.
type Repo struct {
	FullName string
	Public   bool
}

// Variables is the variable access; package repos provides it, so the writing
// of CRSP_RUNS_ON variables stays in the owner code.
type Variables interface {
	Repository(ctx context.Context, full string) (Repo, error)
	// Variables lists the CRSP_RUNS_ON family variables now set on the repository.
	Variables(ctx context.Context, full string) (map[string]string, error)
	Write(ctx context.Context, full, name, value string) error
	Remove(ctx context.Context, full, name string) error
	// Desired is what the controller would write for the repository (CRSP_RUNS_ON_<OS>).
	Desired(ctx context.Context, full string) (map[string]string, error)
}

// Dispatcher starts the shadow workflow (workflow_dispatch).
type Dispatcher interface {
	Dispatch(ctx context.Context, full, workflow, ref string, inputs map[string]string) error
}

// KV is a small key-value store; the settings table backs it.
type KV interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// State is the trial of one repository.
type State struct {
	Repo  string    `json:"repo"`
	Mode  Mode      `json:"mode"`
	Since time.Time `json:"since"`
	By    string    `json:"by,omitempty"`
	// Canary settings: a percentage, or named jobs, or both.
	Percent int      `json:"percent,omitempty"`
	Jobs    []string `json:"jobs,omitempty"`
	// Shadow settings: the workflow file to dispatch and the jobs to copy
	// (empty means every job that crsp would take).
	ShadowWorkflow string   `json:"shadow_workflow,omitempty"`
	ShadowJobs     []string `json:"shadow_jobs,omitempty"`
	// Seen is every job name observed, with its operating system.
	Seen map[string]string `json:"seen,omitempty"`
	// Before is the CRSP_RUNS_ON family variables as they were before the canary;
	// Written is the names the canary wrote.
	Before  map[string]string `json:"before,omitempty"`
	Written []string          `json:"written,omitempty"`
	// Dispatched keeps the run/job pairs already shadowed, newest last.
	Dispatched []string `json:"dispatched,omitempty"`
}

// Options of Set.
type Options struct {
	By             string
	Percent        int
	Jobs           []string
	ShadowWorkflow string
	ShadowJobs     []string
}

// DefaultShadowWorkflow is the workflow file a shadow dispatches.
const DefaultShadowWorkflow = "crsp-shadow.yml"

const maxSeen, maxDispatched, maxOutcomes = 500, 200, 2000

// Manager runs the trial modes.
type Manager struct {
	vars Variables
	disp Dispatcher
	kv   KV
	now  func() time.Time
	// Serves says whether crsp has a machine for an OS; nil means every OS.
	serves func(ctx context.Context, os string) bool

	mu sync.Mutex
}

// Config builds a Manager. Dispatcher may be nil: shadow then cannot start
// copies and says so.
type Config struct {
	Vars       Variables
	Dispatcher Dispatcher
	KV         KV
	Now        func() time.Time
	Serves     func(ctx context.Context, os string) bool
}

// New returns a Manager.
func New(c Config) *Manager {
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Manager{vars: c.Vars, disp: c.Dispatcher, kv: c.KV, now: c.Now, serves: c.Serves}
}

func stateKey(repo string) string   { return "trial_state:" + strings.ToLower(repo) }
func outcomeKey(repo string) string { return "trial_outcomes:" + strings.ToLower(repo) }

// Get is the trial state of a repository; Mode is Off when none was set.
func (m *Manager) Get(ctx context.Context, repo string) (State, error) {
	raw, ok, err := m.kv.Get(ctx, stateKey(repo))
	if err != nil {
		return State{}, err
	}
	if !ok || raw == "" {
		return State{Repo: repo, Mode: Off}, nil
	}
	var s State
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return State{}, fmt.Errorf("trial state of %s: %w", repo, err)
	}
	if s.Mode == "" {
		s.Mode = Off
	}
	return s, nil
}

func (m *Manager) put(ctx context.Context, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return m.kv.Set(ctx, stateKey(s.Repo), string(b))
}

// Set moves a repository to a mode. Switching to Off undoes a canary and puts
// the earlier variables back exactly. Public repositories are refused for any
// mode but Off.
func (m *Manager) Set(ctx context.Context, repo string, mode Mode, o Options) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.vars.Repository(ctx, repo)
	if err != nil {
		return State{}, err
	}
	repo = r.FullName
	if r.Public && mode != Off {
		return State{}, fmt.Errorf("%s: %w", repo, ErrPublic)
	}
	cur, err := m.Get(ctx, repo)
	if err != nil {
		return State{}, err
	}
	if mode == Canary && (o.Percent < 0 || o.Percent > 100 || (o.Percent == 0 && len(o.Jobs) == 0)) {
		return State{}, ErrBadPercent
	}
	if mode.Order() > cur.Mode.Order()+1 {
		return State{}, fmt.Errorf("%s is %s, so it cannot go to %s: %w", repo, cur.Mode, mode, ErrTwoSteps)
	}
	// Leaving or changing a canary first puts the variables back.
	if cur.Mode == Canary {
		if err := m.restore(ctx, &cur); err != nil {
			return State{}, err
		}
	}
	next := State{Repo: repo, Mode: mode, Since: m.now().UTC(), By: o.By, Seen: cur.Seen, Dispatched: cur.Dispatched}
	switch mode {
	case Off:
		next.Seen, next.Dispatched = nil, nil
		// The outcomes stay: the report is read after a trial too.
	case Observe, Live:
	case Shadow:
		next.ShadowWorkflow = o.ShadowWorkflow
		if next.ShadowWorkflow == "" {
			next.ShadowWorkflow = DefaultShadowWorkflow
		}
		next.ShadowJobs = cleanNames(o.ShadowJobs)
	case Canary:
		next.Percent, next.Jobs = o.Percent, cleanNames(o.Jobs)
		if err := m.applyCanary(ctx, &next); err != nil {
			// Put back whatever was written before the failure.
			if rerr := m.restore(ctx, &next); rerr != nil {
				err = errors.Join(err, rerr)
			}
			return State{}, err
		}
	}
	if err := m.put(ctx, next); err != nil {
		return State{}, err
	}
	return next, nil
}

func cleanNames(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
