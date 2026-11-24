package repos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Change asks for the pool to be turned on or off for one repository.
type Change struct {
	RepositoryID int64
	Pool         bool
}

// ErrNothingToChange: every repository already is as asked.
var ErrNothingToChange = errors.New("nothing to change: every repository is already as asked")

// ErrPlanStale: the repositories changed since the plan was made.
var ErrPlanStale = errors.New("the plan is out of date; make a new one")

// ErrPlanNotPlanned: a plan applies once.
var ErrPlanNotPlanned = errors.New("this plan was already applied")

// ErrPlanNotApplying: only a plan that is applying can be stopped.
var ErrPlanNotApplying = errors.New("this plan is not applying, so there is nothing to stop")

// PlanChanges makes a plan that turns the pool on or off for repositories.
// Nothing changes until ApplyPlan. A plan that turns the pool off names each
// variable it would delete and the workflows that still read it, and each
// delete needs its own yes (R5.20, R5.29).
func (s *Service) PlanChanges(ctx context.Context, p auth.Principal, changes []Change) (Plan, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return Plan{}, err
	}
	if len(changes) == 0 {
		return Plan{}, auth.Invalid("at least one change is needed")
	}
	var steps []Step
	seen := map[int64]bool{}
	for _, c := range changes {
		if seen[c.RepositoryID] {
			return Plan{}, auth.Invalid("repository %d is listed twice", c.RepositoryID)
		}
		seen[c.RepositoryID] = true
		r, err := s.cfg.Store.GetRepository(ctx, c.RepositoryID)
		if err != nil {
			return Plan{}, err
		}
		if c.Pool == r.PoolOn {
			continue
		}
		var more []Step
		if c.Pool {
			if err := s.RequirePublicAllowed(ctx, r); err != nil {
				return Plan{}, err
			}
			more, err = s.planOn(ctx, r)
		} else {
			more, err = s.planOff(ctx, r)
		}
		if err != nil {
			return Plan{}, err
		}
		steps = append(steps, more...)
	}
	if len(steps) == 0 {
		return Plan{}, ErrNothingToChange
	}
	for i := range steps {
		steps[i].ID = fmt.Sprintf("s%d", i+1)
		steps[i].State = StepPlanned
	}
	hash, err := s.stateHash(ctx, changes)
	if err != nil {
		return Plan{}, err
	}
	now := s.cfg.Now()
	plan := Plan{ID: "pl_" + auth.RandomToken(9), State: PlanPlanned, StateHash: hash, Steps: steps, CreatedBy: p.Login, CreatedAt: now, UpdatedAt: now}
	if err := s.cfg.Store.InsertPlan(ctx, plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *Service) planOn(ctx context.Context, r Repository) ([]Step, error) {
	pol, err := s.policyOrDefault(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	want, err := s.desired(ctx, pol, nil, pol.EphemeralOnly(r.Visibility))
	if err != nil {
		return nil, err
	}
	steps := []Step{{Action: ActPoolOn, Target: r.FullName, RepositoryID: r.ID, Pool: true,
		Text: "Turn the pool on for " + r.FullName}}
	for _, name := range namesToCheck(want, nil) {
		if val, ok := want[name]; ok {
			steps = append(steps, Step{Action: ActWriteVariable, Target: name, RepositoryID: r.ID, Value: val,
				Text: "Write variable " + name + " of " + r.FullName + " = " + val})
		}
	}
	return steps, nil
}

func (s *Service) planOff(ctx context.Context, r Repository) ([]Step, error) {
	steps := []Step{{Action: ActPoolOff, Target: r.FullName, RepositoryID: r.ID, Pool: false,
		Text: "Turn the pool off for " + r.FullName + ". Its runners are removed after their running jobs end."}}
	// GitHub is read fresh: the question must name what is really there.
	have, err := s.cfg.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return nil, err
	}
	var present []string
	for _, v := range have {
		if pools[v.Name] {
			present = append(present, v.Name)
		} else if isLegacyVariable(v.Name) || isSizeVar(v.Name) {
			if yes, err := s.mayDelete(ctx, r, v.Name); err != nil {
				return nil, err
			} else if yes {
				present = append(present, v.Name)
			}
		}
	}
	if len(present) == 0 {
		return steps, nil
	}
	files, err := s.workflowFiles(ctx, r)
	if err != nil {
		return nil, err
	}
	read := VariablesRead(files)
	sort.Strings(present)
	for _, name := range present {
		text := "Delete variable " + name + " of " + r.FullName + "."
		if users := read[name]; len(users) > 0 {
			text += " Workflows that read it will fail to start: " + strings.Join(users, ", ") + "."
		} else {
			text += " No workflow reads it."
		}
		steps = append(steps, Step{Action: ActDeleteVariable, Target: name, RepositoryID: r.ID, NeedsYes: true, Text: text})
	}
	return steps, nil
}

