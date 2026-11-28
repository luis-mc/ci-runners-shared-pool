package runners

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// Service plans, applies and removes runners on the controller side.
// It reaches machines only through Agent, GitHub only through GitHub, and the
// database only through Store.
type Service struct {
	Store    Store
	Machines Machines
	Resolver Resolver
	GitHub   GitHub
	Agent    Agent
	Audit    Auditor
	// PoolName is the one pool label a runner may carry.
	PoolName string
	// SharedToolsRoot is the folder on a machine that holds the shared tool
	// folders of each repository (caches R5.57). Empty takes the default of
	// the machine's OS (DefaultSharedToolsRoot).
	SharedToolsRoot string
	// NoSharedTools keeps every runner's own folders (the CRSP_NO_SHARED_TOOLS
	// environment value, caches R5.57).
	NoSharedTools bool
	// PackageProxy gives a persistent runner of one repository the package
	// proxy key and token while the proxy is on (caches R5.76); nil gives none.
	PackageProxy func(ctx context.Context, githubURL string) lancache.ProxyGrant
	// CacheQuota is the declared cache sizing every new runner gets (runners
	// R5.39); a zero value takes the defaults.
	CacheQuota runnercache.Quota
	// PoolSettings reads what a repository's pool asks for: the image layers
	// and the database it declares (runners R5.50, R5.54). Nil means the base
	// image and no database.
	PoolSettings func(ctx context.Context, scope Scope) (backend.PoolSetup, error)
	// Gate refuses adding a runner for a scope before anything is planned or
	// started: a public repository while the setting Public repositories is
	// off (github-integration R5.55). The error it returns reaches the caller
	// as it is. Nil allows every scope.
	Gate func(ctx context.Context, scope Scope) error
	// DefaultScope picks the scope of a runner plan that names none: the first
	// repository with the pool on, until every pool repository gets its own
	// registration (OPS23). Nil, or an error, refuses such a plan.
	DefaultScope func(ctx context.Context) (Scope, error)
	// Drain settings; the idle check and clock are replaceable for tests.
	Poll, WarnAfter, DrainTimeout time.Duration
	Sleep                         func(ctx context.Context, d time.Duration) error
	Now                           func() time.Time
	NewID                         func() string
	// OnDrainWarn is called once at the warn time with the blocking job.
	OnDrainWarn func(runnerID, job string)
	// OnDrainStep gets each drain decision before the slow step it leads to
	// (scheduling R5.63).
	OnDrainStep func(runnerID, line string)
	// Background runs long work (a plan apply, a drain) off the request. The
	// default is a goroutine; tests run it inline.
	Background func(func())

	// OnProgress, when set, is called with the plan after each step and when
	// the plan ends or is stopped (the plan.progress event).
	OnProgress func(Plan)

	// Updater replaces the runner files of a persistent runner (runners
	// R5.38). Nil means the machine's agent cannot update yet.
	Updater RunnerUpdater
	// AutoUpdate reads the owner setting for automatic updates (operations
	// R5.15); nil means on.
	AutoUpdate func(ctx context.Context) bool

	// Release is the crsp version this controller runs. The recovery writes it
	// into a failed reason, so each release tries a failed runner once by
	// itself (CRSP385). Empty reads as "dev".
	Release string

	// LogInfo writes one INFO line; nil writes nothing.
	LogInfo func(line string)
	// Services lists the OS services of a machine, from its agent. Nil means
	// the service state is not known, so no runner shows `stopped` (UX-19).
	Services func(ctx context.Context, machineID string) ([]backend.Found, error)

	// PoolRepos lists the repositories that use the pool, with the scope each
	// registers in. Nil means no pool runner gets a registration (runners R5.88).
	PoolRepos func(ctx context.Context) ([]PoolRepo, error)
	// Pauser pauses and resumes the service of a hidden registration for the
	// slot lock. Nil means the lock only watches: it reports two busy
	// registrations of one runner but cannot stop the second one.
	Pauser ServicePauser

	poolMu   sync.Mutex // one pool reconcile at a time
	slotMu   sync.Mutex // guards draining and paused
	draining map[string]bool
	paused   map[string]bool

	hygMu        sync.Mutex      // guards clearPending
	clearPending map[string]bool // machine|runner name -> a cache clear was asked

	legacyMu    sync.Mutex     // guards legacyTries
	legacyTries map[string]int // runner id -> failed tries to remove a light runner left by an older version

	mu      sync.Mutex // one plan apply at a time
	lightMu sync.Mutex // one light-runner removal pass at a time

	stopMu  sync.Mutex
	stops   map[string]bool // plan id -> a stop was asked while it applies
	running map[string]bool // plan id -> Apply is running it in this process
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) newID(prefix string) string {
	if s.NewID != nil {
		return prefix + s.NewID()
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

func (s *Service) audit(ctx context.Context, actor, action, machineID, subject, detail string) {
	if s.Audit != nil {
		s.Audit.Audit(ctx, actor, action, machineID, subject, detail)
	}
}

// AddRequest is the body of planRunners.
type AddRequest struct {
	OS        string
	Kind      string
	MachineID string
	Count     int
	Labels    []string
	Scope     Scope
	// PoolLevel is set when the request named no scope and the service chose
	// one; the runners then carry the pool label too.
	PoolLevel bool
	// KeepDependencies is the persistent runner's setting (runners R5.90); nil
	// means on. It is ignored for an ephemeral runner.
	KeepDependencies *bool
}

// Empty reports that the scope names nothing.
func (s Scope) Empty() bool {
	return s.Kind == "" && s.RepositoryID == 0 && s.Organization == ""
}

var nonSlug = strings.NewReplacer(" ", "-", "_", "-", ".", "-")

// chooseBackend finds an allowed, usable backend for the OS and kind (R5.1).
// A Windows machine whose container engine runs Linux containers (Docker
// Desktop on WSL2) also takes Linux runners, in the container backend and
// ephemeral only (R5.47); since OPS28 it also takes persistent Linux runners in
// a long-lived container (R5.93).
func chooseBackend(m Machine, os, kind string) (BackendOption, error) {
	if m.OS == os {
		for _, b := range m.Backends {
			if b.Usable && b.Allowed && contains(b.Kinds, kind) && contains(b.OS, os) {
				return b, nil
			}
		}
	}
	if crossesToLinuxContainers(m, os) {
		// Both kinds run in the container backend here. A persistent one is a
		// long-lived container the agent keeps; the backend need not list the
		// kind for it (R5.93).
		for _, b := range m.Backends {
			if b.Name == backend.Docker && b.Usable && b.Allowed && contains(b.OS, os) && (kind == "persistent" || contains(b.Kinds, kind)) {
				return b, nil
			}
		}
		return BackendOption{}, refuse("machine %s: Linux containers run here, but the container backend is not allowed on this machine (it is off by default)", m.Name)
	}
	return BackendOption{}, refuse("machine %s: no allowed backend for %s", m.Name, OSName(os))
}

// crossesToLinuxContainers says a Linux runner is asked of a Windows machine
// whose engine runs Linux containers.
func crossesToLinuxContainers(m Machine, os string) bool {
	return os == "linux" && m.OS == "windows" && m.ContainerOS == "linux"
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// nameInfix is the part of a default name between the machine and the number. A runner of the pool carries its
// system, so a Windows runner is never given the name a removed Linux runner had (OPS87): box-pool-1 for Linux,
// box-win-1 for Windows, box-mac-1 for macOS. A runner of one repository has none. Only new names change.
func nameInfix(sc Scope, os string) string {
	if sc.Kind != ScopePool {
		return ""
	}
	switch os {
	case "windows":
		return "-win"
	case "macos":
		return "-mac"
	}
	return "-pool"
}

func (s *Service) freeName(ctx context.Context, m Machine, taken map[string]bool, infix string) (string, error) {
	existing, err := s.Store.ListRunners(ctx, Filter{MachineID: m.ID})
	if err != nil {
		return "", err
	}
	for _, r := range existing {
		taken[r.Name] = true
	}
	base := strings.Trim(strings.ToLower(nonSlug.Replace(m.Name)), "-")
	if base == "" {
		base = "runner"
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s%s-%d", base, infix, n)
		if !taken[name] {
			taken[name] = true
			return name, nil
		}
	}
}

// PlanAdd plans adding runners on a machine (runners R5.1, R5.36). It refuses a
// machine without an allowed backend for the OS and kind, and a label that is
// not generic, with the reason. Nothing changes until the plan is applied.
func (s *Service) PlanAdd(ctx context.Context, req AddRequest, actor string) (Plan, error) {
	if req.Count < 1 || req.Count > 64 {
		return Plan{}, refuse("count must be from 1 to 64")
	}
	if req.Scope.Empty() {
		// The user asked for runners of the pool, not of one repository.
		if s.DefaultScope == nil {
			return Plan{}, ErrNoPoolRepository
		}
		sc, err := s.DefaultScope(ctx)
		if err != nil {
			return Plan{}, err
		}
		req.Scope, req.PoolLevel = sc, true
	}
	if s.Gate != nil && req.Scope.Kind != ScopePool { // a pool runner has no repository to gate
		if err := s.Gate(ctx, req.Scope); err != nil {
			return Plan{}, err
		}
	}
	m, err := s.Machines.GetMachine(ctx, req.MachineID)
	if err != nil {
		return Plan{}, err
	}
	if err := s.checkSlotCap(ctx, m, req); err != nil {
		return Plan{}, err
	}
	specs, err := s.addSpecs(ctx, m, req)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{ID: s.newID("p_"), Area: "runners", State: PlanPlanned, CreatedAt: s.now(), CreatedBy: actor, Adds: specs}
	for i, a := range specs {
		p.Steps = append(p.Steps, Step{
			ID: fmt.Sprintf("s%d", i+1), Action: ActAddRunner, Target: a.Name, State: "planned",
			Text: addText(a, m.Name),
		})
	}
	return p, s.Store.SavePlan(ctx, p)
}

func addText(a AddSpec, machine string) string {
	if a.Kind == "ephemeral" {
		return fmt.Sprintf("Add ephemeral runner %s on machine %s (%s, in a container, takes one job and is removed)", a.Name, machine, OSName(a.OS))
	}
	if a.Scope.Kind == ScopePool {
		// A pool runner has no account of its own: each repository that uses
		// the pool gets a registration (regName) with its own service,
		// account and folder, so the parent's name would name an account that
		// is never made (OPS88).
		return fmt.Sprintf("Add %s runner %s on machine %s (%s, keeps state between jobs, one own account for each repository that uses the pool)",
			a.Kind, a.Name, machine, OSName(a.OS))
	}
	return fmt.Sprintf("Add %s runner %s on machine %s (%s, keeps state between jobs, own account %s)",
		a.Kind, a.Name, machine, OSName(a.OS), AccountName(a.Name, nil))
}

func (s *Service) addSpecs(ctx context.Context, m Machine, req AddRequest) ([]AddSpec, error) {
	if req.Kind != "persistent" && req.Kind != "ephemeral" {
		return nil, refuse("machine %s: %s runners are not built yet", m.Name, req.Kind)
	}
	if req.Kind == "ephemeral" && req.OS != "linux" {
		return nil, refuse("machine %s: ephemeral runners are fresh Linux containers, so %s cannot have them; add a persistent runner instead", m.Name, OSName(req.OS))
	}
	be, err := chooseBackend(m, req.OS, req.Kind)
	if err != nil {
		if req.Kind == "ephemeral" {
			// Ephemeral runners run in containers and only on a machine whose
			// allow list names the container backend (runners R5.27).
			return nil, refuse("%v; ephemeral runners need the container backend allowed on the machine (it is off by default)", err)
		}
		return nil, err
	}
	// An ephemeral pool request sets the ceiling of ephemeral runners at once on the machine (R5.92).
	for _, l := range req.Labels {
		if req.Kind == "persistent" && strings.TrimSpace(l) == EphemeralLabel {
			return nil, refuse("label %q is carried by ephemeral runners only; a persistent runner does not take it, so jobs that ask for ephemeral runners never reach it", EphemeralLabel)
		}
	}
	verified := map[string]bool{}
	for _, v := range be.Verified {
		verified[v] = true
	}
	want := append([]string{"self-hosted", req.OS, m.Arch}, req.Labels...)
	if (req.PoolLevel || crossesToLinuxContainers(m, req.OS)) && s.PoolName != "" {
		// A container runner on a Windows machine takes the pool's jobs
		// (R5.47), so it carries the pool label.
		want = append(want, s.PoolName)
	}
	if req.Kind == "ephemeral" {
		want = append(want, EphemeralLabel)
	}
	labels, err := ValidateLabels(want, s.PoolName, verified)
	if err != nil {
		return nil, refuse("%v", err)
	}
	keep := req.KeepDependencies == nil || *req.KeepDependencies
	taken := map[string]bool{}
	var out []AddSpec
	for i := 0; i < req.Count; i++ {
		name, err := s.freeName(ctx, m, taken, nameInfix(req.Scope, req.OS))
		if err != nil {
			return nil, err
		}
		out = append(out, AddSpec{Name: name, MachineID: m.ID, OS: req.OS, Arch: m.Arch, Kind: req.Kind,
			Backend: string(be.Name), Labels: labels, Scope: req.Scope, Symlink: m.Symlink, MaxParallel: m.MaxParallel(),
			KeepDependencies: keep && req.Kind == "persistent"})
	}
	return out, nil
}

// PlanReplacement plans replacing a runner CRSP did not make (runners R5.6;
// owner decision for M1: nothing changes silently). The new runner comes first;
// then one step per removal, each with its own yes, each waiting until the new
// runner is online, the runner list can be read and the old runner has no job.
func (s *Service) PlanReplacement(ctx context.Context, runnerID, actor string) (Plan, error) {
	old, err := s.find(ctx, runnerID)
	if err != nil {
		return Plan{}, err
	}
	if old.Managed {
		return Plan{}, refuse("runner %s was made by CRSP; remove it instead of replacing it", old.Name)
	}
	m, err := s.Machines.GetMachine(ctx, old.MachineID)
	if err != nil {
		return Plan{}, err
	}
	specs, err := s.addSpecs(ctx, m, AddRequest{OS: old.OS, Kind: "persistent", MachineID: m.ID, Count: 1, Scope: old.Scope})
	if err != nil {
		return Plan{}, err
	}
	n := specs[0]
	p := Plan{ID: s.newID("p_"), Area: "runners", State: PlanPlanned, CreatedAt: s.now(), CreatedBy: actor, Adds: specs, OldRunnerID: old.ID}
	p.Steps = []Step{
		{ID: "s1", Action: ActAddRunner, Target: n.Name, State: "planned",
			Text: fmt.Sprintf("Add persistent runner %s on machine %s", n.Name, m.Name)},
		{ID: "s2", Action: ActStopOldService, Target: old.ServiceName, NeedsYes: true, WaitsFor: "s1", State: "planned",
			Text: fmt.Sprintf("Stop the old service %s on machine %s", old.ServiceName, m.Name)},
		{ID: "s3", Action: ActRemoveOldRunner, Target: old.Name, NeedsYes: true, WaitsFor: "s2", State: "planned",
			Text: fmt.Sprintf("Remove the old runner %s from GitHub (with a remove token)", old.Name)},
		{ID: "s4", Action: ActDeleteOldFolder, Target: old.ServiceName, NeedsYes: true, WaitsFor: "s3", State: "planned",
			Text: fmt.Sprintf("Delete the old service entry, its account and the runner folder %s on machine %s", old.Folder, m.Name)},
	}
	return p, s.Store.SavePlan(ctx, p)
}

// GetPlan returns a plan with its outcome.
func (s *Service) GetPlan(ctx context.Context, id string) (Plan, error) {
	return s.Store.GetPlan(ctx, id)
}

// Get returns one runner.
func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	return s.Store.GetRunner(ctx, id)
}

// List returns runners, managed or not (runners R5.2, R5.5).
func (s *Service) List(ctx context.Context, f Filter) ([]Record, error) {
	return s.Store.ListRunners(ctx, f)
}

func (s *Service) background(f func()) {
	if s.Background != nil {
		s.Background(f)
		return
	}
	go f()
}

// StartApply checks the request and runs Apply in the background, so the
// request answers `applying` at once (the API answers 202).
func (s *Service) StartApply(ctx context.Context, planID string, approved []string, actor string) (Plan, error) {
	p, err := s.Store.GetPlan(ctx, planID)
	if err != nil {
		return Plan{}, err
	}
	if p.State == PlanDone || p.State == PlanFailed || p.State == PlanStopped {
		return p, refuse("plan %s is %s and cannot be applied again", p.ID, p.State)
	}
	for _, id := range approved {
		found := false
		for _, st := range p.Steps {
			found = found || st.ID == id
		}
		if !found {
			return p, refuse("step %s is not in plan %s", id, p.ID)
		}
	}
	if s.Gate != nil { // the setting may have been switched off since the plan was made
		for _, a := range p.Adds {
			if a.Scope.Kind == ScopePool {
				continue
			}
			if err := s.Gate(ctx, a.Scope); err != nil {
				return p, err
			}
		}
	}
	p.State = PlanApplying
	bg := context.WithoutCancel(ctx)
	s.background(func() { _, _ = s.Apply(bg, planID, approved, actor) })
	return p, nil
}

// find reads one runner by its id. A caller that holds the runner's name
// instead (a light or persistent runner is known by its name too) is answered
// when the name is the name of exactly one runner, so a lookup never misses a
// runner that is in the list (OPS19).
func (s *Service) find(ctx context.Context, key string) (Record, error) {
	r, err := s.Store.GetRunner(ctx, key)
	if !errors.Is(err, ErrNotFound) {
		return r, err
	}
	all, lerr := s.Store.ListRunners(ctx, Filter{})
	if lerr != nil {
		return Record{}, lerr
	}
	var hit []Record
	for _, x := range all {
		if x.Name == key {
			hit = append(hit, x)
		}
	}
	if len(hit) == 1 {
		return hit[0], nil
	}
	return Record{}, ErrNotFound
}

// StartRemove runs the drain and removal of a confirmed BeginRemove in the
// background.
func (s *Service) StartRemove(ctx context.Context, finish func(context.Context) error) {
	bg := context.WithoutCancel(ctx)
	s.background(func() { _ = finish(bg) })
}

// Apply runs the steps of a plan in order. A step that needs a yes runs only
// when its id is in approved. A step with WaitsFor waits (state `waiting`)
// until the new runner is online, the runner list can be read and the old
// runner has no job; calling Apply again continues (runners R5.6).
func (s *Service) Apply(ctx context.Context, planID string, approved []string, actor string) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.Store.GetPlan(ctx, planID)
	if err != nil {
		return Plan{}, err
	}
	if p.State == PlanDone || p.State == PlanFailed || p.State == PlanStopped {
		return p, refuse("plan %s is %s and cannot be applied again", p.ID, p.State)
	}
	ok := map[string]bool{}
	for _, id := range approved {
		ok[id] = true
	}
	for id := range ok {
		found := false
		for _, st := range p.Steps {
			found = found || st.ID == id
		}
		if !found {
			return p, refuse("step %s is not in plan %s", id, p.ID)
		}
	}
	p.State = PlanApplying
	_ = s.Store.SavePlan(ctx, p)
	s.beginRun(p.ID)
	defer s.endRun(p.ID)

	byID := map[string]*Step{}
	for i := range p.Steps {
		byID[p.Steps[i].ID] = &p.Steps[i]
	}
	for i := range p.Steps {
		st := &p.Steps[i]
		if st.State == "done" || st.State == "skipped" || st.State == "failed" {
			continue
		}
		if s.stopAsked(p.ID) {
			// The step that ran has finished; no later step starts (web-console 11).
			stopRemaining(&p)
			return p, errors.Join(s.Store.SavePlan(ctx, p))
		}
		if st.NeedsYes && !ok[st.ID] {
			st.State = "skipped"
			continue
		}
		if st.WaitsFor != "" {
			prev := byID[st.WaitsFor]
			if prev != nil && (prev.State == "skipped" || prev.State == "failed") {
				st.State, st.Error = "skipped", "the step it waits for did not run"
				continue
			}
			if prev != nil && prev.State != "done" {
				st.State = "waiting"
				continue
			}
			if reason := s.readyToRemove(ctx, p); reason != "" {
				st.State, st.Error = "waiting", reason
				continue
			}
			st.Error = ""
		}
		st.State = "running"
		_ = s.Store.SavePlan(ctx, p)
		if err := s.runStep(ctx, p, *st, actor); err != nil {
			st.State, st.Error = "failed", err.Error()
			// A failed step stops the plan: later steps that wait on it are skipped.
			continue
		}
		st.State, st.Error = "done", ""
		_ = s.Store.SavePlan(ctx, p)
		s.progress(p)
	}
	p.State = planOutcome(p)
	s.progress(p)
	return p, errors.Join(s.Store.SavePlan(ctx, p))
}

func planOutcome(p Plan) string {
	waiting, failed := false, false
	for _, st := range p.Steps {
		switch st.State {
		case "waiting", "planned", "running":
			waiting = true
		case "failed":
			failed = true
		}
	}
	switch {
	case failed:
		return PlanFailed
	case waiting:
		return PlanApplying
	}
	return PlanDone
}

// readyToRemove is empty when a replace step may go on, or the reason it waits
// (runners R5.6, section 8: fail closed).
func (s *Service) readyToRemove(ctx context.Context, p Plan) string {
	if p.OldRunnerID == "" || len(p.Adds) == 0 {
		return ""
	}
	// After the old registration is removed its record is gone; the steps after
	// that only need the new runner online and the list readable.
	old, err := s.Store.GetRunner(ctx, p.OldRunnerID)
	gone := errors.Is(err, ErrNotFound)
	if err != nil && !gone {
		return "the old runner cannot be read: " + err.Error()
	}
	scope := p.Adds[0].Scope
	if !gone {
		scope = old.Scope
	}
	t, err := s.Resolver.Resolve(ctx, scope)
	if err != nil {
		return "the scope cannot be resolved: " + err.Error()
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return "the runner list cannot be read, so nothing is removed"
	}
	newOnline := false
	for _, r := range list {
		if r.Name == p.Adds[0].Name && r.Status == "online" {
			newOnline = true
		}
		if !gone && r.Name == old.Name && r.Busy {
			return fmt.Sprintf("the old runner %s has a job; it waits until the job ends", old.Name)
		}
	}
	if !newOnline {
		return fmt.Sprintf("the new runner %s is not online yet", p.Adds[0].Name)
	}
	return ""
}

func (s *Service) runStep(ctx context.Context, p Plan, st Step, actor string) error {
	switch st.Action {
	case ActAddRunner:
		for _, a := range p.Adds {
			if a.Name == st.Target {
				return s.addRunner(ctx, a, actor)
			}
		}
		return fmt.Errorf("no runner named %s in the plan", st.Target)
	case ActStopOldService:
		old, err := s.Store.GetRunner(ctx, p.OldRunnerID)
		if err != nil {
			return err
		}
		if err := s.Agent.StopService(ctx, old.MachineID, old.ServiceName); err != nil {
			return err
		}
		s.audit(ctx, actor, "runner.old_service_stopped", old.MachineID, old.ID, old.ServiceName)
		return nil
	case ActRemoveOldRunner:
		old, err := s.Store.GetRunner(ctx, p.OldRunnerID)
		if err != nil {
			return err
		}
		return s.removeRegistration(ctx, old, actor, "runner.old_deregistered", true)
	case ActDeleteOldFolder:
		old, err := s.Store.GetRunner(ctx, p.OldRunnerID)
		if err != nil {
			return err
		}
		if err := s.Agent.CleanUp(ctx, old.MachineID, old.ServiceName); err != nil {
			return err
		}
		if err := s.Store.DeleteRunner(ctx, old.ID); err != nil {
			return err
		}
		s.audit(ctx, actor, "runner.replaced", old.MachineID, old.ID, old.Folder)
		return nil
	}
	return fmt.Errorf("unknown step action %s", st.Action)
}

// addRunner registers and starts one persistent runner. The registration token
// is fetched now, used once and dropped (runners R6.2).
func (s *Service) addRunner(ctx context.Context, a AddSpec, actor string) error {
	if a.Kind == "ephemeral" && a.Scope.Kind == ScopePool {
		return s.addPoolContainerRunner(ctx, a, actor)
	}
	if a.Kind == "ephemeral" {
		_, err := s.StartJobRunner(ctx, JobRunner{Name: a.Name, MachineID: a.MachineID, Arch: a.Arch,
			Backend: a.Backend, Labels: a.Labels, Scope: a.Scope}, actor)
		return err
	}
	pin, err := PinFor(a.OS, a.Arch)
	if err != nil {
		return err
	}
	if a.Scope.Kind == ScopePool {
		return s.addPoolRunner(ctx, a, pin.Version, actor)
	}
	if a.Scope, err = s.registrationScope(ctx, a.Scope); err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	t, err := s.Resolver.Resolve(ctx, a.Scope)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	size, err := ParseSize(string(a.SizeClass))
	if err != nil {
		return refuse("%v", err)
	}
	id := s.newID("r_")
	rec := Record{ID: id, Name: a.Name, MachineID: a.MachineID, OS: a.OS, Arch: a.Arch, Kind: a.Kind,
		Backend: a.Backend, Isolation: isolationOf(a), Labels: a.Labels,
		Route: RouteForLabels(s.PoolName, a.Labels), SizeClass: size, Scope: a.Scope,
		RunnerVersion: pin.Version, State: Creating, Managed: true, CreatedAt: s.now(), PoolRunnerID: a.PoolRunnerID,
		KeepDependencies: a.KeepDependencies}
	if err := s.Store.InsertRunner(ctx, rec); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = s.Store.SetRunnerState(ctx, id, Failed, err.Error())
		s.audit(ctx, actor, "runner.add_failed", a.MachineID, id, err.Error())
		return err
	}
	token, err := s.GitHub.CreateRegistrationToken(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fail(fmt.Errorf("registration token: %w", err))
	}
	spec := s.runnerSpec(ctx, id, a, t, pin)
	// Names GitHub already knows before the add: only a registration that the
	// add itself made is removed when it fails.
	before, listErr := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	h, err := s.Agent.AddRunner(ctx, a.MachineID, spec, backend.StartConfig{Registration: token})
	if err != nil {
		if h.Ref != "" {
			// The service exists though it did not start; keep its name so a
			// removal cleans it up.
			_ = s.Store.SetRunnerService(ctx, id, h.Ref, AccountName(a.Name, nil))
		}
		return fail(s.withRegistrationCleanup(ctx, t, a.Name, before, listErr, err))
	}
	// The runner is registered on GitHub now. A local record that cannot be
	// written would leave a GitHub runner no machine knows (runners R5.94), so
	// any failure from here removes the registration, the service and the
	// folder again.
	if err := s.Store.SetRunnerService(ctx, id, h.Ref, AccountName(a.Name, nil)); err != nil {
		return fail(s.rollbackRegistered(ctx, t, a, id, h.Ref, err))
	}
	if err := s.Store.SetRunnerState(ctx, id, Idle, "registered"); err != nil {
		return fail(s.rollbackRegistered(ctx, t, a, id, h.Ref, err))
	}
	s.audit(ctx, actor, "runner.add", a.MachineID, id, a.Name)
	return nil
}

