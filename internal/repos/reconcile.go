package repos

import (
	"context"
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// ReconcileInstallations follows installation changes by reading the App's
// installations and their repositories with the App's own credentials (R5.22,
// R5.24). A repository that left an installation, or whose installation went
// away, is turned off and the audit log says why. Run every 10 minutes, and on
// an installation webhook.
func (s *Service) ReconcileInstallations(ctx context.Context) error {
	bg := github.Background(ctx)
	live, err := s.cfg.GitHub.ListAppInstallations(bg)
	if err != nil {
		return err
	}
	known, err := s.cfg.Store.ListInstallations(ctx)
	if err != nil {
		return err
	}
	liveIDs := map[int64]bool{}
	var fresh []Repository    // repositories the store did not know before this read
	reach := map[int64]bool{} // repository ids still reachable
	for _, in := range live {
		liveIDs[in.ID] = true
		if err := s.cfg.Store.UpsertInstallation(ctx, toInstallation(in)); err != nil {
			return err
		}
		if in.Suspended {
			// A suspended installation is not a removed one: its repositories
			// keep their pool state until GitHub lifts the suspension (CRSP148).
			for _, r := range s.reposOf(ctx, in.ID) {
				reach[r.ID] = true
			}
			continue
		}
		rs, err := s.cfg.GitHub.ListAppRepositories(bg, in.ID)
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				return err
			}
			// Keep what we know for this installation; try again next round.
			for _, r := range s.reposOf(ctx, in.ID) {
				reach[r.ID] = true
			}
			continue
		}
		for _, r := range rs {
			reach[r.ID] = true
			if _, err := s.cfg.Store.GetRepository(ctx, r.ID); errors.Is(err, auth.ErrNotFound) {
				fresh = append(fresh, toRepository(r, in.ID))
			} else if err != nil {
				return err
			}
			if err := s.cfg.Store.UpsertRepository(ctx, toRepository(r, in.ID)); err != nil {
				return err
			}
		}
	}
	s.adoptPools(ctx, fresh)
	for _, k := range known {
		if !liveIDs[k.ID] {
			if err := s.cfg.Store.MarkInstallationRemoved(ctx, k.ID); err != nil {
				return err
			}
		}
	}
	pool := true
	on, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &pool})
	if err != nil {
		return err
	}
	for _, r := range on {
		if reach[r.ID] {
			continue
		}
		if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, false, ChangedByInstallation, s.cfg.Now()); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, System, "pool.off", "repository", r.FullName, "the repository left the App's installation")
		if err := s.SyncOrgGroup(ctx, r.InstallationID); err != nil {
			s.cfg.Audit.Audit(ctx, System, "org_pool.sync_failed", "repository", r.FullName, err.Error())
		}
	}
	// A repository that came back keeps its pool off, but the console must
	// stop saying it left (R5.22).
	off := false
	gone, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &off})
	if err != nil {
		return err
	}
	for _, r := range gone {
		if r.PoolChangedBy == ChangedByInstallation && reach[r.ID] {
			if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, false, System.Login, s.cfg.Now()); err != nil {
				return err
			}
		}
	}
	// A repository that left the installation is off now: its registrations
	// go. Registrations that failed earlier are tried again here.
	if err := s.ReconcileRegistrations(ctx); err != nil {
		s.cfg.Audit.Audit(ctx, System, "pool.registrations_failed", "repository", "", err.Error())
	}
	// The visibility just read was stored with each repository above. It stops
	// nothing: a public repository on the pool carries a problem while public
	// repositories are off (R5.41, R5.56).
	return nil
}

func (s *Service) reposOf(ctx context.Context, installationID int64) []Repository {
	rs, _ := s.cfg.Store.ListRepositories(ctx, ListFilter{InstallationID: installationID})
	return rs
}

// adoptPools turns the pool on for a repository the store does not know yet but
// whose variables show the pool already runs it: RUNS_ON_LIGHT, or a
// CRSP_RUNS_ON_<OS> or older RUNS_ON_<OS> variable with a crsp label. The store of an older version lived in memory, so
// an upgrade left the repositories with their pool off while GitHub still held
// the variables (CRSP148). A GitHub error leaves the repository as it is.
func (s *Service) adoptPools(ctx context.Context, fresh []Repository) {
	for _, r := range fresh {
		vars, err := s.cfg.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
		if err != nil {
			continue
		}
		if !poolVariables(vars) {
			continue
		}
		if err := s.cfg.Store.SetRepositoryPool(ctx, r.ID, true, System.Login, s.cfg.Now()); err != nil {
			continue
		}
		s.cfg.Audit.Audit(ctx, System, "pool.adopted", "repository", r.FullName, "the repository's variables show the pool runs it; the pool switch was restored")
	}
}

func poolVariables(vars []github.Variable) bool {
	for _, v := range vars {
		if v.Name == VarLight && v.Value == LightValue {
			return true
		}
		for _, o := range variableOrder {
			if v.Name == o.Var && strings.Contains(v.Value, "crsp-") {
				return true
			}
		}
		if legacyOS(v.Name) != "" && strings.Contains(v.Value, "crsp-") {
			return true // RUNS_ON_<OS>, the name an older version wrote
		}
	}
	return false
}
