package machines

import (
	"context"
	"fmt"
	"sort"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// View is a machine with what the console and CLI show (R5.50).
type View struct {
	Machine       Machine
	Settings      Settings
	Backends      []BackendView
	Load          *Load
	SlotsUsed     int
	RunningJobs   []JobRef
	PendingChange *Change
	// Policy is what the agent last reported about taking new jobs (R5.62).
	Policy Policy
	// Engine is the container engine the agent last reported (R5.18). The zero
	// value means nothing was reported.
	Engine Engine
	// Recovery is set when the machine needs recovery (operations R5.44).
	Recovery *Recovery
	// Compact is the Docker disk compact policy state (caches R5.51 to R5.54).
	Compact CompactStatus
	// RunnerServices are the runner services of the machine as its agent last
	// left them (runners R5.89); empty for an offline machine.
	RunnerServices []RunnerService
	// LoadWarning is set while the machine is over its load budget with jobs
	// running (scheduling R5.77); nil otherwise.
	LoadWarning *LoadWarning
}

// BackendView is a detected backend and whether the user allowed it (R5.12).
type BackendView struct {
	Backend
	Allowed bool
}

// Stale says whether a load is older than 20 seconds (R6.7).
func (l Load) Stale(now time.Time) bool { return now.Sub(l.ReceivedAt) > StaleAfter }

// List returns every machine as a View.
func (s *Service) List(ctx context.Context) ([]View, error) {
	ms, err := s.o.Repo.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ms))
	for _, m := range ms {
		v, err := s.view(ctx, m)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns one machine as a View.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !m.RemovedAt.IsZero() {
		return View{}, ErrNotFound
	}
	return s.view(ctx, m)
}

func (s *Service) view(ctx context.Context, m Machine) (View, error) {
	set, err := s.o.Repo.GetSettings(ctx, m.ID)
	if err != nil {
		return View{}, err
	}
	s.fillPolicy(&m, set)
	v := View{Machine: m, Settings: set, Policy: capFor(s.Policy(m.ID), set.EffectiveCPUCap()), Engine: s.Engine(m.ID), Compact: s.CompactStatus(m, set)}
	if m.Online {
		v.RunnerServices = s.RunnerServices(m.ID)
	}
	if r, ok := s.RecoveryOf(m); ok {
		v.Recovery = &r
	}
	allowed := map[string]bool{}
	for _, b := range set.AllowedBackends {
		allowed[b] = true
	}
	for _, b := range m.Backends {
		v.Backends = append(v.Backends, BackendView{Backend: b, Allowed: allowed[b.Name]})
	}
	s.mu.Lock()
	if l, ok := s.loads[m.ID]; ok {
		l := l
		v.Load = &l
		v.SlotsUsed = l.SlotsUsed
	}
	v.RunningJobs = append([]JobRef(nil), s.running[m.ID]...)
	v.LoadWarning = s.loadWarningLocked(m.ID)
	s.mu.Unlock()
	if c, ok, err := s.o.Repo.OpenChange(ctx, m.ID); err != nil {
		return View{}, err
	} else if ok {
		v.PendingChange = &c
	}
	return v, nil
}

