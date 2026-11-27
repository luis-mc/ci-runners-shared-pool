package repos

import "context"

// AnyPoolOn reports whether at least one repository has its pool on. It reads
// the store only, with no per-repository view, so a variable or finding read
// cannot hide a pool-on repository from the first-run state.
func (s *Service) AnyPoolOn(ctx context.Context) (bool, error) {
	on := true
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &on, Limit: 1})
	if err != nil {
		return false, err
	}
	return len(rs) > 0, nil
}
