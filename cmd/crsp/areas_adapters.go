package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// machineCapacity says which OSes some active machine serves, with the labels
// its machines carry (github-integration R5.28).
type machineCapacity struct {
	svc  *machines.Service
	pool string
}

func (c machineCapacity) OSLabels(ctx context.Context) (map[string][]string, error) {
	views, err := c.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, v := range views {
		m := v.Machine
		if !m.RemovedAt.IsZero() || m.State == "removed" {
			continue
		}
		for _, os := range servedOSes(v) {
			have := map[string]bool{}
			for _, l := range out[os] {
				have[strings.ToLower(l)] = true
			}
			for _, l := range []string{archLabel(m.Arch), c.pool} {
				if l != "" && !have[strings.ToLower(l)] {
					out[os] = append(out[os], l)
					have[strings.ToLower(l)] = true
				}
			}
			if _, ok := out[os]; !ok {
				out[os] = nil
			}
		}
	}
	return out, nil
}

// servedOSes is the OS of a machine and, for a Windows machine whose engine runs
// Linux containers with the container backend allowed and usable, linux too:
// its pool runners are Linux container runners (runners R5.47).
func servedOSes(v machines.View) []string {
	os := runners.OSName(v.Machine.OS)
	if os == "" {
		return nil
	}
	out := []string{os}
	if v.Machine.OS == "windows" && v.Engine.RunsLinuxContainers() {
		for _, b := range v.Backends {
			if b.Name == string(backend.Docker) && b.Present && b.Usable && b.Allowed {
				return append(out, runners.OSName("linux"))
			}
		}
	}
	return out
}

// MachineSizes gives each active machine's OS, labels and pool size for the
// size variables (scheduling R5.67). A machine with no pool size set offers
// the CPUs and memory it reported.
func (c machineCapacity) MachineSizes(ctx context.Context) ([]repos.MachineSize, error) {
	views, err := c.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []repos.MachineSize
	for _, v := range views {
		m := v.Machine
		if !m.RemovedAt.IsZero() || m.State == "removed" {
			continue
		}
		os := runners.OSName(m.OS)
		if os == "" {
			continue
		}
		cpus, mem := v.Settings.PoolCPUs, v.Settings.PoolMemoryGiB
		if cpus <= 0 {
			cpus = m.Facts.Threads
		}
		if mem <= 0 {
			mem = float64(m.Facts.MemoryBytes) / (1 << 30)
		}
		var labels []string
		for _, l := range []string{archLabel(m.Arch), c.pool} {
			if l != "" {
				labels = append(labels, l)
			}
		}
		out = append(out, repos.MachineSize{OS: os, Labels: labels, CPUs: cpus, MemoryGiB: mem})
	}
	return out, nil
}

// machineUp says, per OS, whether some machine can take jobs now: online on
// the agent link and accepting jobs, so a paused machine or one on battery
// counts as down (github-integration R5.43). An OS with machines but none up
// is false; an OS with no machine is absent.
type machineUp struct{ svc *machines.Service }

func (u machineUp) UpOS(ctx context.Context) (map[string]bool, error) {
	views, err := u.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	return upByOS(views), nil
}

func upByOS(views []machines.View) map[string]bool {
	out := map[string]bool{}
	for _, v := range views {
		m := v.Machine
		if !m.RemovedAt.IsZero() || m.State == "removed" {
			continue
		}
		up := m.Online && m.Accepting && m.State == machines.StateActive
		for _, os := range servedOSes(v) {
			out[os] = out[os] || up
		}
	}
	return out
}

func archLabel(arch string) string {
	switch arch {
	case "x64":
		return "X64"
	case "arm64":
		return "ARM64"
	}
	return ""
}

// machineLookup gives the runners service the machine facts.
type machineLookup struct{ svc *machines.Service }