// runnerSpec is the runner the agent starts for an add spec: the same for a
// new runner and for one that registers again in place.
func (s *Service) runnerSpec(ctx context.Context, id string, a AddSpec, t Target, pin Pin) backend.RunnerSpec {
	spec := backend.RunnerSpec{ID: id, Name: a.Name, OS: a.OS, Arch: a.Arch, Labels: a.Labels, GitHubURL: t.URL, Group: t.Group,
		RunnerVersion: pin.Version, RunnerSHA256: pin.SHA256, WindowsSymlinkRight: a.OS == "windows" && a.Symlink, CacheQuota: s.CacheQuota, MaxParallel: a.MaxParallel, KeepDependencies: a.KeepDependencies,
		Container: a.Backend == string(backend.Docker), SharedTools: s.sharedTools(a, t)}
	// Container runners cannot read the controller CA from the host yet, so
	// only the other persistent runners get the package proxy (caches section 11).
	if s.PackageProxy != nil && !spec.Container {
		spec.PackageProxy = s.PackageProxy(ctx, t.URL)
	}
	return spec
}

// rollbackRegistered undoes a registration whose local record could not be
// finished: a remove token deregisters the runner and the agent removes its
// service, account and folder. The error says what was cleaned up and what is
// left behind, never one without the other.
func (s *Service) rollbackRegistered(ctx context.Context, t Target, a AddSpec, id, ref string, cause error) error {
	ctx = context.WithoutCancel(ctx)
	token, err := s.GitHub.CreateRemoveToken(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fmt.Errorf("%w. Left behind: GitHub registration %s (no remove token: %s); remove it in the Doctor", cause, a.Name, err.Error())
	}
	if err := s.Agent.RemoveRunner(ctx, a.MachineID, RemoveRequest{RunnerID: id, ServiceName: ref, RemoveToken: token}); err != nil {
		return fmt.Errorf("%w. Left behind: GitHub registration %s and its files (%s); remove it in the Doctor", cause, a.Name, err.Error())
	}
	return fmt.Errorf("%w. Cleaned up: GitHub registration %s", cause, a.Name)
}

