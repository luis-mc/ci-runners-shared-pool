package runners

import "context"

// registrationScope is the scope a new runner registers in (github-integration
// R5.21). A runner of a repository of an organization whose pool mode is on
// registers at organization level, in the runner group named for the pool; the
// record keeps the repository id, so the repository's runners are still found.
// Every other runner, including each runner of a personal account, keeps its
// scope. The record stores the answer, so a runner is removed with the scope it
// was registered with, whatever the mode is later.
func (s *Service) registrationScope(ctx context.Context, sc Scope) (Scope, error) {
	if sc.Kind != "repository" {
		return sc, nil
	}
	os, ok := s.Resolver.(OrgScoper)
	if !ok {
		return sc, nil
	}
	org, on, err := os.OrgScope(ctx, sc)
	if err != nil {
		return Scope{}, err
	}
	if !on {
		return sc, nil
	}
	return org, nil
}