// stateHash fingerprints what a plan was made from: each repository's pool
// state, policy and managed variables.
func (s *Service) stateHash(ctx context.Context, changes []Change) (string, error) {
	ids := make([]int64, 0, len(changes))
	want := map[int64]bool{}
	for _, c := range changes {
		ids = append(ids, c.RepositoryID)
		want[c.RepositoryID] = c.Pool
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	h := sha256.New()
	for _, id := range ids {
		r, err := s.cfg.Store.GetRepository(ctx, id)
		if err != nil {
			return "", err
		}
		pol, err := s.policyOrDefault(ctx, id)
		if err != nil {
			return "", err
		}
		vars, err := s.cfg.Store.ListManagedVariables(ctx, id)
		if err != nil {
			return "", err
		}
		pol.UpdatedAt = pol.UpdatedAt.UTC()
		b, _ := json.Marshal([]any{id, r.PoolOn, want[id], pol, vars})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetPlan reads a plan with its outcome, for the page after a reload.
func (s *Service) GetPlan(ctx context.Context, p auth.Principal, id string) (Plan, error) {
	if err := auth.Require(p, auth.RoleViewer); err != nil {
		return Plan{}, err
	}
	return s.cfg.Store.GetPlan(ctx, id)
}

// ApplyPlan applies a plan after a yes. approved lists the step ids the user
// said yes to; a step that needs a yes and is not in the list is skipped
// (the pool still turns off; the variable stays). Before anything changes the
// user's GitHub admin permission is read fresh for every repository in the
// plan, and a stale plan is refused (R5.19, R5.20, R5.28, R5.29). Steps run in
// order and the outcome of each is saved as it finishes. A failed step stops
// the plan.
func (s *Service) ApplyPlan(ctx context.Context, p auth.Principal, id string, approved []string) (Plan, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return Plan{}, err
	}
	plan, err := s.cfg.Store.GetPlan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if plan.State != PlanPlanned {
		return plan, ErrPlanNotPlanned
	}
	changes := changesOf(plan)
	hash, err := s.stateHash(ctx, changes)
	if err != nil {
		return plan, err
	}
	if hash != plan.StateHash {
		plan.State = PlanStale
		plan.UpdatedAt = s.cfg.Now()
		_ = s.cfg.Store.UpdatePlan(ctx, plan)
		return plan, ErrPlanStale
	}
	repos := map[int64]Repository{}
	for _, c := range changes {
		r, err := s.cfg.Store.GetRepository(ctx, c.RepositoryID)
		if err != nil {
			return plan, err
		}
		if err := s.requireGitHubAdmin(ctx, p, r); err != nil {
			return plan, err // nothing has changed yet
		}
		if c.Pool {
			if err := s.RequirePublicAllowed(ctx, r); err != nil {
				return plan, err // R5.55; nothing has changed yet
			}
		}
		repos[r.ID] = r
	}
	yes := map[string]bool{}
	for _, a := range approved {
		yes[a] = true
	}
	plan.State = PlanApplying
	_ = s.cfg.Store.UpdatePlan(ctx, plan)
	s.beginRun(plan.ID)
	defer s.endRun(plan.ID)

	for i := range plan.Steps {
		st := &plan.Steps[i]
		if s.stopAsked(plan.ID) {
			// The step that ran has finished; no later step starts (web-console 11).
			for j := i; j < len(plan.Steps); j++ {
				plan.Steps[j].State = StepSkipped
			}
			plan.State = PlanStopped
			plan.UpdatedAt = s.cfg.Now()
			err := s.cfg.Store.UpdatePlan(ctx, plan)
			s.progress(plan)
			return plan, err
		}
		if st.NeedsYes && !yes[st.ID] {
			st.State = StepSkipped
			continue
		}
		st.State = StepRunning
		if err := s.runStep(ctx, p, repos[st.RepositoryID], *st); err != nil {
			st.State, st.Error = StepFailed, publicError(err)
			plan.State = PlanFailed
			for j := i + 1; j < len(plan.Steps); j++ {
				plan.Steps[j].State = StepSkipped
			}
			plan.UpdatedAt = s.cfg.Now()
			_ = s.cfg.Store.UpdatePlan(ctx, plan)
			s.progress(plan)
			return plan, nil
		}
		st.State = StepDone
		plan.UpdatedAt = s.cfg.Now()
		_ = s.cfg.Store.UpdatePlan(ctx, plan)
		s.progress(plan)
	}
	plan.State = PlanDone
	plan.UpdatedAt = s.cfg.Now()
	if err := s.cfg.Store.UpdatePlan(ctx, plan); err != nil {
		return plan, err
	}
	s.progress(plan)
	for id := range repos {
		if r2, err := s.cfg.Store.GetRepository(ctx, id); err == nil && r2.PoolOn {
			_ = s.CheckRunsOn(ctx, id)
		}
	}
	if s.cfg.OnPoolOn != nil { // the pool is on and its variables are written (R5.106)
		for _, c := range changesOf(plan) {
			if r2, err := s.cfg.Store.GetRepository(ctx, c.RepositoryID); c.Pool && err == nil && r2.PoolOn {
				s.cfg.OnPoolOn(ctx, r2)
			}
		}
	}
	return plan, nil
}

func changesOf(p Plan) []Change {
	seen := map[int64]bool{}
	var out []Change
	for _, st := range p.Steps {
		if seen[st.RepositoryID] {
			continue
		}
		seen[st.RepositoryID] = true
		switch st.Action {
		case ActPoolOn:
			out = append(out, Change{RepositoryID: st.RepositoryID, Pool: true})
		case ActPoolOff:
			out = append(out, Change{RepositoryID: st.RepositoryID, Pool: false})
		}
	}
	return out
}

func (s *Service) runStep(ctx context.Context, p auth.Principal, r Repository, st Step) error {
	switch st.Action {
	case ActPoolOn:
		// The setting may have been switched off since the plan was made (R5.55).
		if err := s.RequirePublicAllowed(ctx, r); err != nil {
			return err
		}
		pol, err := s.policyOrDefault(ctx, r.ID)
		if err != nil {
			return err
		}
		pol.UpdatedAt, pol.UpdatedBy = s.cfg.Now(), p.Login
		if err := s.cfg.Store.UpsertPolicy(ctx, r.ID, pol); err != nil {
			return err
		}
		if !pol.AllowForkPRs {
			if err := s.guardForks(ctx, r); err != nil {
				return err
			}
		}
		if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, true, p.Login, s.cfg.Now()); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, p, "pool.on", "repository", r.FullName, "")
		if err := s.SyncOrgGroup(ctx, r.InstallationID); err != nil { // R5.21: the group follows the pool switch
			return err
		}
		r.PoolOn = true
		s.syncRegistrations(ctx, p, r) // the pool runners get a registration for this repository
	case ActPoolOff:
		if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, false, p.Login, s.cfg.Now()); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, p, "pool.off", "repository", r.FullName, "no new runner is registered; running jobs finish")
		if err := s.SyncOrgGroup(ctx, r.InstallationID); err != nil {
			s.cfg.Audit.Audit(ctx, p, "org_pool.sync_failed", "repository", r.FullName, err.Error())
		}
		s.syncRegistrations(ctx, p, r) // drain and remove this repository's registrations
	case ActWriteVariable:
		if err := s.cfg.GitHub.SetVariable(ctx, r.InstallationID, r.Owner, r.Name, github.Variable{Name: st.Target, Value: st.Value}); err != nil {
			return err
		}
		if err := s.recordVariable(ctx, r.ID, st.Target, st.Value, VarOK, true); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, p, "variable.written", "repository", r.FullName, st.Target+" = "+st.Value)
		// CRSP229: the name an older version wrote for it goes once it is written.
		if _, err := s.migrateLegacy(ctx, p, r, func(n string) bool { return n == st.Target }); err != nil {
			return err
		}
	case ActDeleteVariable:
		if err := s.cfg.GitHub.DeleteVariable(ctx, r.InstallationID, r.Owner, r.Name, st.Target); err != nil {
			return err
		}
		if err := s.cfg.Store.DeleteManagedVariable(ctx, r.ID, st.Target); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, p, "variable.deleted", "repository", r.FullName, st.Target)
	default:
		return fmt.Errorf("unknown step %q", st.Action)
	}
	return nil
}