// withRegistrationCleanup removes the GitHub registration a failed add left
// (the agent has already removed its service, account and folder) and says in
// the error what it did or what is left. A runner of the same name that GitHub
// knew before the add is never touched.
func (s *Service) withRegistrationCleanup(ctx context.Context, t Target, name string, before []github.Runner, listErr, cause error) error {
	ctx = context.WithoutCancel(ctx)
	left := func(why string) error {
		return fmt.Errorf("%w. Left behind: GitHub registration %s (%s); remove it in the repository's runner settings", cause, name, why)
	}
	if listErr != nil {
		return left("could not read the runners on GitHub: " + listErr.Error())
	}
	for _, r := range before {
		if r.Name == name {
			return cause // an older runner of that name; not ours to remove
		}
	}
	after, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return left("could not read the runners on GitHub: " + err.Error())
	}
	for _, r := range after {
		if r.Name != name {
			continue
		}
		if err := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, r.ID); err != nil {
			return left(err.Error())
		}
		return fmt.Errorf("%w. Cleaned up: GitHub registration %s", cause, name)
	}
	return cause
}

// removeRegistration removes a runner from GitHub with a remove token, never a
// registration token, and checks the result (runners R5.4, R5.19, DL315). On
// failure the runner and its folder stay as they were.
func (s *Service) removeRegistration(ctx context.Context, r Record, actor, action string, keepFiles bool) error {
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	token, err := s.GitHub.CreateRemoveToken(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return fmt.Errorf("remove token: %w", err)
	}
	if err := s.Agent.RemoveRunner(ctx, r.MachineID, RemoveRequest{RunnerID: r.ID, ServiceName: r.ServiceName, RemoveToken: token, KeepFiles: keepFiles}); err != nil {
		return fmt.Errorf("removal failed, the runner is left as it was: %w", err)
	}
	if keepFiles {
		s.audit(ctx, actor, action, r.MachineID, r.ID, r.Name)
		return nil
	}
	if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, action, r.MachineID, r.ID, r.Name)
	return nil
}

