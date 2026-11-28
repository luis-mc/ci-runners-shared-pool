package runners

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

// MaxRegistrationsPerMachine is the number of GitHub registrations one machine
// may hold before the Doctor says so. GitHub limits the runners of a
// repository and of an organization, and each registration is a service and a
// folder on the machine (runners R5.88).
const MaxRegistrationsPerMachine = 20

// PoolRepo is a repository that uses the pool and the scope its runners
// register in.
type PoolRepo struct {
	ID       int64
	FullName string
	Scope    Scope // repository scope with RepositoryID set
}

// ServicePauser pauses and resumes the service of one registration. A paused
// registration takes no job, so the slot lock can keep one job on a machine
// slot at a time.
type ServicePauser interface {
	PauseService(ctx context.Context, machineID, serviceName string) error
	ResumeService(ctx context.Context, machineID, serviceName string) error
}

// IsPoolRunner reports whether a record is a pool runner: a machine slot with
// no registration of its own.
func IsPoolRunner(r Record) bool { return r.Scope.Kind == ScopePool }

// IsRegistration reports whether a record is the hidden registration of a pool
// runner for one repository (or one organization group).
func IsRegistration(r Record) bool { return r.PoolRunnerID != "" }

// regKey names where a registration lives, so one repository, or one
// organization group that serves many repositories, has one registration.
func regKey(sc Scope) string {
	if sc.Kind == "organization" {
		return "org|" + sc.Organization + "|" + sc.Group
	}
	return fmt.Sprintf("repo|%d", sc.RepositoryID)
}

func regName(parent string, sc Scope) string {
	if sc.Kind == "organization" {
		return parent + "-g-" + sc.Organization
	}
	return fmt.Sprintf("%s-r%d", parent, sc.RepositoryID)
}

// isolationOf names where a persistent runner lives: in a long-lived container
// when the container backend runs it (a Linux runner on a Windows machine,
// R5.93), as the machine's own account otherwise.
func isolationOf(a AddSpec) string {
	if a.Backend == string(backend.Docker) {
		return string(backend.IsolationContainer)
	}
	return string(backend.IsolationPersistent)
}

// addPoolRunner stores a pool runner. It has no GitHub registration: the pool
// reconcile makes one for each repository that uses the pool.
func (s *Service) addPoolRunner(ctx context.Context, a AddSpec, version, actor string) error {
	size, err := ParseSize(string(a.SizeClass))
	if err != nil {
		return refuse("%v", err)
	}
	rec := Record{ID: s.newID("r_"), Name: a.Name, MachineID: a.MachineID, OS: a.OS, Arch: a.Arch, Kind: a.Kind,
		Backend: a.Backend, Isolation: isolationOf(a), Labels: a.Labels, Route: RouteForLabels(s.PoolName, a.Labels),
		SizeClass: size, Scope: Scope{Kind: ScopePool}, RunnerVersion: version, State: Idle,
		Reason: poolIdleReason(a.Kind), Managed: true, CreatedAt: s.now(), KeepDependencies: a.KeepDependencies}
	if err := s.Store.InsertRunner(ctx, rec); err != nil {
		return err
	}
	s.audit(ctx, actor, "runner.pool_add", a.MachineID, rec.ID, a.Name)
	// The registrations are best effort: the pool runner exists either way and
	// the next reconcile tries again.
	if err := s.SyncPoolRegistrations(ctx, actor); err != nil {
		s.audit(ctx, actor, "runner.pool_sync_failed", a.MachineID, rec.ID, err.Error())
	}
	// The step ends with the real outcome of this runner's registrations (OPS60,
	// R5.101): one that failed fails the step with its reason, so Add runners
	// never says "added" for a runner that is Failed. The runner stays in the
	// list as Failed; the owner removes it.
	if all, err := s.Store.ListRunners(ctx, Filter{}); err == nil {
		for _, k := range all {
			if k.PoolRunnerID == rec.ID && k.State == Failed {
				return fmt.Errorf("runner %s was added but is failed: %s. It stays in Runners as failed; remove it there, fix the cause and add it again", a.Name, k.Reason)
			}
		}
	}
	return nil
}

