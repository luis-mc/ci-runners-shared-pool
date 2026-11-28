package runners

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

// A runner GitHub no longer lists (its registration was deleted, for example
// after the runner was offline for a while) can never connect again by
// restarting. The controller registers it again by itself, once, with a fresh
// token, then says so on the Runners page. When that fails, or the registration
// is lost again at once, the runner shows failed with the reason and a button to
// register it again (runners R5.152, R5.153).
const (
	// ReregisteredReason is the reason of a runner that was registered again
	// after GitHub deleted its registration. It stays until the runner changes
	// state, so a second loss at once is not repaired in a loop.
	ReregisteredReason = "registration lost, registered again"
	// ReregisterFailedPrefix starts the failed reason of a runner whose
	// registration is lost and could not be made again.
	ReregisterFailedPrefix = "registration lost, could not register again: "
	// RegistrationLostReason is the shown reason of a runner GitHub does not list
	// while its listener says the registration was deleted.
	RegistrationLostReason = "GitHub deleted the registration of this runner"
	// SetUpAgainFailedPrefix starts the failed reason of a runner whose set-up
	// failed (or never finished) and that a new release could not set up again
	// either (CRSP385).
	SetUpAgainFailedPrefix = "could not be set up again: "
)

// RecoverResult says what one recovery pass did.
type RecoverResult struct {
	Registered []string          // names registered again
	Failed     []string          // names that could not be, now shown failed
	Left       []string          // names left alone (a job runs, GitHub unreadable)
	Why        map[string]string // the reason of each failed name, for the log
}

func (res *RecoverResult) fail(name, why string) {
	res.Failed = append(res.Failed, name)
	if res.Why == nil {
		res.Why = map[string]string{}
	}
	res.Why[name] = why
}

// releaseTag marks a reason the recovery wrote with the release that wrote it,
// so each release tries a failed runner once and never loops (CRSP385).
func (s *Service) releaseTag() string {
	v := s.Release
	if v == "" {
		v = "dev"
	}
	return "(crsp " + v + ")"
}

// retryable is a failed record a release sets up again once by itself
// (CRSP385): a runner whose register again failed, a runner whose set-up never
// finished, and a hidden registration of a pool runner whose add failed. The
// fault may have been a bug this release fixes, and the product does the work,
// not the owner. A reason this release wrote is not tried again by itself: the
// button Register again is left.
func (s *Service) retryable(r Record) bool {
	if !r.Managed || r.Kind != "persistent" || IsPoolRunner(r) || r.State != Failed || r.CurrentJob != "" {
		return false
	}
	if strings.Contains(r.Reason, s.releaseTag()) {
		return false
	}
	switch {
	case strings.HasPrefix(r.Reason, ReregisterFailedPrefix), strings.HasPrefix(r.Reason, SetUpAgainFailedPrefix),
		r.Reason == StuckCreatingReason:
		return true
	}
	return r.PoolRunnerID != ""
}

// recoverable is a record the recovery looks at: a persistent runner CRSP made
// that is not a pool runner itself (a pool runner has no registration; its
// hidden registrations are records of their own).
func recoverable(r Record) bool {
	if !r.Managed || r.Kind != "persistent" || IsPoolRunner(r) || r.ServiceName == "" {
		return false
	}
	switch r.State {
	case Idle, Busy, Offline:
		return true
	}
	return false
}