// BeginRemove checks the request and starts the drain. Without confirm it
// returns NeedsConfirmation naming what goes and the job it waits for (R5.4,
// UX-62). With confirm it sets the runner `draining` and returns finish, which
// waits for an idle runner and removes it; run finish in the background.
func (s *Service) BeginRemove(ctx context.Context, id string, confirm bool, actor string) (Record, func(context.Context) error, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}
	id = r.ID
	if !r.Managed {
		return r, nil, refuse("runner %s was not made by CRSP; replace it instead", r.Name)
	}
	if r.Scope.Kind == ScopePool {
		return s.beginRemovePool(ctx, r, confirm, actor)
	}
	if !confirm {
		items := []ConfirmItem{
			{"registration", "The registration of runner " + r.Name + " on GitHub"},
			{"service", "The service " + r.ServiceName},
			{"account", "The account " + r.Account},
			{"folder", "The runner folder " + r.Folder},
		}
		if r.CurrentJob != "" {
			items = append(items, ConfirmItem{"job", "Waits for job " + r.CurrentJob})
		}
		return r, nil, &NeedsConfirmation{Items: items}
	}
	prev := r.State
	if CanTransition(r.State, Draining) {
		if err := s.Store.SetRunnerState(ctx, id, Draining, "remove asked"); err != nil {
			return r, nil, err
		}
		r.State = Draining
	}
	finish := func(ctx context.Context) error {
		return s.finishRemove(ctx, r, prev, actor)
	}
	return r, finish, nil
}

