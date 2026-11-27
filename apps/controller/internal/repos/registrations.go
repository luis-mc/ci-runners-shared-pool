package repos

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// syncRegistrations makes the pool runners' hidden registrations follow the
// pool switch. It is best effort: the pool switch is already stored, a failure
// is written to the audit log and the reconcile loop tries again (runners
// R5.88).
func (s *Service) syncRegistrations(ctx context.Context, p auth.Principal, r Repository) {
	if s.cfg.Registrar == nil {
		return
	}
	if err := s.cfg.Registrar.SyncPoolRegistrations(ctx, p.Login); err != nil {
		s.cfg.Audit.Audit(ctx, p, "pool.registrations_failed", "repository", r.FullName, err.Error())
	}
}

// ReconcileRegistrations runs the registrar once; the reconcile loop calls it
// so a registration that failed, or a pool runner added while a repository
// already used the pool, is made without a pool switch.
func (s *Service) ReconcileRegistrations(ctx context.Context) error {
	if s.cfg.Registrar == nil {
		return nil
	}
	return s.cfg.Registrar.SyncPoolRegistrations(ctx, System.Login)
}