// addPoolContainerRunner stores a Linux container runner of a Windows machine as a pool runner (R5.47, OPS27). It
// has no registration: the demand loop starts a container for each job that carries the pool label.
func (s *Service) addPoolContainerRunner(ctx context.Context, a AddSpec, actor string) error {
	size, err := ParseSize(string(a.SizeClass))
	if err != nil {
		return refuse("%v", err)
	}
	pin, err := PinFor("linux", a.Arch)
	if err != nil {
		return err
	}
	rec := Record{ID: s.newID("r_"), Name: a.Name, MachineID: a.MachineID, OS: a.OS, Arch: a.Arch, Kind: "ephemeral",
		Backend: a.Backend, Isolation: string(backend.IsolationContainer), Labels: a.Labels, Route: RouteForLabels(s.PoolName, a.Labels),
		SizeClass: size, Scope: Scope{Kind: ScopePool}, RunnerVersion: pin.Version, State: Idle,
		Reason: "a container starts for each job of the pool", Managed: true, CreatedAt: s.now()}
	if err := s.Store.InsertRunner(ctx, rec); err != nil {
		return err
	}
	s.audit(ctx, actor, "runner.pool_add", a.MachineID, rec.ID, a.Name)
	return nil
}

// SyncPoolRegistrations keeps one hidden GitHub registration for each pool
// runner and each repository that uses the pool: it adds the missing ones,
// removes those of a repository that stopped using the pool after a drain (the
// runner finishes its job first, nothing is stopped), migrates repository
// runners, and applies the slot lock. Run it when the pool turns on or off for
// a repository, when a pool runner is added, and in the reconcile loop.
func (s *Service) SyncPoolRegistrations(ctx context.Context, actor string) error {
	if s.PoolRepos == nil {
		return nil
	}
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	repos, err := s.PoolRepos(ctx)
	if err != nil {
		return fmt.Errorf("the repositories that use the pool cannot be read, so no registration changes: %w", err)
	}
	var errs []error
	if err := s.migrateToPool(ctx, actor); err != nil {
		errs = append(errs, err)
	}
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	children := map[string][]Record{}
	for _, r := range all {
		if r.PoolRunnerID != "" {
			children[r.PoolRunnerID] = append(children[r.PoolRunnerID], r)
		}
	}
	type want struct {
		repo  PoolRepo
		scope Scope
	}
	wanted := map[string]want{}
	for _, pr := range repos {
		sc, err := s.registrationScope(ctx, pr.Scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve scope of %s: %w", pr.FullName, err))
			continue
		}
		if k := regKey(sc); wanted[k].scope.Kind == "" {
			wanted[k] = want{repo: pr, scope: sc}
		}
	}
	for _, p := range all {
		if !IsPoolRunner(p) || p.Kind == "ephemeral" || !p.Managed || p.State == Failed || p.State == Removing || p.State == Draining {
			continue
		}
		have := map[string]Record{}
		for _, c := range children[p.ID] {
			have[regKey(c.Scope)] = c
		}
		keys := make([]string, 0, len(wanted))
		for k := range wanted {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := have[k]; ok {
				// A failed registration stays: the pool runner shows Failed with the
				// reason, and only the owner removes it. It is never removed or
				// retried by the reconcile (OPS60, R5.101): each retry of a failed
				// image build costs minutes and hid the reason.
				continue
			}
			if err := s.addRegistration(ctx, p, wanted[k].repo, actor); err != nil {
				errs = append(errs, fmt.Errorf("registration of %s for %s: %w", p.Name, wanted[k].repo.FullName, err))
			}
		}
		for k, c := range have {
			if _, ok := wanted[k]; ok {
				continue
			}
			s.startDrainRemove(ctx, c, actor)
		}
	}
	if err := s.enforceSlotLocks(ctx, all, children, actor); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) addRegistration(ctx context.Context, parent Record, repo PoolRepo, actor string) error {
	if s.Gate != nil {
		if err := s.Gate(ctx, repo.Scope); err != nil {
			return err
		}
	}
	m, err := s.Machines.GetMachine(ctx, parent.MachineID)
	if err != nil {
		return err
	}
	sc, err := s.registrationScope(ctx, repo.Scope)
	if err != nil {
		return err
	}
	return s.addRunner(ctx, AddSpec{Name: regName(parent.Name, sc), MachineID: parent.MachineID, OS: parent.OS, Arch: parent.Arch,
		Kind: "persistent", Backend: parent.Backend, Labels: parent.Labels, Scope: repo.Scope, Symlink: m.Symlink,
		SizeClass: parent.SizeClass, MaxParallel: m.MaxParallel(), PoolRunnerID: parent.ID, KeepDependencies: parent.KeepDependencies}, actor)
}

