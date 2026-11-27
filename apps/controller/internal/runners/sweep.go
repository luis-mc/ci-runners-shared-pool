package runners

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// SweepPrefix starts the name of every runner crsp makes for a job. A runner
// whose name does not start with it is never swept.
const SweepPrefix = "crsp-"

// SweepResult says what one sweep did.
type SweepResult struct {
	Deleted  []string // names deregistered and cleaned up
	LeftBusy []string // names GitHub says run a job (also a 422 on delete)
	Failed   []string // names the sweep could not remove; tried again next time
}

// SweepOffline deregisters the offline runner registrations that crsp made
// and that are older than grace (github-integration R5.33). A zero grace turns
// the sweep off.
//
// What counts as crsp's own is decided by crsp's own records, never by what
// GitHub lists: a registration is swept only when
//
//   - crsp has a record of it (managed, ephemeral, same name, and the same
//     GitHub id when the record has one), and
//   - its name carries the crsp prefix, and
//   - the record is older than grace, and the record is not in a live state
//     (creating, busy, draining, updating, removing), and
//   - GitHub lists the registration as offline and not busy.
//
// A runner GitHub lists that has no record, or whose name is foreign, is not
// looked at again. A 422 on the delete means busy: the runner is left alone.
// The sweep logs each deletion with the runner name and its age.
func (s *Service) SweepOffline(ctx context.Context, grace time.Duration, actor string) (SweepResult, error) {
	var res SweepResult
	if grace <= 0 {
		return res, nil
	}
	managed := true
	recs, err := s.Store.ListRunners(ctx, Filter{Managed: &managed})
	if err != nil {
		return res, err
	}
	now := s.now()
	byScope := map[Scope][]Record{}
	for _, r := range recs {
		if !sweepable(r, now, grace) {
			continue
		}
		byScope[r.Scope] = append(byScope[r.Scope], r)
	}
	var errs []error
	for scope, rs := range byScope {
		t, err := s.Resolver.Resolve(ctx, scope)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
		if err != nil {
			errs = append(errs, err) // fail closed: an unreadable list removes nothing
			continue
		}
		listed := map[string]github.Runner{}
		for _, g := range list {
			listed[g.Name] = g
		}
		for _, r := range rs {
			g, ok := listed[r.Name]
			if !ok {
				continue // not at GitHub: the idle-removal path cleans such a record
			}
			if r.GitHubRunnerID != 0 && r.GitHubRunnerID != g.ID {
				continue // same name, not the registration crsp made
			}
			if g.Busy || !strings.EqualFold(g.Status, "offline") {
				if g.Busy {
					res.LeftBusy = append(res.LeftBusy, r.Name)
				}
				continue
			}
			age := now.Sub(r.CreatedAt).Round(time.Hour)
			err := s.sweepOne(ctx, r, t, g.ID, actor, "runner.swept", fmt.Sprintf("%s, offline, record age %s", r.Name, age))
			switch {
			case errors.Is(err, ErrRunnerBusy):
				res.LeftBusy = append(res.LeftBusy, r.Name)
			case err != nil:
				res.Failed = append(res.Failed, r.Name)
				errs = append(errs, fmt.Errorf("sweep %s: %w", r.Name, err))
			default:
				res.Deleted = append(res.Deleted, r.Name)
			}
		}
	}
	return res, errors.Join(errs...)
}

// sweepable is the record half of the ownership test.
func sweepable(r Record, now time.Time, grace time.Duration) bool {
	if !r.Managed || r.Kind != "ephemeral" || !strings.HasPrefix(r.Name, SweepPrefix) {
		return false
	}
	switch r.State {
	case Creating, Busy, Draining, Updating, Removing:
		return false
	}
	return !r.CreatedAt.IsZero() && now.Sub(r.CreatedAt) >= grace
}

// sweepOne deregisters one offline registration, then cleans up what is left
// on the machine. The machine may be gone: then the clean-up error is noted
// and the record still goes, because the registration it stood for is gone.
func (s *Service) sweepOne(ctx context.Context, r Record, t Target, githubID int64, actor, action, detail string) error {
	err := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, githubID)
	switch {
	case errors.Is(err, github.ErrRunnerBusy):
		s.audit(ctx, actor, "runner.sweep_left_busy", r.MachineID, r.ID, r.Name+" (GitHub answered 422)")
		return ErrRunnerBusy
	case errors.Is(err, github.ErrNotFound):
		// Already gone at GitHub: only the local clean-up is left.
	case err != nil:
		return fmt.Errorf("deregister at GitHub: %w", err)
	}
	if err := s.Agent.StopService(ctx, r.MachineID, r.ServiceName); err != nil {
		detail += " (stop: " + err.Error() + ")"
	}
	if err := s.Agent.CleanUp(ctx, r.MachineID, r.ServiceName); err != nil {
		detail += " (clean up: " + err.Error() + ")"
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, action, r.MachineID, r.ID, detail)
	return nil
}