// validate checks the ranges of R5.22.
func validate(set Settings, facts Facts, backends []Backend) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	switch {
	case set.BudgetMemoryPct < 10 || set.BudgetMemoryPct > 95:
		return bad("the memory budget is 10 to 95 percent")
	case set.BudgetCPUPct < 10 || set.BudgetCPUPct > 95:
		return bad("the CPU budget is 10 to 95 percent")
	case set.JobSlots < 0 || set.JobSlots > 64, set.LightSlots < 0 || set.LightSlots > 64, set.HeavySlots < 0 || set.HeavySlots > 64:
		return bad("job slots are 0 to 64")
	case set.DrainTimeoutMinutes < 5 || set.DrainTimeoutMinutes > 1440:
		return bad("the drain timeout is 5 minutes to 24 hours")
	case set.CacheCapBytes < 1<<30:
		return bad("the cache size cap is at least 1 GiB")
	case facts.DiskFreeBytes > 0 && set.CacheCapBytes > facts.DiskFreeBytes:
		return bad("the cache size cap is above the free disk")
	case set.GateOnTimeout != "fail" && set.GateOnTimeout != "run":
		return bad("gate on timeout is fail or run")
	}
	if err := validatePolicy(set); err != nil {
		return err
	}
	if err := validateCompact(set); err != nil {
		return err
	}
	usable := map[string]bool{"persistent": true}
	known := len(backends) > 0
	for _, b := range backends {
		usable[b.Name] = b.Usable
	}
	for _, name := range set.AllowedBackends {
		if known && !usable[name] {
			return bad("backend %q is not usable on this machine", name)
		}
		if !known && name != "persistent" {
			return bad("backend %q has not been detected on this machine yet", name)
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func apply(set Settings, u SettingsUpdate) Settings {
	if u.BudgetMemoryPct != nil {
		set.BudgetMemoryPct = *u.BudgetMemoryPct
	}
	if u.BudgetCPUPct != nil {
		set.BudgetCPUPct = *u.BudgetCPUPct
	}
	if u.JobSlots != nil {
		set.JobSlots = *u.JobSlots
	}
	if u.LightSlots != nil {
		set.LightSlots = *u.LightSlots
	}
	if u.HeavySlots != nil {
		set.HeavySlots = *u.HeavySlots
	}
	if u.LightJobs != nil {
		set.LightJobs = *u.LightJobs
	}
	if u.HeavyJobs != nil {
		set.HeavyJobs = *u.HeavyJobs
	}
	if u.GateOnTimeout != nil {
		set.GateOnTimeout = *u.GateOnTimeout
	}
	if u.AllowedBackends != nil {
		set.AllowedBackends = *u.AllowedBackends
	}
	if u.CacheCapBytes != nil {
		set.CacheCapBytes = *u.CacheCapBytes
	}
	if u.DrainTimeoutMinutes != nil {
		set.DrainTimeoutMinutes = *u.DrainTimeoutMinutes
	}
	if u.WindowsSymlinkRight != nil {
		set.WindowsSymlinkRight = *u.WindowsSymlinkRight
	}
	if u.PoolMemoryGiB != nil {
		set.PoolMemoryGiB = *u.PoolMemoryGiB
	}
	if u.PoolCPUs != nil {
		set.PoolCPUs = *u.PoolCPUs
	}
	return set
}

// UpdateSettings saves a settings change (R5.22 to R5.26).
//
// A change to the budget, slots or the symlink right takes effect for the next
// job and reaches an online agent at once (R5.24, R5.25): running jobs keep
// going. A change to the allowed backends restarts runners, so it goes through
// drain (R5.26): the returned Change waits for the running jobs, and Confirm is
// needed once those jobs are named. Exactly one of the Machine view and the
// Change is returned.
func (s *Service) UpdateSettings(ctx context.Context, actor, id string, u SettingsUpdate) (View, *Change, error) {
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return View{}, nil, err
	}
	if !m.RemovedAt.IsZero() {
		return View{}, nil, ErrNotFound
	}
	cur, err := s.o.Repo.GetSettings(ctx, id)
	if err != nil {
		return View{}, nil, err
	}
	next := apply(cur, u)
	next = applyCompact(next, u)
	next, err = applyPolicy(next, u, s.now())
	if err != nil {
		return View{}, nil, err
	}
	if u.DiskFloorGiB != nil && s.Policy(id).DiskFloorOff {
		return View{}, nil, fmt.Errorf("%w: the free disk floor is off in config.toml on this machine; change it there", ErrInvalid)
	}
	if err := validate(next, m.Facts, m.Backends); err != nil {
		return View{}, nil, err
	}
	if u.PoolMemoryGiB != nil || u.PoolCPUs != nil || u.BudgetMemoryPct != nil || u.BudgetCPUPct != nil {
		if err := s.checkPoolSize(id, m, cur, next, u.ConfirmShrink); err != nil {
			return View{}, nil, err
		}
	}
	if u.Name != nil {
		if *u.Name == "" || len(*u.Name) > 64 {
			return View{}, nil, fmt.Errorf("%w: a machine name is 1 to 64 characters", ErrInvalid)
		}
		if *u.Name != m.Name {
			nm, _, err := s.updateMachine(ctx, id, func(m *Machine) bool { m.Name = *u.Name; return true })
			if err != nil {
				return View{}, nil, err
			}
			m = nm
			_ = s.o.Repo.Audit(ctx, actor, "machine.rename", id, m.Name)
		}
	}
	restart := !sameSet(cur.AllowedBackends, next.AllowedBackends)
	if restart {
		if _, open, err := s.o.Repo.OpenChange(ctx, id); err != nil {
			return View{}, nil, err
		} else if open {
			return View{}, nil, fmt.Errorf("%w: a change is already waiting on this machine", ErrConflict)
		}
		jobs := s.runningJobs(id)
		if len(jobs) > 0 && !u.Confirm {
			return View{}, nil, &NeedsConfirmationError{
				Message: "this change restarts runners and waits for running jobs to finish",
				Names:   jobNames(jobs),
			}
		}
		c, err := s.beginChange(ctx, actor, m, KindSettings, ChangeDetail{Settings: &next, Base: &cur, Reason: "allowed backends changed"}, cur.DrainTimeoutMinutes)
		if err != nil {
			return View{}, nil, err
		}
		return View{}, &c, nil
	}
	next.Version = cur.Version + 1
	if err := s.o.Repo.PutSettings(ctx, id, next); err != nil {
		return View{}, nil, err
	}
	_ = s.o.Repo.Audit(ctx, actor, "machine.settings", id, settingsDiff(cur, next))
	s.pushSettings(id, next)
	s.notifyMachine(id)
	v, err := s.Get(ctx, id)
	return v, nil, err
}

func settingsDiff(a, b Settings) string {
	var out string
	add := func(name string, x, y any) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			out += fmt.Sprintf("%s %v -> %v; ", name, x, y)
		}
	}
	add("memory budget", a.BudgetMemoryPct, b.BudgetMemoryPct)
	add("CPU budget", a.BudgetCPUPct, b.BudgetCPUPct)
	add("job slots", a.JobSlots, b.JobSlots)
	add("light slots", a.LightSlots, b.LightSlots)
	add("heavy slots", a.HeavySlots, b.HeavySlots)
	add("cache cap", a.CacheCapBytes, b.CacheCapBytes)
	add("drain timeout", a.DrainTimeoutMinutes, b.DrainTimeoutMinutes)
	add("backends", a.AllowedBackends, b.AllowedBackends)
	add("symlink right", a.WindowsSymlinkRight, b.WindowsSymlinkRight)
	add("pool memory GiB", a.PoolMemoryGiB, b.PoolMemoryGiB)
	add("pool CPUs", a.PoolCPUs, b.PoolCPUs)
	add("mode", a.EffectiveMode(), b.EffectiveMode())
	add("pause on battery", a.PauseOnBattery, b.PauseOnBattery)
	add("CPU cap", a.EffectiveCPUCap(), b.EffectiveCPUCap())
	add("disk floor GiB", a.DiskFloorGiB, b.DiskFloorGiB)
	add("docker compact", a.CompactMode, b.CompactMode)
	add("docker compact threshold GiB", a.CompactThresholdGiB, b.CompactThresholdGiB)
	add("docker compact idle minutes", a.CompactIdleMinutes, b.CompactIdleMinutes)
	add("pause until", a.PauseUntil, b.PauseUntil)
	add("pause until resumed", a.PausedUntilResumed, b.PausedUntilResumed)
	return out
}