func (l machineLookup) GetMachine(ctx context.Context, id string) (runners.Machine, error) {
	v, err := l.svc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, machines.ErrNotFound) {
			return runners.Machine{}, runners.ErrNotFound
		}
		return runners.Machine{}, err
	}
	m := runners.Machine{ID: v.Machine.ID, Name: v.Machine.Name, OS: v.Machine.OS, Arch: v.Machine.Arch,
		Online: v.Machine.Online, PoolCPUs: v.Settings.PoolCPUs, PoolMemoryGiB: v.Settings.PoolMemoryGiB, JobSlots: v.Settings.JobSlots}
	if v.Engine.RunsLinuxContainers() {
		m.ContainerOS = "linux"
	}
	for _, b := range v.Backends {
		kinds := []string{"ephemeral"}
		if b.Name == string(backend.Persistent) {
			kinds = []string{"persistent"}
		}
		opt := runners.BackendOption{
			Name: backend.Name(b.Name), Usable: b.Present && b.Usable, Allowed: b.Allowed,
			Reason: b.Reason, Kinds: kinds, OS: []string{v.Machine.OS},
		}
		if b.Name == string(backend.Docker) && m.ContainerOS != "" && v.Machine.OS != m.ContainerOS {
			// The container backend runs the containers' OS, not the host's.
			opt.OS = []string{m.ContainerOS}
		}
		m.Backends = append(m.Backends, opt)
	}
	return m, nil
}

// scopeResolver turns a runner scope into the installation and address GitHub calls need.
type scopeResolver struct {
	repos repos.Store
	auth  *auth.Service
	addr  addresses.Set
}

func (r scopeResolver) Resolve(ctx context.Context, s runners.Scope) (runners.Target, error) {
	switch s.Kind {
	case "repository":
		repo, err := r.repos.GetRepository(ctx, s.RepositoryID)
		if err != nil {
			return runners.Target{}, fmt.Errorf("repository %d: %w", s.RepositoryID, err)
		}
		return runners.Target{
			Scope:          github.RunnerScope{Owner: repo.Owner, Repo: repo.Name},
			InstallationID: repo.InstallationID, URL: r.addr.RepoPage(repo.FullName), Group: s.Group,
		}, nil
	case "organization":
		ins, err := r.repos.ListInstallations(ctx)
		if err != nil {
			return runners.Target{}, err
		}
		for _, i := range ins {
			if strings.EqualFold(i.AccountLogin, s.Organization) && !i.Removed {
				rs := github.RunnerScope{Owner: i.AccountLogin, Organization: i.AccountLogin}
				// The group id comes from the pool per organization (R5.21); a
				// scope with no group uses GitHub's default group.
				if op, ok, err := repos.OrgPoolByLogin(ctx, r.repos, i.AccountLogin); err != nil {
					return runners.Target{}, err
				} else if ok && s.Group != "" && strings.EqualFold(op.GroupName, s.Group) {
					rs.GroupID = op.GroupID
				}
				return runners.Target{Scope: rs, InstallationID: i.ID, URL: r.addr.RepoPage(i.AccountLogin), Group: s.Group}, nil
			}
		}
		return runners.Target{}, fmt.Errorf("the GitHub App is not installed on %s", s.Organization)
	}
	return runners.Target{}, fmt.Errorf("unknown runner scope %q", s.Kind)
}

// OrgScope implements runners.OrgScoper: a repository of an organization whose
// pool mode is on registers its runners at organization level (R5.21).
func (r scopeResolver) OrgScope(ctx context.Context, s runners.Scope) (runners.Scope, bool, error) {
	if s.Kind != "repository" || s.RepositoryID == 0 {
		return s, false, nil
	}
	op, ok, err := repos.OrgRunnerScope(ctx, r.repos, s.RepositoryID)
	if err != nil || !ok {
		return s, false, err
	}
	return runners.Scope{Kind: "organization", RepositoryID: s.RepositoryID, Organization: op.Organization, Group: op.GroupName}, true, nil
}

// linkAgent reaches a machine's agent. The controller's own machine runs the
// backend of this OS directly; any other machine gets the runner messages of
// the agent link (agent.proto AddRunner, StopRunner, RemoveRunner; CRSP63).
type linkAgent struct {
	local  *runners.LocalAgent
	svc    *machines.Service
	remote runners.Agent // the agent link; nil when the link is not wired
}