// startDrainRemove removes a registration after its job ends, in the
// background, with the drain rules: the idle check runs right before the
// removal and a running job is never stopped (runners R5.4).
func (s *Service) startDrainRemove(ctx context.Context, c Record, actor string) {
	s.slotMu.Lock()
	if s.draining == nil {
		s.draining = map[string]bool{}
	}
	already := s.draining[c.ID]
	s.draining[c.ID] = true
	s.slotMu.Unlock()
	if already {
		return
	}
	prev := c.State
	if CanTransition(c.State, Draining) {
		_ = s.Store.SetRunnerState(ctx, c.ID, Draining, "the repository stopped using the pool")
	}
	bg := context.WithoutCancel(ctx)
	s.background(func() {
		err := s.finishRemoveRegistration(bg, c, prev, actor)
		s.slotMu.Lock() // Background may run inline, inside the sync's own lock
		delete(s.draining, c.ID)
		s.slotMu.Unlock()
		if err != nil {
			s.audit(bg, actor, "runner.pool_remove_failed", c.MachineID, c.ID, err.Error())
		}
	})
}

func (s *Service) finishRemoveRegistration(ctx context.Context, c Record, prev State, actor string) error {
	if s.Pauser != nil && s.isPaused(c.ID) { // a paused service must run to deregister
		_ = s.Pauser.ResumeService(ctx, c.MachineID, c.ServiceName)
		s.setPaused(c.ID, false)
	}
	err := s.finishRemove(ctx, c, prev, actor)
	if err == nil {
		s.audit(ctx, actor, "runner.pool_registration_removed", c.MachineID, c.ID, c.Name)
	}
	return err
}

// beginRemovePool removes a pool runner: first every registration (each after
// its job), then the record. It asks for a yes that names them.
func (s *Service) beginRemovePool(ctx context.Context, r Record, confirm bool, actor string) (Record, func(context.Context) error, error) {
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return r, nil, err
	}
	var kids []Record
	for _, c := range all {
		if c.PoolRunnerID == r.ID {
			kids = append(kids, c)
		}
	}
	if !confirm {
		items := []ConfirmItem{{"runner", "The pool runner " + r.Name + " on its machine"}}
		for _, c := range kids {
			items = append(items, ConfirmItem{"registration", "The registration " + c.Name + " on GitHub, its service and its folder"})
			if c.CurrentJob != "" {
				items = append(items, ConfirmItem{"job", "Waits for job " + c.CurrentJob})
			}
		}
		return r, nil, &NeedsConfirmation{Items: items}
	}
	if CanTransition(r.State, Draining) {
		if err := s.Store.SetRunnerState(ctx, r.ID, Draining, "remove asked"); err != nil {
			return r, nil, err
		}
		r.State = Draining
	}
	finish := func(ctx context.Context) error {
		var errs []error
		for _, c := range kids {
			if err := s.finishRemoveRegistration(ctx, c, c.State, actor); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			_ = s.Store.SetRunnerState(ctx, r.ID, Idle, "a registration could not be removed; the pool runner stays")
			return errors.Join(errs...)
		}
		if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
			return err
		}
		s.audit(ctx, actor, "runner.pool_remove", r.MachineID, r.ID, r.Name)
		return nil
	}
	return r, finish, nil
}

