package repos

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// PoolOnRepositories lists the repositories that have the pool on, by name
// (the pool rename writes the variables of each).
func (s *Service) PoolOnRepositories(ctx context.Context) ([]Repository, error) {
	on := true
	return s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &on})
}

// SyncPoolVariables makes the CRSP_RUNS_ON_* variables of one pool-on
// repository what the product writes now: the same writer as the restore loop
// and Repair, so the label in them is the pool name in force. A repository that
// has left the pool since is left alone. actor is the login named in the audit
// entries.
func (s *Service) SyncPoolVariables(ctx context.Context, repoID int64, actor string) error {
	r, err := s.cfg.Store.GetRepository(ctx, repoID)
	if err != nil {
		return err
	}
	if !r.PoolOn {
		return nil
	}
	pol, err := s.policyOrDefault(ctx, r.ID)
	if err != nil {
		return err
	}
	p := System
	if actor != "" {
		p.Login = actor
	}
	return s.syncVariables(github.Background(ctx), p, r, pol)
}