func (s *Service) finishRemove(ctx context.Context, r Record, prev State, actor string) error {
	d := s.drainFor(r)
	err := d.Act(ctx, func(ctx context.Context) error {
		if prev == Failed {
			// A runner that never started has nothing to deregister with a
			// token: take away what it left (UX-6).
			return s.discardFailed(ctx, r, actor, "runner.remove")
		}
		_ = s.Store.SetRunnerState(ctx, r.ID, Removing, "idle; removing")
		return s.removeRegistration(ctx, r, actor, "runner.remove", false)
	})
	if err != nil {
		// The runner still works: put it back. A timeout asks the user; it never
		// stops the job.
		_ = s.Store.SetRunnerState(ctx, r.ID, restoreState(prev), err.Error())
	}
	return err
}

// drainFor builds the drain of one runner from the service settings.
func (s *Service) drainFor(r Record) Drain {
	return Drain{Poll: s.Poll, WarnAfter: s.WarnAfter, Timeout: s.DrainTimeout, Sleep: s.Sleep, Now: s.Now,
		OnWarn: func(job string) {
			if s.OnDrainWarn != nil {
				s.OnDrainWarn(r.ID, job)
			}
		},
		OnStep: func(line string) {
			if s.OnDrainStep != nil {
				s.OnDrainStep(r.ID, line)
			}
		},
		Idle: func(ctx context.Context) (bool, string, error) { return s.idle(ctx, r) },
	}
}

