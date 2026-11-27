package runners

import (
	"context"
	"fmt"
)

// discardFailed takes away what a failed runner left: a GitHub registration
// when there is one, its service with the account and folder, and its record.
// It stops at the first step that fails, so the record stays and a person can
// see what is left.
func (s *Service) discardFailed(ctx context.Context, r Record, actor, action string) error {
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fmt.Errorf("the runner list cannot be read, so nothing is removed: %w", err)
	}
	for _, g := range list {
		if g.Name != r.Name && (r.GitHubRunnerID == 0 || g.ID != r.GitHubRunnerID) {
			continue
		}
		if g.Busy {
			return fmt.Errorf("runner %s has a job on GitHub; it waits until the job ends", r.Name)
		}
		if err := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, g.ID); err != nil {
			return fmt.Errorf("remove the registration of %s: %w", r.Name, err)
		}
	}
	service := r.ServiceName
	if service == "" && s.Services != nil {
		if found, err := s.Services(ctx, r.MachineID); err == nil {
			for _, f := range found {
				if f.Name == r.Name {
					service = f.ServiceName
				}
			}
		}
	}
	if service != "" {
		if err := s.Agent.CleanUp(ctx, r.MachineID, service); err != nil {
			return fmt.Errorf("clean up the service %s: %w", service, err)
		}
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, action, r.MachineID, r.ID, r.Name)
	return nil
}