// RecoverLostRegistrations registers again, once, each persistent runner whose
// machine says its registration was deleted and that GitHub does not list. A
// runner that was already registered again and is lost again, or that cannot be
// registered, is set failed with the reason, so the supervisor's restart loop
// ends in a state the owner sees. Run it in the reconcile loop.
func (s *Service) RecoverLostRegistrations(ctx context.Context, actor string) (RecoverResult, error) {
	var res RecoverResult
	if s.Services == nil || s.Resolver == nil || s.GitHub == nil || s.Agent == nil {
		return res, nil
	}
	recs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return res, err
	}
	svc := map[string]map[string]backend.Found{}
	gh := map[string]ghList{}
	byID := map[string]Record{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	ghFor := func(r Record) ghList {
		key := fmt.Sprintf("%s|%d|%s|%s", r.Scope.Kind, r.Scope.RepositoryID, r.Scope.Organization, r.Scope.Group)
		l, seen := gh[key]
		if !seen {
			l = s.githubList(ctx, r.Scope)
			gh[key] = l
		}
		return l
	}
	for _, r := range recs {
		if s.retryable(r) {
			s.retryOnce(ctx, r, byID, ghFor(r), actor, &res)
			continue
		}
		if !recoverable(r) {
			continue
		}
		m, seen := svc[r.MachineID]
		if !seen {
			if found, err := s.Services(ctx, r.MachineID); err == nil {
				m = foundMap(found)
			}
			svc[r.MachineID] = m
		}
		f, ok := lookupFound(m, r)
		// A hidden registration of a pool runner that its machine does not list
		// at all (in a list that was read) cannot run either: it is set up again
		// like a lost one (CRSP385). Any other runner needs the machine to say
		// its registration is lost.
		gone := !ok && m != nil && r.PoolRunnerID != "" && r.State != Busy
		if !gone && (!ok || !f.RegistrationLost) {
			continue
		}
		l := ghFor(r)
		if !l.ok || listed(l, r) || r.CurrentJob != "" {
			res.Left = append(res.Left, r.Name)
			continue
		}
		if r.Reason == ReregisteredReason {
			why := "it was registered again and GitHub deleted the registration again"
			s.failReregister(ctx, r, ReregisterFailedPrefix, why, actor)
			res.fail(r.Name, why)
			continue
		}
		if err := s.reregister(ctx, r, actor); err != nil {
			s.failReregister(ctx, r, ReregisterFailedPrefix, err.Error(), actor)
			res.fail(r.Name, err.Error())
			continue
		}
		res.Registered = append(res.Registered, r.Name)
	}
	return res, nil
}

// retryOnce sets a retryable failed runner up again, once in this release. A
// runner GitHub lists, one GitHub cannot be read for, one the scope gate now
// refuses and a hidden registration whose pool runner is going away are left
// alone, and looked at again on a later pass.
func (s *Service) retryOnce(ctx context.Context, r Record, byID map[string]Record, l ghList, actor string, res *RecoverResult) {
	if r.PoolRunnerID != "" {
		p, ok := byID[r.PoolRunnerID]
		if !ok || p.State == Removing || p.State == Draining || p.State == Failed {
			res.Left = append(res.Left, r.Name)
			return
		}
	}
	if !l.ok || listed(l, r) {
		res.Left = append(res.Left, r.Name)
		return
	}
	if s.Gate != nil {
		if err := s.Gate(ctx, r.Scope); err != nil {
			res.Left = append(res.Left, r.Name)
			return
		}
	}
	prefix := SetUpAgainFailedPrefix
	if strings.HasPrefix(r.Reason, ReregisterFailedPrefix) {
		prefix = ReregisterFailedPrefix
	}
	if err := s.reregister(ctx, r, actor); err != nil {
		s.failReregister(ctx, r, prefix, err.Error(), actor)
		res.fail(r.Name, err.Error())
		return
	}
	res.Registered = append(res.Registered, r.Name)
}