var errRemoteRunners = errors.New("this machine is not the controller's own and the agent link is not wired")

func (a linkAgent) forMachine(ctx context.Context, id string) (runners.Agent, error) {
	v, err := a.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Machine.IsController {
		return a.local, nil
	}
	if a.remote == nil {
		return nil, errRemoteRunners
	}
	return a.remote, nil
}

// Services lists the OS services of a machine. Only the controller's own
// machine can answer until the agent link carries the service list; for any
// other machine it errors, and the runner state does not claim a service fact
// it does not have (UX-19).
func (a linkAgent) Services(ctx context.Context, id string) ([]backend.Found, error) {
	v, err := a.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !v.Machine.IsController {
		return nil, errRemoteRunners
	}
	return a.local.Found(ctx)
}

func (a linkAgent) AddRunner(ctx context.Context, id string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	l, err := a.forMachine(ctx, id)
	if err != nil {
		return backend.Handle{}, err
	}
	return l.AddRunner(ctx, id, spec, cfg)
}

func (a linkAgent) StopService(ctx context.Context, id, service string) error {
	l, err := a.forMachine(ctx, id)
	if err != nil {
		return err
	}
	return l.StopService(ctx, id, service)
}

func (a linkAgent) RemoveRunner(ctx context.Context, id string, req runners.RemoveRequest) error {
	l, err := a.forMachine(ctx, id)
	if err != nil {
		return err
	}
	return l.RemoveRunner(ctx, id, req)
}

func (a linkAgent) CleanUp(ctx context.Context, id, service string) error {
	l, err := a.forMachine(ctx, id)
	if err != nil {
		return err
	}
	return l.CleanUp(ctx, id, service)
}

// runnerAudit writes the audit entry of every add, remove and replace.
type runnerAudit struct{ auth *auth.Service }

func (r runnerAudit) Audit(ctx context.Context, actor, action, machineID, subject, detail string) {
	if machineID != "" {
		detail = strings.TrimSpace("machine " + machineID + " " + detail)
	}
	r.auth.Audit(ctx, auth.Principal{Login: actor, Source: "web"}, action, "runner", subject, detail)
}

// doctorFleet is the controller's own data, as the doctor checks read it.
type doctorFleet struct {
	machines *machines.Service
	runners  runners.Store
	repos    repos.Store
	rsvc     *repos.Service
}

func (f doctorFleet) Machines(ctx context.Context) ([]doctor.Machine, error) {
	views, err := f.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]doctor.Machine, 0, len(views))
	allRunners, _ := f.runners.ListRunners(ctx, runners.Filter{})
	signs, _ := f.machines.ResetSigns(ctx)
	known := map[string]bool{}
	for _, v := range views {
		known[v.Machine.ID] = true
	}
	orphans := 0
	for _, r := range allRunners {
		if !known[r.MachineID] {
			orphans++
		}
	}
	for _, v := range views {
		m := doctor.Machine{
			ID: v.Machine.ID, Name: v.Machine.Name, OS: v.Machine.OS, Online: v.Machine.Online,
			LastSeen: v.Machine.LastSeen, State: v.Settings.State,
			BudgetMemoryPct: v.Settings.BudgetMemoryPct, BudgetCPUPct: v.Settings.BudgetCPUPct,
			LightSlots: v.Settings.LightSlots, PauseReason: v.Machine.PauseReason,
			JobSlots: v.Settings.JobSlots, GateMaxWaitMin: v.Settings.GateMaxWaitMinutes,
		}
		if v.LoadWarning != nil {
			m.LoadWarnSince = v.LoadWarning.Since
		}
		for _, j := range v.RunningJobs {
			m.RunningJobs = append(m.RunningJobs, strings.TrimSpace(j.Repository+" "+j.Job))
		}
		m.RunnerPeak, m.RunnerPeakRepo = f.runnerPeak(ctx, v.Machine.ID, allRunners)
		m.ResetSigns = signs[v.Machine.ID]
		if v.Machine.IsController && orphans > 0 {
			m.ResetSigns = append(m.ResetSigns, fmt.Sprintf("%d runner records belong to a machine that is not in the pool", orphans))
		}
		if m.State == "" {
			m.State = v.Machine.State
		}
		if v.Load != nil {
			m.Load = &doctor.Load{MemoryPct: v.Load.MemoryPct, CPUPct: v.Load.CPUPct,
				MemoryKnown: v.Load.MemoryKnown, CPUKnown: v.Load.CPUKnown, SampledAt: v.Load.SampledAt}
		}
		if d := v.Compact.Disk; d.Present {
			m.DockerCompact = &doctor.DockerCompact{Enabled: v.Compact.Enabled, CanCompact: d.CanCompact,
				FileBytes: d.FileBytes, UsedBytes: d.UsedBytes, UsedKnown: d.UsedKnown}
		}
		if free, total := v.Machine.Facts.DiskFreeBytes, v.Machine.Facts.DiskTotalBytes; total > 0 {
			m.DiskFreeBytes, m.DiskTotalBytes = &free, &total
		}
		out = append(out, m)
	}
	return out, nil
}

