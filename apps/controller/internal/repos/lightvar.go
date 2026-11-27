package repos

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// lightTarget is the value RUNS_ON_LIGHT gets once light runners are gone: the
// pool's normal labels for one OS, the same JSON list a normal job type gets.
// It takes the first OS of the fixed order (Linux, Windows, macOS) that the
// repository has a variable for. ok is false when the repository has none, so
// no runner of the pool could take the job anyway.
func lightTarget(want map[string]string) (value string, ok bool) {
	for _, v := range variableOrder {
		if val, has := want[v.Var]; has {
			return val, true
		}
	}
	return "", false
}

// migrateLightVariable rewrites RUNS_ON_LIGHT when it still holds the removed
// light runner label, so no workflow waits for a runner that no longer exists
// (decision D110, 19 Nov 2026; R5.65). A copy this controller wrote earlier
// follows the normal labels from then on; one a person wrote by hand is left
// alone. A second pass finds nothing to do. have is the variable list just read.
func (s *Service) migrateLightVariable(ctx context.Context, actor auth.Principal, r Repository, want map[string]string, have []github.Variable) error {
	got, exists := "", false
	for _, v := range have {
		if v.Name == VarLight {
			got, exists = v.Value, true
		}
	}
	if !exists {
		return nil
	}
	target, ok := lightTarget(want)
	if !ok {
		return nil
	}
	managed, err := s.managed(ctx, r.ID, VarLight)
	if err != nil {
		return err
	}
	if got != LightValue && (!managed || sameLabels(got, target)) {
		return nil
	}
	if err := s.cfg.GitHub.SetVariable(ctx, r.InstallationID, r.Owner, r.Name, github.Variable{Name: VarLight, Value: target}); err != nil {
		return err
	}
	// A copy the store does not list stays unlisted, so the upgrade to the
	// CRSP_RUNS_ON_* names (CRSP229) never deletes it.
	if managed {
		if err := s.recordVariable(ctx, r.ID, VarLight, target, VarOK, true); err != nil {
			return err
		}
	}
	s.cfg.Audit.Audit(ctx, actor, "variable.migrated", "repository", r.FullName,
		VarLight+" pointed at the removed light runner label; set to "+target)
	return nil
}