// ReregisterRunner registers a runner again on the owner's request (the button
// Register again). A pool runner registers each of its hidden registrations
// that GitHub does not list. It is refused for a runner GitHub lists, one with
// a job and one CRSP did not make.
func (s *Service) ReregisterRunner(ctx context.Context, id, actor string) (View, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !r.Managed {
		return View{}, refuse("runner %s was not made by CRSP; crsp does not register it again", r.Name)
	}
	if r.Kind != "persistent" {
		return View{}, refuse("runner %s is made for one job and is never registered again", r.Name)
	}
	ms, err := s.members(ctx, r)
	if err != nil {
		return View{}, err
	}
	did := 0
	var errs []string
	for _, k := range ms {
		if IsPoolRunner(k) {
			continue
		}
		err := s.reregisterOnRequest(ctx, k, actor)
		var re *RefusalError
		switch {
		case err == nil:
			did++
		case errors.As(err, &re) && IsPoolRunner(r):
			// one registration of a pool runner that GitHub still lists
		default:
			errs = append(errs, err.Error())
		}
	}
	if did == 0 && len(errs) > 0 {
		return View{}, refuse("%s", strings.Join(errs, "; "))
	}
	if did == 0 {
		return View{}, refuse("GitHub lists every registration of runner %s; there is nothing to register again", r.Name)
	}
	return s.GetView(ctx, id)
}

func (s *Service) reregisterOnRequest(ctx context.Context, r Record, actor string) error {
	if r.CurrentJob != "" {
		return refuse("runner %s has a job; it is left alone", r.Name)
	}
	l := s.githubList(ctx, r.Scope)
	if !l.ok {
		return refuse("GitHub could not be read, so the registration of %s is not changed", r.Name)
	}
	if listed(l, r) {
		return refuse("GitHub lists runner %s; there is nothing to register again", r.Name)
	}
	if s.Gate != nil {
		if err := s.Gate(ctx, r.Scope); err != nil {
			return err
		}
	}
	if err := s.reregister(ctx, r, actor); err != nil {
		s.failReregister(ctx, r, ReregisterFailedPrefix, err.Error(), actor)
		return fmt.Errorf("%s: %w", r.Name, err)
	}
	return nil
}

// listed says whether GitHub lists the runner (by name or id): a registration
// that is listed is not lost, whatever its status.
func listed(l ghList, r Record) bool {
	for _, g := range l.list {
		if g.Name == r.Name || (r.GitHubRunnerID != 0 && g.ID == r.GitHubRunnerID) {
			return true
		}
	}
	return false
}

func foundMap(found []backend.Found) map[string]backend.Found {
	m := map[string]backend.Found{}
	for _, x := range found {
		m[x.ServiceName] = x
	}
	for _, x := range found {
		if _, dup := m[nameKey(x.Name)]; !dup && x.Name != "" {
			m[nameKey(x.Name)] = x
		}
	}
	return m
}

func lookupFound(m map[string]backend.Found, r Record) (backend.Found, bool) {
	if m == nil {
		return backend.Found{}, false
	}
	if x, ok := m[r.ServiceName]; ok {
		return x, true
	}
	if isContainerRef(r.ServiceName) {
		x, ok := m[nameKey(r.Name)]
		return x, ok
	}
	return backend.Found{}, false
}

// reregister makes one fresh registration token and has the agent register the
// runner again in place (the backend cleans the old files first, runs
// `config.sh --unattended --replace` and starts the runner). The token is used
// once and dropped (runners R6.2).
func (s *Service) reregister(ctx context.Context, r Record, actor string) error {
	pin, err := PinFor(r.OS, r.Arch)
	if err != nil {
		return err
	}
	sc, err := s.registrationScope(ctx, r.Scope)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	t, err := s.Resolver.Resolve(ctx, sc)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	a := AddSpec{Name: r.Name, MachineID: r.MachineID, OS: r.OS, Arch: r.Arch, Kind: r.Kind, Backend: r.Backend, Labels: r.Labels,
		Scope: sc, SizeClass: r.SizeClass, PoolRunnerID: r.PoolRunnerID, KeepDependencies: r.KeepDependencies}
	if s.Machines != nil {
		if m, err := s.Machines.GetMachine(ctx, r.MachineID); err == nil {
			a.Symlink, a.MaxParallel = m.Symlink, m.MaxParallel()
		}
	}
	token, err := s.GitHub.CreateRegistrationToken(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fmt.Errorf("registration token: %w", err)
	}
	// A runner whose files are gone from its machine (a failed add cleaned them
	// up) is set up whole by the same call, and a registration the call made
	// but could not finish is removed again, as for any add (CRSP385).
	before, listErr := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	h, err := s.Agent.AddRunner(ctx, r.MachineID, s.runnerSpec(ctx, r.ID, a, t, pin), backend.StartConfig{Registration: token})
	if h.Ref != "" && h.Ref != r.ServiceName {
		_ = s.Store.SetRunnerService(ctx, r.ID, h.Ref, AccountName(r.Name, nil))
	}
	if err != nil {
		return s.withRegistrationCleanup(ctx, t, r.Name, before, listErr, err)
	}
	if err := s.Store.SetRunnerState(ctx, r.ID, Idle, ReregisteredReason); err != nil {
		return err
	}
	s.audit(ctx, actor, "runner.reregistered", r.MachineID, r.ID, r.Name+": "+ReregisteredReason)
	if s.LogInfo != nil {
		s.LogInfo("runner " + r.Name + ": " + ReregisteredReason)
	}
	return nil
}