// runnerPeak is the most runners one repository can use on a machine and the
// repository's name (scheduling R5.73).
func (f doctorFleet) runnerPeak(ctx context.Context, machineID string, rs []runners.Record) (int, string) {
	n, key := runners.Peak(rs, machineID)
	if key == "" {
		return n, ""
	}
	for _, r := range rs {
		if r.MachineID == machineID && !runners.IsPoolRunner(r) && r.Scope.Kind == "repository" && key == "repo:"+strconv.FormatInt(r.Scope.RepositoryID, 10) {
			if repo, err := f.repos.GetRepository(ctx, r.Scope.RepositoryID); err == nil {
				return n, repo.Owner + "/" + repo.Name
			}
		}
	}
	return n, strings.TrimPrefix(key, "org:")
}

var _ doctor.UncleanLister = doctorFleet{}

// UncleanRunners lists the runners whose clean-up failed, pool runners
// included, for runner.unclean (doctor R5.46).
func (f doctorFleet) UncleanRunners(ctx context.Context) ([]doctor.Runner, error) {
	managed := true
	rs, err := f.runners.ListRunners(ctx, runners.Filter{Managed: &managed})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if views, err := f.machines.List(ctx); err == nil {
		for _, v := range views {
			names[v.Machine.ID] = v.Machine.Name
		}
	}
	var out []doctor.Runner
	for _, r := range rs {
		if r.UncleanReason == "" {
			continue
		}
		out = append(out, doctor.Runner{ID: r.ID, Name: r.Name, MachineID: r.MachineID, MachineName: names[r.MachineID],
			OS: r.OS, Kind: r.Kind, Labels: r.Labels, UncleanReason: r.UncleanReason})
	}
	return out, nil
}

func (f doctorFleet) Runners(ctx context.Context) ([]doctor.Runner, error) {
	managed := true
	rs, err := f.runners.ListRunners(ctx, runners.Filter{Managed: &managed})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	services := map[string][]machines.RunnerService{}
	if views, err := f.machines.List(ctx); err == nil {
		for _, v := range views {
			names[v.Machine.ID] = v.Machine.Name
			services[v.Machine.ID] = v.RunnerServices
		}
	}
	out := make([]doctor.Runner, 0, len(rs))
	for _, r := range rs {
		if runners.IsPoolRunner(r) {
			continue // a pool runner has no registration; its hidden registrations are listed
		}
		dr := doctor.Runner{
			ID: r.ID, Name: r.Name, MachineID: r.MachineID, MachineName: names[r.MachineID], OS: r.OS, Kind: r.Kind,
			Labels: r.Labels, GitHubRunnerID: r.GitHubRunnerID, ServiceName: r.ServiceName, FolderReadable: true, UncleanReason: r.UncleanReason,
			LegacyLight: runners.IsLegacyLight(r),
		}
		if dr.LegacyLight && r.State != runners.Draining && r.State != runners.Removing {
			dr.LightRemovalProblem = r.Reason
		}
		if r.Scope.Organization != "" {
			dr.Scope = github.RunnerScope{Owner: r.Scope.Organization, Organization: r.Scope.Organization}
			if repo, err := f.repos.GetRepository(ctx, r.Scope.RepositoryID); err == nil {
				dr.InstallationID = repo.InstallationID
			}
		} else if r.Scope.Kind == "repository" {
			if repo, err := f.repos.GetRepository(ctx, r.Scope.RepositoryID); err == nil {
				dr.InstallationID = repo.InstallationID
				dr.Scope = github.RunnerScope{Owner: repo.Owner, Repo: repo.Name}
			}
		}
		dr.Logon = logonProbe(services[r.MachineID], r)
		out = append(out, dr)
	}
	return out, nil
}

