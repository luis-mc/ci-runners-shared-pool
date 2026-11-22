package repos

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/trial"
)

// Trial hooks (github-integration R5.66). The trial modes change variables
// only through these methods, so the variable writing stays here: the same
// GitHub calls, the store record and the audit row as syncVariables. The
// controller's own variables (keptNames) are never written or deleted here.

// Dispatcher starts a workflow; the GitHub client implements it.
type Dispatcher interface {
	DispatchWorkflow(ctx context.Context, installationID int64, owner, repo, workflow, ref string, inputs map[string]string) error
}

// TrialVars is the trial's view of the Service: it implements trial.Variables
// and trial.Dispatcher.
type TrialVars struct {
	s    *Service
	disp Dispatcher
	by   auth.Principal
}

// Trial returns the hooks for the trial package. disp may be nil.
func (s *Service) Trial(disp Dispatcher) *TrialVars { return &TrialVars{s: s, disp: disp, by: System} }

func (t *TrialVars) find(ctx context.Context, full string) (Repository, error) {
	list, err := t.s.cfg.Store.ListRepositories(ctx, ListFilter{Query: full})
	if err != nil {
		return Repository{}, err
	}
	for _, r := range list {
		if strings.EqualFold(r.FullName, full) {
			return r, nil
		}
	}
	return Repository{}, fmt.Errorf("repository %s is not among the installations", full)
}

// Repository implements trial.Variables.
func (t *TrialVars) Repository(ctx context.Context, full string) (trial.Repo, error) {
	r, err := t.find(ctx, full)
	if err != nil {
		return trial.Repo{}, err
	}
	return trial.Repo{FullName: r.FullName, Public: r.Visibility == "public"}, nil
}

// trialName says whether name is a canary variable: CRSP_RUNS_ON_CANARY_<JOB>,
// or RUNS_ON_CANARY_<JOB> that an older version wrote, so a canary started
// before the upgrade still stops cleanly.
func trialName(name string) bool {
	return (strings.HasPrefix(name, trial.CanaryPrefix) || strings.HasPrefix(name, trial.LegacyCanaryPrefix)) && !inList(keptNames(), name)
}

// Variables implements trial.Variables: the CRSP_RUNS_ON family set now.
func (t *TrialVars) Variables(ctx context.Context, full string) (map[string]string, error) {
	r, err := t.find(ctx, full)
	if err != nil {
		return nil, err
	}
	have, err := t.s.cfg.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, v := range have {
		if strings.HasPrefix(v.Name, "CRSP_RUNS_ON") || strings.HasPrefix(v.Name, "RUNS_ON") || strings.HasPrefix(v.Name, "CI_RUNS_ON") {
			out[v.Name] = v.Value
		}
	}
	return out, nil
}

// Write implements trial.Variables for a canary variable.
func (t *TrialVars) Write(ctx context.Context, full, name, value string) error {
	if !trialName(name) {
		return fmt.Errorf("%s is not a trial variable", name)
	}
	r, err := t.find(ctx, full)
	if err != nil {
		return err
	}
	if err := t.s.cfg.GitHub.SetVariable(ctx, r.InstallationID, r.Owner, r.Name, github.Variable{Name: name, Value: value}); err != nil {
		return err
	}
	if err := t.s.recordVariable(ctx, r.ID, name, value, VarOK, true); err != nil {
		return err
	}
	t.s.cfg.Audit.Audit(ctx, t.by, "variable.written", "repository", r.FullName, name+" = "+value+" (trial)")
	return nil
}

// Remove implements trial.Variables for a canary variable.
func (t *TrialVars) Remove(ctx context.Context, full, name string) error {
	if !trialName(name) {
		return fmt.Errorf("%s is not a trial variable", name)
	}
	r, err := t.find(ctx, full)
	if err != nil {
		return err
	}
	return t.s.deleteVariable(ctx, t.by, r, name)
}

// Desired implements trial.Variables: what the controller would write now.
func (t *TrialVars) Desired(ctx context.Context, full string) (map[string]string, error) {
	r, err := t.find(ctx, full)
	if err != nil {
		return nil, err
	}
	pol, err := t.s.policyOrDefault(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	// The crsp labels, not the hosted ones: a repository on fallback still
	// gets a canary on crsp.
	return t.s.desired(ctx, pol, nil, pol.EphemeralOnly(r.Visibility))
}

// Dispatch implements trial.Dispatcher.
func (t *TrialVars) Dispatch(ctx context.Context, full, workflow, ref string, inputs map[string]string) error {
	r, err := t.find(ctx, full)
	if err != nil {
		return err
	}
	if r.Visibility == "public" {
		return trial.ErrPublic
	}
	if t.disp == nil {
		return fmt.Errorf("no way to start workflows")
	}
	if err := t.disp.DispatchWorkflow(ctx, r.InstallationID, r.Owner, r.Name, workflow, ref, inputs); err != nil {
		return err
	}
	t.s.cfg.Audit.Audit(ctx, t.by, "trial.shadow", "repository", r.FullName, workflow+" "+ref)
	return nil
}