func restoreState(prev State) State {
	if prev == Draining || prev == Removing {
		return Idle
	}
	return prev
}

// idle asks GitHub, right now, whether the runner has a job. An unreadable
// list is an error (fail closed).
func (s *Service) idle(ctx context.Context, r Record) (bool, string, error) {
	t, err := s.Resolver.Resolve(ctx, r.Scope)
	if err != nil {
		return false, "", err
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return false, "", err
	}
	for _, g := range list {
		if g.Name == r.Name {
			if g.Busy {
				job := r.CurrentJob
				if job == "" {
					job = "running on " + r.Name
				}
				return false, job, nil
			}
			return true, "", nil
		}
	}
	return true, "", nil // GitHub does not list it: offline, nothing runs.
}

// ReportFound takes the runners an agent found in the OS service list (R5.5).
// A runner CRSP did not make is stored as not managed; one that vanished is
// dropped; a managed runner's state follows its service.
func (s *Service) ReportFound(ctx context.Context, machineID string, found []backend.Found) error {
	m, err := s.Machines.GetMachine(ctx, machineID)
	if err != nil {
		return err
	}
	have, err := s.Store.ListRunners(ctx, Filter{MachineID: machineID})
	if err != nil {
		return err
	}
	byService := map[string]Record{}
	for _, r := range have {
		byService[r.ServiceName] = r
	}
	seen := map[string]bool{}
	sort.Slice(found, func(i, j int) bool { return found[i].ServiceName < found[j].ServiceName })
	for _, f := range found {
		seen[f.ServiceName] = true
		cur, known := byService[f.ServiceName]
		if known {
			to := Idle
			if !f.Running {
				to = Offline
			}
			if cur.State != to && cur.State != Busy && cur.State != Draining && cur.State != Removing && cur.State != Creating &&
				CanTransition(cur.State, to) {
				if err := s.Store.SetRunnerState(ctx, cur.ID, to, "service list"); err != nil {
					return err
				}
			}
			continue
		}
		state := Idle
		if !f.Running {
			state = Offline
		}
		rec := Record{ID: s.newID("r_"), Name: f.Name, MachineID: machineID, OS: m.OS, Arch: m.Arch, Kind: "persistent",
			Backend: string(backend.Persistent), Isolation: string(backend.IsolationPersistent), Labels: f.Labels,
			Route: RouteForLabels(s.PoolName, f.Labels), SizeClass: DefaultSize, Scope: Scope{Kind: "repository"}, RunnerVersion: f.RunnerVersion, State: state, Managed: f.Managed,
			ServiceName: f.ServiceName, Account: f.Account, Folder: f.Folder, CreatedAt: s.now()}
		if err := s.Store.InsertRunner(ctx, rec); err != nil {
			return err
		}
	}
	for _, r := range have {
		if !r.Managed && !seen[r.ServiceName] {
			if err := s.Store.DeleteRunner(ctx, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopRemaining ends a plan as stopped: every step that has not run is skipped.
func stopRemaining(p *Plan) {
	for i := range p.Steps {
		switch p.Steps[i].State {
		case "planned", "waiting", "running", "":
			p.Steps[i].State = "skipped"
			p.Steps[i].Error = "the plan was stopped before this step"
		}
	}
	p.State = PlanStopped
}

// StopPlan asks a plan that is applying to stop (web-console section 11). The
// step that runs finishes, no later step starts and the plan ends `stopped`.
// A plan that is not applying is refused (409). A stop never cuts a running
// job: a step only adds or removes one runner.
func (s *Service) StopPlan(ctx context.Context, planID, actor string) (Plan, error) {
	p, err := s.Store.GetPlan(ctx, planID)
	if err != nil {
		return Plan{}, err
	}
	if p.State != PlanApplying {
		return p, refuse("plan %s is %s, so there is nothing to stop", p.ID, p.State)
	}
	s.stopMu.Lock()
	running := s.running[planID]
	if running {
		if s.stops == nil {
			s.stops = map[string]bool{}
		}
		s.stops[planID] = true
	}
	s.stopMu.Unlock()
	if s.Audit != nil {
		s.Audit.Audit(ctx, actor, "plan.stop", "", planID, "stop asked while applying")
	}
	if !running {
		// A replacement waiting for the new runner has no step running: end it now.
		stopRemaining(&p)
		p.State = PlanStopped
		if err := s.Store.SavePlan(ctx, p); err != nil {
			return p, err
		}
	}
	s.progress(p)
	return p, nil
}

func (s *Service) stopAsked(id string) bool {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	return s.stops[id]
}

func (s *Service) beginRun(id string) {
	s.stopMu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	s.running[id] = true
	delete(s.stops, id)
	s.stopMu.Unlock()
}

func (s *Service) endRun(id string) {
	s.stopMu.Lock()
	delete(s.running, id)
	delete(s.stops, id)
	s.stopMu.Unlock()
}

func (s *Service) progress(p Plan) {
	if s.OnProgress != nil {
		s.OnProgress(p)
	}
}

// DefaultSharedToolsRoot is where the shared tool folders live on a machine of
// the given OS. macOS has none: its runners keep their own folders.
func DefaultSharedToolsRoot(os string) string {
	switch os {
	case "linux":
		return "/var/lib/crsp/tools"
	case "windows":
		return `C:\ProgramData\crsp\tools`
	}
	return ""
}

// sharedTools is the shared tool folders of the runner's repository, with the
// group of its runner users (caches R5.57). Off when switched off, when the OS
// has none, or when the runner is not tied to one repository.
func (s *Service) sharedTools(a AddSpec, _ Target) runnercache.SharedTools {
	root := s.SharedToolsRoot
	if root == "" {
		root = DefaultSharedToolsRoot(a.OS)
	}
	if s.NoSharedTools || root == "" || a.Scope.Kind != "repository" || a.Scope.RepositoryID == 0 {
		return runnercache.SharedTools{}
	}
	id := fmt.Sprint(a.Scope.RepositoryID)
	sep, group := "/", runnercache.RepoGroup(id)
	if a.OS == "windows" {
		sep, group = `\`, RunnersGroup // every Windows runner account is in it (runners R5.14)
	}
	return runnercache.SharedTools{Root: strings.TrimRight(root, `/\`) + sep + id, Group: group}
}
