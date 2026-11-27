package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// ErrNotFound is returned for an unknown plan or finding.
var ErrNotFound = errors.New("not found")

// ErrNotDraft is returned by Start for a plan that is not a draft (the API answers 409): a plan starts once.
var ErrNotDraft = errors.New("the plan was already started")

// PlanStore keeps plans. Implemented over internal/store (insights_plans.go).
type PlanStore interface {
	Insert(ctx context.Context, p Plan) error
	Get(ctx context.Context, id string) (Plan, error) // ErrNotFound when absent
	Update(ctx context.Context, p Plan) error
}

// FindingReader reads one finding; insights.FindingStore satisfies it.
type FindingReader interface {
	Get(ctx context.Context, id int64) (insights.StoredFinding, error)
}

// RunningJobs names the jobs running on a target (a machine id or repository) that a change which lowers
// slots waits for (UX-62).
type RunningJobs interface {
	RunningOn(ctx context.Context, target string) ([]string, error)
}

// Auditor writes the audit row of a change the service could not apply (observability R5.24). A change that
// is applied is audited by its Setter in the same transaction (R5.25).
type Auditor interface {
	Refused(ctx context.Context, by string, planID string, c Change, reason string) error
}

// Setters routes a change to the owning feature's setter by action kind.
type Setters map[insights.ActionKind]Setter

// fieldFor is the one field each action kind may change.
var fieldFor = map[insights.ActionKind]string{
	insights.ActionMachineSlots:      "job_slots",
	insights.ActionRepoParallelLimit: "max_parallel_jobs",
	insights.ActionLightLane:         "light_slots",
}

// Service is the Planner.
type Service struct {
	Plans    PlanStore
	Findings FindingReader
	Setters  Setters
	Running  RunningJobs // optional
	Audit    Auditor     // optional
	Now      func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var _ Planner = (*Service)(nil)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

// evidenceChanges is the part of a finding's evidence the plan reads: the changes its analyser proposes.
type evidenceChanges struct {
	Changes []struct {
		Target     string `json:"target"`
		TargetName string `json:"target_name"`
		Field      string `json:"field"`
		From       int    `json:"from"`
		To         int    `json:"to"`
		Words      string `json:"words"`
	} `json:"changes"`
}

// Plan makes a draft. It changes nothing in the setup (R6.6).
func (s *Service) Plan(ctx context.Context, findingID int64, by string) (Plan, error) {
	f, err := s.Findings.Get(ctx, findingID)
	if err != nil {
		return Plan{}, err
	}
	if f.Action == insights.ActionNone || !f.ResolvedAt.IsZero() {
		return Plan{}, ErrNoAction
	}
	field, ok := fieldFor[f.Action]
	if !ok {
		return Plan{}, ErrNoAction
	}
	var ev evidenceChanges
	if err := json.Unmarshal(f.Evidence, &ev); err != nil || len(ev.Changes) == 0 {
		return Plan{}, ErrNoAction
	}
	p := Plan{
		ID: newID(), FindingID: f.ID, Action: f.Action, State: Draft, CreatedBy: by, CreatedAt: s.now(),
		Estimate: fmt.Sprintf("about %d min a week (estimate)", f.MinutesSavedWeek),
	}
	for _, c := range ev.Changes {
		if c.Field != field || c.Target == "" || c.To < 0 || c.To == c.From {
			return Plan{}, fmt.Errorf("finding %d: change for %q is not valid", f.ID, c.Target)
		}
		ch := Change{Target: c.Target, TargetName: c.TargetName, Field: c.Field, From: c.From, To: c.To, Words: c.Words}
		if ch.TargetName == "" {
			ch.TargetName = ch.Target
		}
		if c.To < c.From && s.Running != nil {
			jobs, err := s.Running.RunningOn(ctx, c.Target)
			if err != nil {
				return Plan{}, fmt.Errorf("running jobs of %s: %w", c.Target, err)
			}
			ch.JobsWaitedFor = jobs
		}
		p.Changes = append(p.Changes, ch)
		p.Results = append(p.Results, ChangeResult{Target: ch.Target, Field: ch.Field, State: "pending"})
	}
	if err := s.Plans.Insert(ctx, p); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Get returns a plan with its progress.
func (s *Service) Get(ctx context.Context, id string) (Plan, error) { return s.Plans.Get(ctx, id) }

// Start runs a draft plan once. A failed change does not undo the done ones; Done says what changed and
// what did not. A cancelled ctx stops the plan and skips the rest.
func (s *Service) Start(ctx context.Context, id string, by string) (Plan, error) {
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	p, err := s.Plans.Get(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.State != Draft {
		return p, ErrNotDraft
	}
	setter, ok := s.Setters[p.Action]
	if !ok {
		return p, ErrNoAction
	}
	p.State, p.StartedAt = Running, s.now()
	if err := s.Plans.Update(ctx, p); err != nil {
		return p, err
	}
	failed, stopped := false, false
	for i, c := range p.Changes {
		if stopped || ctx.Err() != nil {
			stopped = true
			p.Results[i].State = "skipped"
			continue
		}
		if err := setter.Apply(ctx, c, by); err != nil {
			failed = true
			p.Results[i].State, p.Results[i].Error = "failed", err.Error()
			if s.Audit != nil {
				_ = s.Audit.Refused(context.WithoutCancel(ctx), by, p.ID, c, err.Error())
			}
			continue
		}
		p.Results[i].State = "done"
	}
	switch {
	case stopped:
		p.State = Stopped
	case failed:
		p.State = Failed
	default:
		p.State = Done
	}
	p.FinishedAt = s.now()
	if err := s.Plans.Update(context.WithoutCancel(ctx), p); err != nil {
		return p, err
	}
	return p, nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "plan_" + hex.EncodeToString(b[:])
}
