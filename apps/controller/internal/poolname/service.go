package poolname

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// Job and step states.
const (
	JobPlanned  = "planned"
	JobApplying = "applying"
	JobDone     = "done"
	JobStopped  = "stopped"
	JobFailed   = "failed"

	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// Step kinds, in the order a rename runs them. The order is the promise: the
// new label is on every runner before any variable names it, and the old label
// leaves a runner only after every variable names the new one.
const (
	KindAddLabel    = "add_label"
	KindStoreName   = "store_name"
	KindVariables   = "write_variables"
	KindRemoveLabel = "remove_label"
	KindScaleSet    = "scale_set"
	KindFinish      = "finish"
)

// ErrNotApplying is returned by Stop for a job that is not applying.
var ErrNotApplying = fmt.Errorf("the rename is not running: %w", auth.ErrConflict)

// ErrNotPlanned is returned by Apply for a job that is applying or done.
var ErrNotPlanned = fmt.Errorf("the rename is running or finished already: %w", auth.ErrConflict)

// Step is one change of a rename.
type Step struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Target string `json:"target,omitempty"` // a runner id or a repository id
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

// Counts is what a rename will change, for the Review step.
type Counts struct {
	Runners      int  `json:"runners"`      // runner records that carry the old label
	Registered   int  `json:"registered"`   // of those, the ones GitHub holds a label for
	Ephemeral    int  `json:"ephemeral"`    // runners made for one job; they get the new label when made
	Repositories int  `json:"repositories"` // repositories with the pool on
	ScaleSet     bool `json:"scale_set"`    // scale-set demand is on
}

// Job is one rename.
type Job struct {
	ID        string    `json:"id"`
	Old       string    `json:"old"`
	New       string    `json:"new"`
	State     string    `json:"state"`
	Steps     []Step    `json:"steps"`
	Counts    Counts    `json:"counts"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunnerRef is a runner record that carries a label.
type RunnerRef struct {
	ID         string
	Name       string
	Registered bool // GitHub holds the runner's labels (a persistent registration)
	Ephemeral  bool
}

// RepoRef is a repository with the pool on.
type RepoRef struct {
	ID       int64
	FullName string
}

// Runners is what the rename needs from the runners service.
type Runners interface {
	// WithLabel lists the runner records that carry the label.
	WithLabel(ctx context.Context, label string) ([]RunnerRef, error)
	// AddLabel puts label on the runner at GitHub and in its record. Doing it twice is fine.
	AddLabel(ctx context.Context, id, label, actor string) error
	// RemoveLabel takes label off the runner at GitHub and from its record. A label that is gone is fine.
	RemoveLabel(ctx context.Context, id, label, actor string) error
}

// Repos is what the rename needs from the repositories service.
type Repos interface {
	// PoolOn lists the repositories that have the pool on.
	PoolOn(ctx context.Context) ([]RepoRef, error)
	// SyncVariables makes the repository's CRSP_RUNS_ON_* variables what the
	// product writes for the name in force (the writer of the restore and repair).
	SyncVariables(ctx context.Context, repoID int64, actor string) error
}

// ScaleSet is the scale-set demand source, when it is on.
type ScaleSet interface {
	On() bool
	// Recreate starts the scale set again under the name in force.
	Recreate(ctx context.Context) error
}

// Audit writes one audit entry.
type Audit interface {
	Audit(ctx context.Context, actor, action, subject, detail string)
}

// Config wires a Service.
type Config struct {
	Name     *Name
	KV       KV
	Runners  Runners
	Repos    Repos
	ScaleSet ScaleSet // nil when scale-set demand is off
	Audit    Audit
	Now      func() time.Time
	NewID    func() string
	// Background runs an apply off the request; tests run it inline.
	Background func(func())
}

// Service stores the name and runs a rename.
type Service struct {
	cfg Config

	mu    sync.Mutex // guards stops and live
	stops map[string]bool
	live  map[string]bool
}

// New returns a Service. It reads an unfinished rename from before a restart,
// so the old name keeps meaning this pool until that rename ends.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = func() string { return fmt.Sprintf("pr_%d", time.Now().UnixNano()) }
	}
	if cfg.Background == nil {
		cfg.Background = func(f func()) { go f() }
	}
	s := &Service{cfg: cfg, stops: map[string]bool{}, live: map[string]bool{}}
	if j, ok, _ := s.load(context.Background()); ok && unfinished(j) && storeDone(j) && cfg.Name != nil && cfg.Name.Source() != SourceEnv {
		cfg.Name.set(j.New, j.Old)
	}
	return s
}

func unfinished(j Job) bool { return j.State != JobDone }

func storeDone(j Job) bool {
	for _, st := range j.Steps {
		if st.Kind == KindStoreName {
			return st.State == StepDone
		}
	}
	return false
}

// Info is the name in force and where it came from.
type Info struct {
	Name        string
	Source      string
	EnvOverride bool
	Pending     *Job // an unfinished rename, to resume
}

// Info reports the name and an unfinished rename.
func (s *Service) Info(ctx context.Context) Info {
	in := Info{Name: s.cfg.Name.Get(), Source: s.cfg.Name.Source(), EnvOverride: s.cfg.Name.Source() == SourceEnv}
	if j, ok, _ := s.load(ctx); ok && unfinished(j) {
		in.Pending = &j
	}
	return in
}

// SetFirst stores the name at the first setup, when nothing carries a label
// yet. A pool that has runners or repositories is renamed with a Plan.
func (s *Service) SetFirst(ctx context.Context, name, actor string) (Info, error) {
	if err := Validate(name); err != nil {
		return Info{}, err
	}
	if s.cfg.Name.Source() == SourceEnv {
		return Info{}, fmt.Errorf("the pool name is set by %s on the service, which wins over the console: %w", EnvName, auth.ErrConflict)
	}
	cur := s.cfg.Name.Get()
	if name == cur {
		if s.cfg.Name.Source() == SourceDefault {
			if err := s.putName(ctx, name, actor); err != nil {
				return Info{}, err
			}
			s.cfg.Name.set(name, "")
		}
		return s.Info(ctx), nil
	}
	rs, err := s.cfg.Runners.WithLabel(ctx, cur)
	if err != nil {
		return Info{}, err
	}
	ps, err := s.cfg.Repos.PoolOn(ctx)
	if err != nil {
		return Info{}, err
	}
	if len(rs) > 0 || len(ps) > 0 {
		return Info{}, fmt.Errorf("the pool already has %d runners and %d repositories that use %s; rename it from Setup so each gets the new label: %w", len(rs), len(ps), cur, auth.ErrConflict)
	}
	if err := s.putName(ctx, name, actor); err != nil {
		return Info{}, err
	}
	s.cfg.Name.set(name, "")
	s.audit(ctx, actor, "pool.named", name, "the pool was named at the first setup (it was "+cur+")")
	return s.Info(ctx), nil
}

func (s *Service) putName(ctx context.Context, name, actor string) error {
	b, _ := json.Marshal(name)
	return s.cfg.KV.PutRawSetting(ctx, nameKey, string(b), actor)
}

func (s *Service) audit(ctx context.Context, actor, action, subject, detail string) {
	if s.cfg.Audit != nil {
		s.cfg.Audit.Audit(ctx, actor, action, subject, detail)
	}
}

func (s *Service) load(ctx context.Context) (Job, bool, error) {
	if s.cfg.KV == nil {
		return Job{}, false, nil
	}
	raw, ok, err := s.cfg.KV.RawSetting(ctx, jobKey)
	if err != nil || !ok {
		return Job{}, false, err
	}
	var j Job
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

func (s *Service) save(ctx context.Context, j Job, actor string) error {
	j.UpdatedAt = s.cfg.Now()
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return s.cfg.KV.PutRawSetting(ctx, jobKey, string(b), actor)
}

// Get reads the rename with the given id (the latest one is kept).
func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	j, ok, err := s.load(ctx)
	if err != nil {
		return Job{}, err
	}
	if !ok || j.ID != id {
		return Job{}, auth.ErrNotFound
	}
	return j, nil
}

// Plan works out what renaming the pool to name changes, changing nothing. A
// rename to the same name that stopped or failed is returned again, with its
// steps brought up to date, so Run again resumes it.
func (s *Service) Plan(ctx context.Context, name, actor string) (Job, error) {
	if err := Validate(name); err != nil {
		return Job{}, err
	}
	if s.cfg.Name.Source() == SourceEnv {
		return Job{}, fmt.Errorf("the pool name is set by %s on the service, which wins over the console; remove that value first: %w", EnvName, auth.ErrConflict)
	}
	prev, had, err := s.load(ctx)
	if err != nil {
		return Job{}, err
	}
	if had && prev.State == JobApplying && s.isLive(prev.ID) {
		return Job{}, fmt.Errorf("a rename to %s is running: %w", prev.New, auth.ErrConflict)
	}
	resume := had && unfinished(prev) && prev.New == name
	old := s.cfg.Name.Get()
	if resume {
		old = prev.Old
	}
	if name == old && !resume {
		return Job{}, fmt.Errorf("the pool is already called %s: %w", name, auth.ErrConflict)
	}
	steps, counts, err := s.steps(ctx, old, name)
	if err != nil {
		return Job{}, err
	}
	if resume {
		done := map[string]bool{}
		for _, st := range prev.Steps {
			if st.State == StepDone {
				done[st.ID] = true
			}
		}
		for i := range steps {
			if done[steps[i].ID] {
				steps[i].State = StepDone
			}
		}
	}
	j := Job{ID: s.cfg.NewID(), Old: old, New: name, State: JobPlanned, Steps: steps, Counts: counts, CreatedBy: actor, CreatedAt: s.cfg.Now()}
	if resume {
		j.ID, j.CreatedBy, j.CreatedAt = prev.ID, prev.CreatedBy, prev.CreatedAt
	}
	if err := s.save(ctx, j, actor); err != nil {
		return Job{}, err
	}
	return j, nil
}

// steps lists the work in the promised order.
func (s *Service) steps(ctx context.Context, old, name string) ([]Step, Counts, error) {
	rs, err := s.cfg.Runners.WithLabel(ctx, old)
	if err != nil {
		return nil, Counts{}, err
	}
	ps, err := s.cfg.Repos.PoolOn(ctx)
	if err != nil {
		return nil, Counts{}, err
	}
	var c Counts
	var out []Step
	var held []RunnerRef
	for _, r := range rs {
		c.Runners++
		if r.Ephemeral {
			c.Ephemeral++
		}
		if r.Registered {
			c.Registered++
		}
		held = append(held, r)
	}
	c.Repositories = len(ps)
	c.ScaleSet = s.cfg.ScaleSet != nil && s.cfg.ScaleSet.On()
	for _, r := range held {
		out = append(out, Step{ID: KindAddLabel + ":" + r.ID, Kind: KindAddLabel, Target: r.ID, State: StepPending,
			Text: "Add the label " + name + " to runner " + r.Name + " (it keeps " + old + " for now)"})
	}
	out = append(out, Step{ID: KindStoreName, Kind: KindStoreName, State: StepPending,
		Text: "Store the new name " + name + "; new runners and the console use it from here"})
	for _, p := range ps {
		out = append(out, Step{ID: fmt.Sprintf("%s:%d", KindVariables, p.ID), Kind: KindVariables, Target: fmt.Sprint(p.ID), State: StepPending,
			Text: "Write the CRSP_RUNS_ON variables of " + p.FullName + " with " + name})
	}
	for _, r := range held {
		out = append(out, Step{ID: KindRemoveLabel + ":" + r.ID, Kind: KindRemoveLabel, Target: r.ID, State: StepPending,
			Text: "Remove the label " + old + " from runner " + r.Name})
	}
	if c.ScaleSet {
		out = append(out, Step{ID: KindScaleSet, Kind: KindScaleSet, State: StepPending,
			Text: "Start the scale set again under the name " + name})
	}
	out = append(out, Step{ID: KindFinish, Kind: KindFinish, State: StepPending,
		Text: "Finish: a job queued with " + old + " no longer finds a runner"})
	return out, c, nil
}

func (s *Service) isLive(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[id]
}

// Apply runs a planned, stopped or failed rename: steps already done are not
// repeated, so it resumes where it ended. It answers with the job applying and
// runs the steps in the background; read Get for the outcome.
func (s *Service) Apply(ctx context.Context, id, actor string) (Job, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if s.isLive(id) || j.State == JobDone {
		return j, ErrNotPlanned
	}
	for i := range j.Steps {
		if j.Steps[i].State != StepDone {
			j.Steps[i].State, j.Steps[i].Error = StepPending, ""
		}
	}
	j.State = JobApplying
	if err := s.save(ctx, j, actor); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	s.live[id] = true
	delete(s.stops, id)
	s.mu.Unlock()
	s.audit(ctx, actor, "pool.rename_started", j.New, "from "+j.Old)
	s.cfg.Background(func() { s.run(context.WithoutCancel(ctx), j, actor) })
	if got, err := s.Get(ctx, id); err == nil {
		return got, nil
	}
	return j, nil
}

// Stop asks a running rename to stop: the step that runs finishes and no later
// step starts. What was done stays done and Run again resumes.
func (s *Service) Stop(ctx context.Context, id, actor string) (Job, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	live := s.live[id]
	if live {
		s.stops[id] = true
	}
	s.mu.Unlock()
	if !live {
		return j, ErrNotApplying
	}
	s.audit(ctx, actor, "pool.rename_stop_asked", j.New, "")
	return j, nil
}

func (s *Service) stopAsked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops[id]
}

func (s *Service) end(id string) {
	s.mu.Lock()
	delete(s.live, id)
	delete(s.stops, id)
	s.mu.Unlock()
}

func (s *Service) run(ctx context.Context, j Job, actor string) {
	defer s.end(j.ID)
	for i := range j.Steps {
		st := &j.Steps[i]
		if st.State == StepDone {
			continue
		}
		if s.stopAsked(j.ID) {
			for k := i; k < len(j.Steps); k++ {
				if j.Steps[k].State != StepDone {
					j.Steps[k].State = StepSkipped
				}
			}
			j.State = JobStopped
			_ = s.save(ctx, j, actor)
			s.audit(ctx, actor, "pool.rename_stopped", j.New, leftText(j))
			return
		}
		st.State = StepRunning
		_ = s.save(ctx, j, actor)
		if err := s.runStep(ctx, j, *st, actor); err != nil {
			st.State, st.Error = StepFailed, reason(err)
			for k := i + 1; k < len(j.Steps); k++ {
				if j.Steps[k].State != StepDone {
					j.Steps[k].State = StepSkipped
				}
			}
			j.State = JobFailed
			_ = s.save(ctx, j, actor)
			s.audit(ctx, actor, "pool.rename_failed", j.New, st.Text+": "+st.Error)
			return
		}
		st.State = StepDone
		_ = s.save(ctx, j, actor)
	}
	j.State = JobDone
	_ = s.save(ctx, j, actor)
	s.audit(ctx, actor, "pool.renamed", j.New, "from "+j.Old)
}

func leftText(j Job) string {
	done, left := 0, 0
	for _, st := range j.Steps {
		if st.State == StepDone {
			done++
		} else {
			left++
		}
	}
	return fmt.Sprintf("%d steps done, %d left", done, left)
}

func reason(err error) string {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func (s *Service) runStep(ctx context.Context, j Job, st Step, actor string) error {
	switch st.Kind {
	case KindAddLabel:
		if err := s.cfg.Runners.AddLabel(ctx, st.Target, j.New, actor); err != nil {
			return err
		}
		s.audit(ctx, actor, "pool.label_added", st.Target, j.New)
	case KindStoreName:
		if err := s.putName(ctx, j.New, actor); err != nil {
			return err
		}
		s.cfg.Name.set(j.New, j.Old) // the old name still means this pool until the end
		s.audit(ctx, actor, "pool.name_stored", j.New, "from "+j.Old)
	case KindVariables:
		var id int64
		if _, err := fmt.Sscan(st.Target, &id); err != nil {
			return errors.New("the step names no repository")
		}
		if err := s.cfg.Repos.SyncVariables(ctx, id, actor); err != nil {
			return err
		}
		s.audit(ctx, actor, "pool.variables_written", st.Target, j.New)
	case KindRemoveLabel:
		if err := s.cfg.Runners.RemoveLabel(ctx, st.Target, j.Old, actor); err != nil {
			return err
		}
		s.audit(ctx, actor, "pool.label_removed", st.Target, j.Old)
	case KindScaleSet:
		if s.cfg.ScaleSet != nil {
			if err := s.cfg.ScaleSet.Recreate(ctx); err != nil {
				return err
			}
		}
		s.audit(ctx, actor, "pool.scale_set_renamed", j.New, "")
	case KindFinish:
		s.cfg.Name.set(j.New, "")
	default:
		return fmt.Errorf("unknown step %q", st.Kind)
	}
	return nil
}