// publicError is the text of a failed step shown to a client: GitHub's reason
// in words, never a raw transport error.
func publicError(err error) string {
	var rl *github.RateLimitError
	var po *PublicOffError
	switch {
	case errors.As(err, &po):
		return po.Error()
	case errors.As(err, &rl):
		return "GitHub is limiting requests until " + rl.ResetAt.UTC().Format("15:04 UTC")
	case errors.Is(err, github.ErrUnavailable):
		return "GitHub could not be reached"
	case errors.Is(err, github.ErrForbidden):
		return "GitHub refused: a permission is missing"
	case errors.Is(err, github.ErrNotFound):
		return "GitHub says it is gone"
	}
	return "the step failed"
}

// StopPlan asks a plan that is applying to stop (web-console section 11). The
// step that runs finishes, no later step starts and the plan ends `stopped`.
// It answers the plan as it is now; a plan that is not applying is refused
// with ErrPlanNotApplying. A stop never cuts a running job: a step only
// changes GitHub variables and the pool switch.
func (s *Service) StopPlan(ctx context.Context, p auth.Principal, id string) (Plan, error) {
	if err := auth.Require(p, auth.RoleAdmin); err != nil {
		return Plan{}, err
	}
	plan, err := s.cfg.Store.GetPlan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if plan.State != PlanApplying {
		return plan, ErrPlanNotApplying
	}
	s.stopMu.Lock()
	running := s.running[id]
	if running {
		s.stops[id] = true
	}
	s.stopMu.Unlock()
	s.cfg.Audit.Audit(ctx, p, "plan.stop", "repository_plan", id, "stop asked while applying")
	if !running {
		// Nothing runs it (the controller restarted mid-apply): end it now.
		for i := range plan.Steps {
			if plan.Steps[i].State == StepPlanned || plan.Steps[i].State == StepRunning {
				plan.Steps[i].State = StepSkipped
			}
		}
		plan.State, plan.UpdatedAt = PlanStopped, s.cfg.Now()
		if err := s.cfg.Store.UpdatePlan(ctx, plan); err != nil {
			return plan, err
		}
	}
	s.progress(plan)
	return plan, nil
}

func (s *Service) stopAsked(id string) bool {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	return s.stops[id]
}

func (s *Service) beginRun(id string) {
	s.stopMu.Lock()
	s.running[id] = true
	delete(s.stops, id)
	s.stopMu.Unlock()
}

func (s *Service) endRun(id string) {
	s.stopMu.Lock()
	delete(s.running, id)
	delete(s.stops, id)
	s.stopMu.Unlock()
}

func (s *Service) progress(p Plan) {
	if s.cfg.OnProgress != nil {
		s.cfg.OnProgress(p)
	}
}
