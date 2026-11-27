package runners

import (
	"context"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// MachineRunnerResult is the outcome for one runner of a machine that is removed.
type MachineRunnerResult struct {
	Name   string
	Done   bool
	Reason string
}

// RemoveMachineRunners deregisters every runner of a machine that is being removed
// (machines R5.40 to R5.42). Each runner is deleted at GitHub; a runner GitHub
// does not know counts as done. When the agent is online (agentUp) the service is
// stopped and its files are cleaned up too; when it is offline the local files stay
// until the agent is uninstalled. The record goes only when the runner is gone at
// GitHub, so one that could not be removed is listed with the reason, never lost.
func (s *Service) RemoveMachineRunners(ctx context.Context, machineID, actor string, agentUp bool) []MachineRunnerResult {
	rs, err := s.Store.ListRunners(ctx, Filter{MachineID: machineID})
	if err != nil {
		return []MachineRunnerResult{{Name: "the runners of the machine", Reason: err.Error()}}
	}
	var out []MachineRunnerResult
	for _, r := range rs {
		if !r.Managed {
			continue
		}
		res := MachineRunnerResult{Name: r.Name}
		if err := s.deregisterAtGitHub(ctx, r); err != nil {
			res.Reason = err.Error()
			s.audit(ctx, actor, "runner.remove_machine_failed", machineID, r.ID, r.Name+": "+res.Reason)
			out = append(out, res)
			continue
		}
		if agentUp && r.ServiceName != "" {
			_ = s.Agent.StopService(ctx, machineID, r.ServiceName)
			if err := s.Agent.CleanUp(ctx, machineID, r.ServiceName); err != nil {
				res.Reason = "deregistered at GitHub; its files could not be removed: " + err.Error()
			}
		} else if !agentUp {
			res.Reason = "deregistered at GitHub; its files stay until the agent is uninstalled"
		}
		if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
			res.Reason = "deregistered at GitHub; its record could not be removed: " + err.Error()
		}
		res.Done = true
		s.audit(ctx, actor, "runner.remove_machine", machineID, r.ID, r.Name)
		out = append(out, res)
	}
	return out
}

// deregisterAtGitHub deletes the runner at GitHub. Unlike removeSafely it does not
// wait for the runner to be idle: the machine drained first, or it is offline.
func (s *Service) deregisterAtGitHub(ctx context.Context, r Record) error {
	if s.GitHub == nil || s.Resolver == nil {
		return errors.New("GitHub is not set up")
	}
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return err
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fmt.Errorf("the runner list at GitHub could not be read: %w", err)
	}
	for _, g := range list {
		if g.Name != r.Name {
			continue
		}
		if err := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, g.ID); err != nil && !errors.Is(err, github.ErrNotFound) {
			return fmt.Errorf("GitHub refused to remove it: %w", err)
		}
		return nil
	}
	return nil
}