// logonProbe is what the machine's agent reported about the runner's service
// (runners R5.89); nil when it reported none for this runner.
func logonProbe(svcs []machines.RunnerService, r runners.Record) *doctor.LogonProbe {
	for _, s := range svcs {
		if (r.ServiceName != "" && s.Service == r.ServiceName) || s.Runner == r.Name {
			return &doctor.LogonProbe{Service: s.Service, Account: s.Account, Known: s.LogonKnown, Held: s.LogonHeld,
				Running: s.Running, Problem: s.Problem}
		}
	}
	return nil
}

// Services needs a machine's agent to list its OS services; the agent link does
// not carry that yet, so the checks that need it skip (doctor R5.5).
func (f doctorFleet) Services(context.Context, string) ([]doctor.OSService, error) {
	return nil, doctor.ErrUnreachable
}

func (f doctorFleet) Repositories(ctx context.Context) ([]doctor.Repository, error) {
	on := true
	rs, err := f.repos.ListRepositories(ctx, repos.ListFilter{Pool: &on})
	if err != nil {
		return nil, err
	}
	out := make([]doctor.Repository, 0, len(rs))
	for _, r := range rs {
		pol, _ := f.repos.GetPolicy(ctx, r.ID)
		want, err := f.rsvc.DesiredNow(ctx, r)
		if err != nil {
			want = nil
		}
		rt, _ := f.rsvc.RoutingOf(ctx, r.ID)
		out = append(out, doctor.Repository{ID: r.ID, FullName: r.FullName, Owner: r.Owner, Name: r.Name,
			InstallationID: r.InstallationID, DefaultBranch: r.DefaultBranch, Expected: want, EphemeralOnly: pol.EphemeralOnly(r.Visibility),
			Where: rt.Where, FallbackOn: pol.HostedFallback, HostedOSes: hostedOSes(rt), WaitingOSes: waitingOSes(rt)})
	}
	return out, nil
}

// poolRepos lists the repositories whose pool is on, with the scope their
// runners register in.
func poolRepos(store repos.Store) func(context.Context) ([]runners.PoolRepo, error) {
	return func(ctx context.Context) ([]runners.PoolRepo, error) {
		on := true
		rs, err := store.ListRepositories(ctx, repos.ListFilter{Pool: &on})
		if err != nil {
			return nil, err
		}
		out := make([]runners.PoolRepo, 0, len(rs))
		for _, r := range rs {
			out = append(out, runners.PoolRepo{ID: r.ID, FullName: r.FullName, Scope: runners.Scope{Kind: "repository", RepositoryID: r.ID}})
		}
		return out, nil
	}
}

func hostedOSes(rt repos.Routing) []string {
	if rt.Where == repos.WhereHosted {
		return rt.OSes
	}
	return nil
}

func waitingOSes(rt repos.Routing) []string {
	if rt.Where == repos.WhereWaits {
		return rt.OSes
	}
	return nil
}

// factsReported says whether every machine that is online has reported its
// facts since started: nothing is believed absent before that (R5.28).
func factsReported(d controllerDeps, started time.Time) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		views, err := d.Machines.List(ctx)
		if err != nil {
			return false, err
		}
		for _, v := range views {
			m := v.Machine
			if !m.RemovedAt.IsZero() || m.State == "removed" || !m.Online {
				continue
			}
			if m.Facts.DetectedAt.Before(started) {
				return false, nil
			}
		}
		return true, nil
	}
}