// migrateToPool turns each repository-scoped persistent runner that crsp made
// into a pool runner whose first registration is the one it already has, so
// nothing is registered again and no job is cut. Light runners are bound to
// their repository by design and stay as they are. Run on every sync; it does
// nothing once every runner is migrated.
func (s *Service) migrateToPool(ctx context.Context, actor string) error {
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range all {
		if !r.Managed || r.Kind != "persistent" || IsPoolRunner(r) || IsRegistration(r) || IsLegacyLight(r) ||
			(r.Scope.Kind != "repository" && r.Scope.Kind != "organization") {
			continue
		}
		m, err := s.Machines.GetMachine(ctx, r.MachineID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name, err := s.freeName(ctx, m, map[string]bool{}, "-pool")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		parent := Record{ID: s.newID("r_"), Name: name, MachineID: r.MachineID, OS: r.OS, Arch: r.Arch, Kind: r.Kind,
			Backend: r.Backend, Isolation: r.Isolation, Labels: r.Labels, Route: r.Route, SizeClass: r.SizeClass,
			Scope: Scope{Kind: ScopePool}, RunnerVersion: r.RunnerVersion, State: Idle, Managed: true, CreatedAt: r.CreatedAt,
			KeepDependencies: r.KeepDependencies}
		if err := s.Store.InsertRunner(ctx, parent); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.Store.SetRunnerPool(ctx, r.ID, parent.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		s.audit(ctx, actor, "runner.pool_migrated", r.MachineID, r.ID, r.Name+" is now a registration of pool runner "+name)
	}
	return errors.Join(errs...)
}

// enforceSlotLocks keeps one job on a machine slot at a time. While one
// registration of a pool runner has a job, the others are paused; when the job
// ends they are resumed. Without a Pauser the lock can only report: two busy
// registrations of one runner are written to the audit log.
func (s *Service) enforceSlotLocks(ctx context.Context, all []Record, children map[string][]Record, actor string) error {
	lists := map[string]ghList{}
	var errs []error
	for _, p := range all {
		if !IsPoolRunner(p) {
			continue
		}
		var busy []Record
		var live []Record
		for _, c := range children[p.ID] {
			if c.State == Draining || c.State == Removing || c.State == Failed || c.State == Creating {
				continue
			}
			key := regKey(c.Scope)
			l, ok := lists[key]
			if !ok {
				l = s.githubList(ctx, c.Scope)
				lists[key] = l
			}
			if !l.ok {
				live = nil // fail closed: no change to any registration of this runner
				busy = nil
				break
			}
			live = append(live, c)
			for _, g := range l.list {
				if g.Name == c.Name && g.Busy {
					busy = append(busy, c)
				}
			}
		}
		switch {
		case len(busy) > 1:
			s.audit(ctx, actor, "pool.slot_conflict", p.MachineID, p.ID, fmt.Sprintf("%d registrations of %s have a job at once", len(busy), p.Name))
		case len(busy) == 1 && s.Pauser != nil:
			for _, c := range live {
				if c.ID == busy[0].ID || s.isPaused(c.ID) || c.ServiceName == "" {
					continue
				}
				if err := s.Pauser.PauseService(ctx, c.MachineID, c.ServiceName); err != nil {
					errs = append(errs, fmt.Errorf("pause %s: %w", c.Name, err))
					continue
				}
				s.setPaused(c.ID, true)
			}
		case len(busy) == 0 && s.Pauser != nil:
			for _, c := range live {
				if !s.isPaused(c.ID) {
					continue
				}
				if err := s.Pauser.ResumeService(ctx, c.MachineID, c.ServiceName); err != nil {
					errs = append(errs, fmt.Errorf("resume %s: %w", c.Name, err))
					continue
				}
				s.setPaused(c.ID, false)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) isPaused(id string) bool {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()
	return s.paused[id]
}

func (s *Service) setPaused(id string, on bool) {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()
	if s.paused == nil {
		s.paused = map[string]bool{}
	}
	if on {
		s.paused[id] = true
		return
	}
	delete(s.paused, id)
}

// poolView folds the registrations of a pool runner into its view: the state
// it shows, the repository of the job it serves and how many registrations
// it holds.
func poolView(p Record, kids []View, names map[int64]string, machine string) View {
	v := poolViewBase(p, kids, names, machine)
	if p.UncleanReason != "" {
		v.Reason = "needs clean-up, takes no new job: " + p.UncleanReason
	}
	return v
}

func poolViewBase(p Record, kids []View, names map[int64]string, machine string) View {
	v := View{Record: p, State: Idle, Reason: poolIdleReason(p.Kind), MachineName: machine, Registrations: len(kids)}
	if len(kids) == 0 {
		return v
	}
	v.Reason = ""
	rank := func(st State) int {
		switch st {
		case Busy:
			return 0
		case Idle:
			return 1
		}
		return 2
	}
	best := kids[0]
	for _, k := range kids {
		if rank(k.State) < rank(best.State) {
			best = k
		}
	}
	v.State, v.Reason = best.State, best.Reason
	for _, k := range kids {
		v.CanReregister = v.CanReregister || k.CanReregister
		v.CanStart = v.CanStart || k.CanStart
	}
	if best.State == Busy {
		v.Serving = names[best.Scope.RepositoryID]
		if v.Serving == "" {
			v.Serving = best.Scope.Organization
		}
		v.CurrentJob = best.CurrentJob
	}
	return v
}

// poolIdleReason is why a pool runner with no registration is idle. An
// ephemeral pool runner is a slot: it holds no hidden registration, because
// each job gets its own one-job runner with a JIT config, so "no repository
// uses the pool yet" would be wrong for it (OPS89).
func poolIdleReason(kind string) string {
	if kind == "ephemeral" {
		return "a container starts for each job of the pool"
	}
	return "no repository uses the pool yet"
}
