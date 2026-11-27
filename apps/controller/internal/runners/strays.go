package runners

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// The two halves of a half-registered runner (runners R5.94, doctor R5.50):
// a runner GitHub lists that no local record owns, and a local record GitHub
// does not list. The Doctor finds each; these remove it after a yes. Neither
// runs on its own.

// StrayRegistration names a GitHub runner no local record owns.
type StrayRegistration struct {
	InstallationID int64
	Scope          github.RunnerScope
	GitHubID       int64
	Name           string
}

// RemoveStrayRegistration deletes a GitHub runner that carries the pool's name
// prefix and that no local record owns. It checks both again right now: the
// runner is still listed under that id and name, still has no record, and has
// no job. Without confirm it names what goes.
func (s *Service) RemoveStrayRegistration(ctx context.Context, in StrayRegistration, prefix string, confirm bool, actor string) error {
	if in.GitHubID == 0 || in.Name == "" {
		return refuse("a stray registration needs the GitHub runner id and its name")
	}
	if prefix != "" && !strings.HasPrefix(in.Name, prefix) {
		return refuse("runner %s does not carry the pool's name prefix %s; crsp leaves it alone", in.Name, prefix)
	}
	list, err := s.GitHub.ListRunners(ctx, in.InstallationID, in.Scope)
	if err != nil {
		return err // fail closed
	}
	var g *github.Runner
	for i := range list {
		if list[i].ID == in.GitHubID && list[i].Name == in.Name {
			g = &list[i]
		}
	}
	if g == nil {
		return refuse("GitHub does not list runner %s with that id any more", in.Name)
	}
	if g.Busy {
		return refuse("GitHub says runner %s has a job; it is left alone", in.Name)
	}
	recs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Name == in.Name || (r.GitHubRunnerID != 0 && r.GitHubRunnerID == in.GitHubID) {
			return refuse("runner %s has a local record on this controller; it is not a stray", in.Name)
		}
	}
	if !confirm {
		return &NeedsConfirmation{Items: []ConfirmItem{{"registration", "The registration of runner " + in.Name + " on GitHub, which no machine runs"}}}
	}
	err = s.GitHub.DeleteRunner(ctx, in.InstallationID, in.Scope, in.GitHubID)
	switch {
	case errors.Is(err, github.ErrRunnerBusy):
		return ErrRunnerBusy
	case errors.Is(err, github.ErrNotFound):
		// Gone already: nothing left to do.
	case err != nil:
		return fmt.Errorf("deregister at GitHub: %w", err)
	}
	s.audit(ctx, actor, "runner.stray_registration_removed", "", fmt.Sprint(in.GitHubID), in.Name)
	return nil
}

// RemoveRecordWithoutRegistration removes the local record of a managed runner
// GitHub does not list, and cleans its service, account and folder on the
// machine when the agent answers. GitHub is read right now and must say the
// runner is not there; a list that cannot be read refuses (fail closed). A
// runner that is being set up, drained or removed is left to that work.
func (s *Service) RemoveRecordWithoutRegistration(ctx context.Context, id string, confirm bool, actor string) (Record, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if !r.Managed {
		return r, refuse("runner %s was not made by CRSP; crsp does not remove it", r.Name)
	}
	switch r.State {
	case Creating, Draining, Updating, Removing:
		return r, refuse("runner %s is %s; wait until that ends", r.Name, r.State)
	}
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return r, err
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return r, err // fail closed
	}
	for _, g := range list {
		if g.Name == r.Name || (r.GitHubRunnerID != 0 && g.ID == r.GitHubRunnerID) {
			return r, refuse("GitHub lists runner %s; it is registered, so its record stays", r.Name)
		}
	}
	if !confirm {
		return r, &NeedsConfirmation{Items: []ConfirmItem{
			{"record", "The record of runner " + r.Name + " in crsp, which GitHub does not list"},
			{"service", "The service, account and folder of runner " + r.Name + " on its machine, if the machine answers"},
		}}
	}
	detail := r.Name + ", not on GitHub, removed after a yes"
	if r.ServiceName != "" {
		if err := s.Agent.StopService(ctx, r.MachineID, r.ServiceName); err != nil {
			detail += " (stop: " + err.Error() + ")"
		}
		if err := s.Agent.CleanUp(ctx, r.MachineID, r.ServiceName); err != nil {
			detail += " (clean up: " + err.Error() + ")"
		}
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return r, err
	}
	s.audit(ctx, actor, "runner.record_without_registration_removed", r.MachineID, r.ID, detail)
	return r, nil
}
