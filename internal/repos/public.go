package repos

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Fleet removes the job runners of a repository that crsp stops serving
// (CRSP82). The runners service implements it through an adapter.
type Fleet interface {
	// RemoveRepositoryRunners removes every runner of the repository that has
	// no job; a runner with a job is left to end it. It returns the names removed.
	RemoveRepositoryRunners(ctx context.Context, repoID int64, actor string) ([]string, error)
}

// SetFleet sets who removes job runners. The wiring calls it once the runners
// service exists.
func (s *Service) SetFleet(f Fleet) { s.cfg.Fleet = f }

// StopServingPublic stops serving a repository that is public and whose pool is
// on, as the controller (github-integration R5.47, R5.55). In this order: the
// pool is turned off (no runner is registered after this; its settings stay),
// the CRSP_RUNS_ON variables are set to the hosted labels (so queued and new jobs
// run on GitHub), the light runner and the job runners are removed, and the
// audit log says why. Every step is tried even when an earlier one failed; the
// errors are joined. A repository whose pool is off is only marked public. It
// returns true when it stopped a served repository.
func (s *Service) StopServingPublic(ctx context.Context, r Repository, why string) (bool, error) {
	return s.stopPublic(ctx, System, r, why)
}

func (s *Service) stopPublic(ctx context.Context, p auth.Principal, r Repository, why string) (bool, error) {
	if r.Visibility != "public" {
		r.Visibility = "public"
		if err := s.cfg.Store.UpsertRepository(ctx, r); err != nil {
			return false, err
		}
	}
	if !r.PoolOn {
		return false, nil
	}
	if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, false, p.Login, s.cfg.Now()); err != nil {
		return false, err
	}
	s.dropFallback(r.ID)
	s.cfg.Audit.Audit(ctx, p, "pool.off", "repository", r.FullName,
		"the repository is public and public repositories are off ("+why+"); its settings are kept")
	var errs []error
	if err := s.SyncOrgGroup(ctx, r.InstallationID); err != nil {
		errs = append(errs, err)
	}
	if err := s.hostedVariables(ctx, r); err != nil {
		errs = append(errs, err)
	}
	s.syncRegistrations(ctx, p, r)
	if s.cfg.Fleet != nil {
		names, err := s.cfg.Fleet.RemoveRepositoryRunners(ctx, r.ID, p.Login)
		if err != nil {
			errs = append(errs, err)
		}
		if len(names) > 0 {
			s.cfg.Audit.Audit(ctx, p, "runners.removed", "repository", r.FullName, strings.Join(names, ", "))
		}
	}
	if err := errors.Join(errs...); err != nil {
		s.cfg.Audit.Audit(ctx, p, "public.stop_failed", "repository", r.FullName, publicError(err))
		return true, err
	}
	return true, nil
}

// hostedVariables sets every CRSP_RUNS_ON variable that exists on the repository to
// the hosted label of its OS. A name an older version wrote (RUNS_ON_<OS>,
// CI_RUNS_ON, CI_RUNS_ON_WINDOWS) is set only where crsp wrote it.
func (s *Service) hostedVariables(ctx context.Context, r Repository) error {
	pol, err := s.policyOrDefault(ctx, r.ID)
	if err != nil {
		return err
	}
	have, err := s.cfg.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return err
	}
	osOf := map[string]string{}
	for _, v := range variableOrder {
		osOf[v.Var] = v.OS
	}
	var errs []error
	for _, v := range have {
		os, ok := osOf[v.Name]
		if !ok {
			os = legacyOS(v.Name)
		}
		if os == "" || isSizeVar(v.Name) || legacySizeVarRE.MatchString(v.Name) {
			continue
		}
		if !ok { // a plain name an older version wrote: only where crsp wrote it
			if yes, err := s.mayDelete(ctx, r, v.Name); err != nil || !yes {
				if err != nil {
					errs = append(errs, err)
				}
				continue
			}
		}
		b, _ := json.Marshal([]string{hostedLabel(pol, os)})
		val := string(b)
		if sameLabels(v.Value, val) {
			continue
		}
		if err := s.cfg.GitHub.SetVariable(ctx, r.InstallationID, r.Owner, r.Name, github.Variable{Name: v.Name, Value: val}); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.recordVariable(ctx, r.ID, v.Name, val, VarOK, true); err != nil {
			errs = append(errs, err)
			continue
		}
		s.cfg.Audit.Audit(ctx, System, "variable.written", "repository", r.FullName, v.Name+" = "+val)
	}
	return errors.Join(errs...)
}

// Publicized is the repository webhook with action `publicized`: the repository
// just turned public. It records the visibility and stops nothing (R5.41): a
// repository already on the pool keeps working, and while public repositories
// are off the repository carries the problem public_pool_on_while_off. A
// repository crsp does not know is ignored.
func (s *Service) Publicized(ctx context.Context, fullName string) error {
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Query: fullName})
	if err != nil {
		return err
	}
	for _, r := range rs {
		if !strings.EqualFold(r.FullName, fullName) {
			continue
		}
		if r.Visibility == "public" {
			return nil
		}
		r.Visibility = "public"
		if err := s.cfg.Store.UpsertRepository(ctx, r); err != nil {
			return err
		}
		if r.PoolOn {
			s.cfg.Audit.Audit(ctx, System, "repository.publicized", "repository", r.FullName,
				"GitHub reported it public (publicized webhook); the pool stays on")
		}
		return nil
	}
	return nil
}