// ProtoSettings converts settings to the wire message.
func ProtoSettings(st Settings, state string) *agentv1.MachineSettings {
	return &agentv1.MachineSettings{
		Version: st.Version, State: state,
		BudgetMemoryPct: int32(st.BudgetMemoryPct), BudgetCpuPct: int32(st.BudgetCPUPct), //nolint:gosec // validated ranges
		JobSlots: int32(st.JobSlots), LightSlots: int32(st.LightSlots), HeavySlots: int32(st.HeavySlots), //nolint:gosec // validated
		LightJobs: st.LightJobs, HeavyJobs: st.HeavyJobs,
		SlotMaxAgeMinutes: int32(st.SlotMaxAgeMinutes), GatePollSeconds: int32(st.GatePollSeconds), //nolint:gosec // validated
		GateMaxWaitMinutes: int32(st.GateMaxWaitMinutes), GateOnTimeout: st.GateOnTimeout, //nolint:gosec // validated
		AllowedBackends: st.AllowedBackends, CacheCapBytes: st.CacheCapBytes,
		DrainTimeoutMinutes: int32(st.DrainTimeoutMinutes), WindowsSymlinkRight: st.WindowsSymlinkRight, //nolint:gosec // validated
		PoolMemoryGib: st.PoolMemoryGiB, PoolCpus: int32(st.PoolCPUs), //nolint:gosec // validated
		Mode: st.EffectiveMode(), PauseOnBattery: st.PauseOnBattery, PauseUntilResumed: st.PausedUntilResumed,
		PauseUntilUnixMs: untilMs(st.PauseUntil), CpuCapPct: int32(st.EffectiveCPUCap()), //nolint:gosec // validated
		DiskFloorGib: int32(st.DiskFloorGiB), //nolint:gosec // validated to 2000
	}
}

func untilMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// pushSettings sends settings to an online agent; an offline agent gets them
// in HelloAck on reconnect (R5.24).
func (s *Service) pushSettings(id string, st Settings) {
	state := StateActive
	if m, err := s.o.Repo.GetMachine(context.Background(), id); err == nil {
		state = m.State
	}
	_ = s.sessions.send(id, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_Settings{Settings: ProtoSettings(st, state)}})
}
