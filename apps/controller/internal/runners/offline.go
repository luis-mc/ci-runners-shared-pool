package runners

import (
	"context"
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// DeleteOfflineRegistration deletes the registration of an offline runner
// whose agent cannot run the remove step, for example a machine that is gone
// (github-integration R5.33, the yes path).
//
// Without confirm it returns NeedsConfirmation naming the runner and what goes.
// With it, it asks GitHub right now: only a registration crsp made, listed as
// offline and not busy, is deleted with `DELETE .../actions/runners/{id}`. A
// runner that is online is refused (use remove, which drains), a busy one is
// left alone, and an unreadable list changes nothing. The clean-up on the
// machine is tried after the delete; a machine that is gone does not keep the
// record alive.
func (s *Service) DeleteOfflineRegistration(ctx context.Context, id string, confirm bool, actor string) (Record, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if !r.Managed {
		return r, refuse("runner %s was not made by CRSP; crsp does not delete its registration", r.Name)
	}
	if IsPoolRunner(r) {
		return s.deleteOfflinePoolRunner(ctx, r, confirm, actor)
	}
	t, g, err := s.checkOfflineRegistration(ctx, r)
	if err != nil {
		return r, err
	}
	if !confirm {
		return r, &NeedsConfirmation{Items: []ConfirmItem{
			{"registration", "The registration of offline runner " + r.Name + " on GitHub"},
			{"record", "The record of runner " + r.Name + " in crsp"},
			{"folder", "The runner folder " + r.Folder + ", if its machine answers"},
		}}
	}
	err = s.sweepOne(ctx, r, t, g.ID, actor, "runner.delete_offline", r.Name+", offline, deleted after a yes")
	if errors.Is(err, ErrRunnerBusy) {
		return r, refuse("GitHub says runner %s has a job; it is left alone", r.Name)
	}
	return r, err
}

// checkOfflineRegistration resolves where a registration lives and checks that
// GitHub lists it as the one crsp made, offline and not busy. An unreadable
// list is returned as an error and changes nothing (fail closed).
func (s *Service) checkOfflineRegistration(ctx context.Context, r Record) (Target, *github.Runner, error) {
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return t, nil, err
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return t, nil, err
	}
	var g *github.Runner
	for i := range list {
		if list[i].Name == r.Name {
			g = &list[i]
		}
	}
	switch {
	case g == nil:
		return t, nil, refuse("GitHub does not list runner %s, so there is no registration to delete", r.Name)
	case r.GitHubRunnerID != 0 && r.GitHubRunnerID != g.ID:
		return t, nil, refuse("the runner named %s at GitHub is not the registration crsp made", r.Name)
	case g.Busy:
		return t, nil, refuse("GitHub says runner %s has a job; it is left alone", r.Name)
	case !strings.EqualFold(g.Status, "offline"):
		return t, nil, refuse("runner %s is online; remove it instead, which waits for its job", r.Name)
	}
	return t, g, nil
}

// deleteOfflinePoolRunner deletes a pool runner (R5.88): a pool runner has no
// GitHub registration of its own, so each of its hidden per-repository
// registrations is checked first (offline, not busy, the one crsp made; one
// that fails any check refuses the whole delete before anything changes), the
// confirmation lists them, then each registration is deleted at GitHub and
// the pool runner's own record goes last. A registration GitHub no longer
// lists is already gone, so only its record is cleaned.
func (s *Service) deleteOfflinePoolRunner(ctx context.Context, r Record, confirm bool, actor string) (Record, error) {
	ms, err := s.members(ctx, r)
	if err != nil {
		return r, err
	}
	type step struct {
		rec Record
		t   Target
		id  int64 // 0 when GitHub no longer lists it
	}
	var steps []step
	items := []ConfirmItem{{"record", "The record of pool runner " + r.Name + " in crsp"}}
	for _, k := range ms {
		if k.ID == r.ID {
			continue
		}
		if !k.Managed {
			return r, refuse("registration %s was not made by CRSP; crsp does not delete it", k.Name)
		}
		t, g, err := s.checkOfflineRegistration(ctx, k)
		var re *RefusalError
		switch {
		case err == nil:
			steps = append(steps, step{k, t, g.ID})
			items = append(items, ConfirmItem{"registration", "The registration " + k.Name + " on GitHub, offline"})
		case errors.As(err, &re) && g == nil && strings.Contains(re.Reason, "does not list"):
			steps = append(steps, step{k, t, 0})
			items = append(items, ConfirmItem{"registration", "The record of registration " + k.Name + " (GitHub no longer lists it)"})
		default:
			return r, err // refused, or unreadable: nothing changes
		}
	}
	if !confirm {
		return r, &NeedsConfirmation{Items: items}
	}
	for _, st := range steps {
		if st.id == 0 {
			if err := s.Store.DeleteRunner(ctx, st.rec.ID); err != nil {
				return r, err
			}
			s.audit(ctx, actor, "runner.delete_offline", st.rec.MachineID, st.rec.ID, st.rec.Name+", registration of "+r.Name+", already gone at GitHub")
			continue
		}
		err := s.sweepOne(ctx, st.rec, st.t, st.id, actor, "runner.delete_offline", st.rec.Name+", registration of "+r.Name+", offline, deleted after a yes")
		if errors.Is(err, ErrRunnerBusy) {
			return r, refuse("GitHub says registration %s has a job; it is left alone", st.rec.Name)
		}
		if err != nil {
			return r, err
		}
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return r, err
	}
	s.audit(ctx, actor, "runner.delete_offline", r.MachineID, r.ID, r.Name+", pool runner, deleted after a yes")
	return r, nil
}