// failReregister shows the runner failed with the reason and writes it down.
// The reason ends with the release that tried, so that release never tries it
// again by itself and a newer one tries it once (CRSP385).
func (s *Service) failReregister(ctx context.Context, r Record, prefix, why, actor string) {
	reason := prefix + why + " " + s.releaseTag()
	if err := s.Store.SetRunnerState(ctx, r.ID, Failed, reason); err != nil {
		return
	}
	s.audit(ctx, actor, "runner.reregister_failed", r.MachineID, r.ID, r.Name+": "+reason)
}

// CanReregister says whether the Runners page offers Register again on a
// runner with this record and shown state: a persistent runner CRSP made that
// failed because its registration is lost or its set-up did not finish, a
// failed hidden registration of a pool runner, or one GitHub does not list.
func CanReregister(r Record, shown State, reason string) bool {
	if !r.Managed || r.Kind != "persistent" || r.CurrentJob != "" {
		return false
	}
	switch shown {
	case NotRegistered:
		return true
	case Failed:
		// a failed hidden registration of a pool runner is set up again by the
		// same press (CRSP385)
		return strings.HasPrefix(reason, ReregisterFailedPrefix) || strings.HasPrefix(reason, SetUpAgainFailedPrefix) ||
			reason == StuckCreatingReason || r.PoolRunnerID != ""
	}
	return false
}

// StuckCreatingReason is the failed reason of a runner whose set-up never
// finished: no plan of this controller runs, and its record is still creating.
const StuckCreatingReason = "its set-up did not finish; remove it and add it again"

// SettleStuckCreating ends the creating state of a runner whose set-up is not
// running any more (the controller stopped in the middle of it): a runner whose
// service runs and that GitHub lists is idle, any other one is failed with the
// reason. A runner younger than after, or any runner while a plan runs, is left
// alone. Run it in the reconcile loop (runners R5.153).
func (s *Service) SettleStuckCreating(ctx context.Context, after time.Duration, actor string) ([]string, error) {
	s.stopMu.Lock()
	busy := len(s.running) > 0
	s.stopMu.Unlock()
	if busy {
		return nil, nil
	}
	recs, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	var settled []string
	for _, r := range recs {
		if r.State != Creating || r.CreatedAt.IsZero() || s.now().Sub(r.CreatedAt) < after {
			continue
		}
		to, why := Failed, StuckCreatingReason
		if v := s.views(ctx, []Record{withState(r, Idle)}); len(v) == 1 && v[0].State == Idle {
			to, why = Idle, "registered"
		}
		if err := s.Store.SetRunnerState(ctx, r.ID, to, why); err != nil {
			continue
		}
		s.audit(ctx, actor, "runner.creating_settled", r.MachineID, r.ID, r.Name+": "+string(to)+", "+why)
		settled = append(settled, r.Name)
	}
	return settled, nil
}

func withState(r Record, st State) Record {
	r.State = st
	return r
}
