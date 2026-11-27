package runners

import (
	"context"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// EphemeralOnMachine counts what holds a job slot of a machine now: the
// ephemeral runners made for a job (every state except failed) and the
// persistent Linux container runners of the pool (runners R5.93). The ceiling
// slot records of an ephemeral pool request are not counted: they are the
// limit, not a runner (R5.92). The demand loop uses it to keep within the slot
// budget of the machine (scheduling R5.1, runners R5.27).
func (s *Service) EphemeralOnMachine(ctx context.Context, machineID string) (int, error) {
	rs, err := s.Store.ListRunners(ctx, Filter{MachineID: machineID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		switch {
		case r.State == Failed, isCeilingSlot(r):
		case r.Kind == "ephemeral":
			n++
		case IsPoolRunner(r) && r.Isolation == "container":
			n++
		}
	}
	return n, nil
}

// isCeilingSlot is the record an ephemeral pool request leaves for each
// ephemeral runner that may run at once on a machine (runners R5.92).
func isCeilingSlot(r Record) bool { return r.Kind == "ephemeral" && IsPoolRunner(r) }

// EphemeralCeiling is how many ephemeral runners may run at once on a machine:
// the number of ceiling slot records the owner added (runners R5.92). Zero
// means no ephemeral runner starts there.
func (s *Service) EphemeralCeiling(ctx context.Context, machineID string) (int, error) {
	rs, err := s.Store.ListRunners(ctx, Filter{MachineID: machineID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		if isCeilingSlot(r) && r.State != Failed {
			n++
		}
	}
	return n, nil
}

// EphemeralCapacity is true when at least one ceiling slot exists on a machine
// that is online, so jobs that ask for ephemeral runners can run (runners
// R5.92). It reads the machine through the Machines lookup; a machine that
// cannot be read does not count.
func (s *Service) EphemeralCapacity(ctx context.Context) (bool, error) {
	rs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return false, err
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if !isCeilingSlot(r) || r.State == Failed || seen[r.MachineID] {
			continue
		}
		seen[r.MachineID] = true
		if s.Machines == nil {
			return true, nil
		}
		if m, err := s.Machines.GetMachine(ctx, r.MachineID); err == nil && m.Online {
			return true, nil
		}
	}
	return false, nil
}

// FinishJobRunner cleans up the runner of a job that ended: the agent removes
// the job folder and its files, then the record goes (runners R5.25, R5.26).
// GitHub drops an ephemeral runner by itself after its one job.
func (s *Service) FinishJobRunner(ctx context.Context, runnerID, actor string) error {
	r, err := s.Store.GetRunner(ctx, runnerID)
	if err != nil {
		return err
	}
	if r.Kind != "ephemeral" {
		return refuse("runner %s is not ephemeral", r.Name)
	}
	if err := s.Agent.CleanUp(ctx, r.MachineID, r.ServiceName); err != nil {
		return err
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "runner.job_done", r.MachineID, r.ID, r.Name)
	return nil
}

// ErrRunnerBusy means GitHub says the runner has a job, so it is not removed.
var ErrRunnerBusy = errors.New("the runner has a job")

// RemoveIdleRunner removes an ephemeral runner that never got a job and frees
// its slot (runners R5.26). It is the safe removal of R5.62: see removeSafely.
func (s *Service) RemoveIdleRunner(ctx context.Context, runnerID, actor string) error {
	r, err := s.Store.GetRunner(ctx, runnerID)
	if err != nil {
		return err
	}
	if r.Kind != "ephemeral" {
		return refuse("runner %s is not ephemeral", r.Name)
	}
	return s.removeSafely(ctx, r, actor, "runner.idle_removed")
}

// removeSafely is the one order every removal of a runner follows (runners
// R5.62): ask GitHub whether the runner is idle, deregister it at GitHub, and
// only then stop the listener and clean up. GitHub therefore never lists a
// runner with nothing behind it, and a busy runner is never touched.
//
//   - A busy runner, or a delete GitHub refuses with 422 because a job came
//     in between, returns ErrRunnerBusy and changes nothing.
//   - A runner GitHub does not know (not listed, or a 404 on delete) counts
//     as already deregistered, and the local clean-up goes on.
//   - An unreadable runner list changes nothing (fail closed).
//
// Each outcome is logged to the audit trail with the runner name.
func (s *Service) removeSafely(ctx context.Context, r Record, actor, action string) error {
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return err
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return err // fail closed: an unreadable list removes nothing
	}
	known := false
	for _, g := range list {
		if g.Name != r.Name {
			continue
		}
		if g.Busy {
			s.audit(ctx, actor, "runner.remove_left_busy", r.MachineID, r.ID, r.Name)
			return ErrRunnerBusy
		}
		known = true
		err := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, g.ID)
		switch {
		case errors.Is(err, github.ErrRunnerBusy):
			s.audit(ctx, actor, "runner.remove_left_busy", r.MachineID, r.ID, r.Name+" (GitHub answered 422)")
			return ErrRunnerBusy
		case errors.Is(err, github.ErrNotFound):
			known = false // gone between the list and the delete
		case err != nil:
			return fmt.Errorf("remove the runner at GitHub: %w", err)
		}
		break
	}
	// The listener may already be gone; the clean-up below is what counts.
	stopErr := s.Agent.StopService(ctx, r.MachineID, r.ServiceName)
	if err := s.Agent.CleanUp(ctx, r.MachineID, r.ServiceName); err != nil {
		return err
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return err
	}
	detail := r.Name
	if !known {
		detail += " (GitHub no longer knew it)"
	}
	if stopErr != nil {
		detail += " (stop: " + stopErr.Error() + ")"
	}
	s.audit(ctx, actor, action, r.MachineID, r.ID, detail)
	return nil
}

// RemoveRepositoryRunners removes every ephemeral runner of a repository that
// has no job, so a repository crsp stops serving keeps none (CRSP82). A busy
// runner is left to finish its one job. It returns the names it removed and
// the joined errors of the others.
func (s *Service) RemoveRepositoryRunners(ctx context.Context, repoID int64, actor string) ([]string, error) {
	rs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	var names []string
	var errs []error
	for _, r := range rs {
		if r.Kind != "ephemeral" || r.Scope.RepositoryID != repoID {
			continue
		}
		switch err := s.RemoveIdleRunner(ctx, r.ID, actor); {
		case err == nil:
			names = append(names, r.Name)
		case errors.Is(err, ErrRunnerBusy):
		default:
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
		}
	}
	return names, errors.Join(errs...)
}
