package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/poolsize"
)

// MachinesService is what the machines routes need (machines R5.50 to R5.52,
// R6.4). *machines.Service implements it.
type MachinesService interface {
	PoolSizeCheck(ctx context.Context, id string, memGiB float64, cpus int) (machines.PoolSizeCheck, error)
	List(ctx context.Context) ([]machines.View, error)
	Get(ctx context.Context, id string) (machines.View, error)
	UpdateSettings(ctx context.Context, actor, id string, u machines.SettingsUpdate) (machines.View, *machines.Change, error)
	Drain(ctx context.Context, actor, id, reason string) (machines.Change, error)
	Resume(ctx context.Context, actor, id string, confirm bool) (machines.View, error)
	Remove(ctx context.Context, actor, id string, confirm bool) (machines.Change, error)
	Redetect(ctx context.Context, actor, id string) error
	RunHostCommand(ctx context.Context, actor, id string, argv []string, timeout time.Duration) (machines.HostOutcome, error)
	RemoveRunnerLeftovers(ctx context.Context, actor, machineID string, refs []leftovers.Ref) ([]leftovers.Result, error)
	StartCompact(ctx context.Context, actor, id string, confirm bool) error
	ArmRecovery(ctx context.Context, actor, id string) error
	LoadHistory(ctx context.Context, id string, from, to time.Time) ([]machines.LoadPoint, error)
	GetChange(ctx context.Context, machineID, id string) (machines.Change, error)
	DecideChange(ctx context.Context, actor, machineID, changeID string, d machines.Decision, waitMinutes int, confirm bool) (machines.Change, error)
	CreateJoinCode(ctx context.Context, actor string, ttl time.Duration) (machines.CreatedCode, error)
	GetJoinCode(ctx context.Context, id string) (machines.JoinCode, machines.CodeState, error)
	RevokeJoinCode(ctx context.Context, actor, id string) error
	JoinOverSSH(ctx context.Context, actor string, in machines.SSHJoinInput) (machines.CreatedCode, error)
	CreateJoinFile(ctx context.Context, actor, os string, ttl time.Duration) (machines.JoinFile, error)
	InstallCommands(code string) []machines.InstallCommand
	ControllerInfo() (address string, port int, fingerprint string)
}

var _ MachinesService = (*machines.Service)(nil)

// CAProvider gives the controller CA certificate for the one-click download.
type CAProvider interface{ CAPEM() []byte }

type errShape = struct {
	Body       gen.ErrorEnvelope
	StatusCode int
}

// machineErr maps a machines error to the error envelope of any operation's
// default response.
func machineErr[T ~struct {
	Body       gen.ErrorEnvelope
	StatusCode int
}](log func(string, ...any), err error) T {
	status, env := mapMachinesError(log, err)
	return T(errShape{Body: env, StatusCode: status})
}

func mapMachinesError(log func(string, ...any), err error) (int, gen.ErrorEnvelope) {
	env := func(code, msg string) gen.ErrorEnvelope {
		return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
	}
	var nc *machines.NeedsConfirmationError
	switch {
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Names))
		for _, n := range nc.Names {
			details = append(details, gen.ErrorDetail{Kind: "item", Text: n})
		}
		e := env(CodeNeedsConfirmation, nc.Message)
		e.Error.Details = &details
		return http.StatusConflict, e
	case errors.Is(err, machines.ErrNotFound):
		return http.StatusNotFound, env(CodeNotFound, "no such machine, change or join code")
	case errors.Is(err, machines.ErrInvalid):
		return http.StatusBadRequest, env(CodeBadRequest, err.Error())
	case errors.Is(err, machines.ErrConflict):
		return http.StatusConflict, env(CodeConflict, err.Error())
	case errors.Is(err, machines.ErrOffline):
		return http.StatusConflict, env(CodeConflict, "the machine is offline; the change will reach it when it connects")
	case errors.Is(err, machines.ErrNotSupported):
		return http.StatusNotImplemented, env(CodeNotImplemented, err.Error())
	case errors.Is(err, machines.ErrUnauthenticated):
		return http.StatusUnauthorized, env(CodeUnauthenticated, "unknown or revoked key")
	}
	if log != nil {
		log("machines: handler failed", "err", err)
	}
	return http.StatusServiceUnavailable, env(CodeUnavailable, "the controller cannot reach its database right now")
}

func (s *server) machinesOff() bool { return s.opts.Machines == nil }

func machinesUnavailable[T ~struct {
	Body       gen.ErrorEnvelope
	StatusCode int
}]() T {
	return T(errShape{StatusCode: http.StatusNotImplemented, Body: notImplemented()})
}

func fail[T ~struct {
	Body       gen.ErrorEnvelope
	StatusCode int
}](s *server, err error) T {
	return machineErr[T](func(m string, a ...any) { s.opts.Logger.Error(m, a...) }, err)
}

func (s *server) actor(ctx context.Context) string { return machines.ActorFrom(ctx) }

// ---------------------------------------------------------------- conversions

func machinesPtr[T any](v T) *T { return &v }

func jobToAPI(j machines.JobRef) gen.Job {
	out := gen.Job{
		Id: j.GitHubJobID, GithubJobId: j.GitHubJobID, JobName: j.Job, Labels: []string{}, Repository: j.Repository,
		RunAttempt: j.RunAttempt, RunId: j.RunID, State: gen.JobStateRunning,
	}
	if j.RunnerName != "" {
		out.RunnerName = machinesPtr(j.RunnerName)
	}
	if !j.StartedAt.IsZero() {
		out.StartedAt = machinesPtr(j.StartedAt)
	}
	return out
}

func changeToAPI(c machines.Change) gen.MachineChange {
	out := gen.MachineChange{
		Id: c.ID, MachineId: c.MachineID, Kind: gen.MachineChangeKind(c.Kind), State: gen.MachineChangeState(c.State),
		StartedAt: c.StartedAt, WaitingFor: []gen.Job{},
	}
	if !c.Deadline.IsZero() {
		out.DeadlineAt = machinesPtr(c.Deadline)
	}
	for _, j := range c.Detail.WaitingFor {
		out.WaitingFor = append(out.WaitingFor, jobToAPI(j))
	}
	if len(c.Detail.RunnerNames) > 0 {
		names := append([]string(nil), c.Detail.RunnerNames...)
		out.RunnerNames = &names
	}
	if h := c.Detail.Holder; h != nil {
		dh := gen.DrainHolder{Job: jobToAPI(h.Job), OverUsual: h.OverUsual}
		if !h.Job.StartedAt.IsZero() {
			dh.RunningSeconds = int(time.Since(h.Job.StartedAt).Seconds())
		}
		if h.Usual > 0 {
			dh.UsualSeconds = machinesPtr(int(h.Usual.Seconds()))
		}
		out.Holder = &dh
	}
	if ch := machines.Choices(c); len(ch) > 0 {
		cs := make([]gen.MachineChangeChoices, 0, len(ch))
		for _, x := range ch {
			cs = append(cs, gen.MachineChangeChoices(x))
		}
		out.Choices = &cs
	}
	return out
}

func settingsToAPI(st machines.Settings) gen.MachineSettings {
	backends := make([]gen.BackendName, 0, len(st.AllowedBackends))
	for _, b := range st.AllowedBackends {
		backends = append(backends, gen.BackendName(b))
	}
	out := gen.MachineSettings{
		AllowedBackends: backends, BudgetCpuPct: st.BudgetCPUPct, BudgetMemoryPct: st.BudgetMemoryPct,
		CacheCapBytes: machinesPtr(st.CacheCapBytes), DrainTimeoutMinutes: st.DrainTimeoutMinutes,
		GateMaxWaitMinutes: machinesPtr(st.GateMaxWaitMinutes), GatePollSeconds: machinesPtr(st.GatePollSeconds),
		HeavySlots: st.HeavySlots, JobSlots: st.JobSlots, LightSlots: st.LightSlots,
		SlotMaxAgeMinutes: machinesPtr(st.SlotMaxAgeMinutes), WindowsSymlinkRight: st.WindowsSymlinkRight,
		PoolMemoryGib: machinesPtr(st.PoolMemoryGiB), PoolCpus: machinesPtr(st.PoolCPUs),
		HeavyJobs: machinesPtr(append([]string{}, st.HeavyJobs...)), LightJobs: machinesPtr(append([]string{}, st.LightJobs...)),
	}
	if st.GateOnTimeout != "" {
		out.GateOnTimeout = machinesPtr(gen.MachineSettingsGateOnTimeout(st.GateOnTimeout))
	}
	out.Mode = machinesPtr(gen.MachineMode(st.EffectiveMode()))
	out.PauseOnBattery = machinesPtr(st.PauseOnBattery)
	out.CpuCapPct = machinesPtr(gen.CpuCapPct(st.EffectiveCPUCap()))
	out.PauseUntilResumed = machinesPtr(st.PausedUntilResumed)
	mode := gen.DockerCompactModeAuto
	switch st.CompactMode {
	case machines.CompactOn:
		mode = gen.DockerCompactModeOn
	case machines.CompactOff:
		mode = gen.DockerCompactModeOff
	}
	pol := st.EffectiveCompact()
	out.DockerCompact = machinesPtr(mode)
	out.DockerCompactThresholdGib = machinesPtr(int(pol.ThresholdBytes >> 30))
	out.DockerCompactIdleMinutes = machinesPtr(int(pol.Idle / time.Minute))
	if !st.PauseUntil.IsZero() {
		out.PauseUntil = machinesPtr(st.PauseUntil)
	}
	return out
}

func f32(v float64) *float32 { x := float32(v); return &x }

func machineToAPI(v machines.View, now time.Time) gen.Machine {
	m := v.Machine
	out := gen.Machine{
		Id: m.ID, Name: m.Name, Os: gen.OS(m.OS), Arch: gen.Arch(m.Arch), State: gen.MachineState(m.State), Online: m.Online,
		IsController: m.IsController, Settings: settingsToAPI(v.Settings), SlotsUsed: v.SlotsUsed, LastSeenAt: m.LastSeen,
		Backends: []gen.BackendSupport{},
	}
	if !m.JoinedAt.IsZero() {
		out.JoinedAt = machinesPtr(m.JoinedAt)
	}
	out.Accepting = machinesPtr(m.Accepting)
	out.Taking = gen.MachineTaking{TakingJobs: m.Taking.Taking}
	if m.Taking.Reason != "" {
		out.Taking.Reason = machinesPtr(gen.MachineTakingReason(m.Taking.Reason))
	}
	if v.Engine.Usable && v.Engine.ContainerOS != "" {
		out.ContainerOs = machinesPtr(gen.OS(v.Engine.ContainerOS))
	}
	if r := v.Recovery; r != nil {
		out.Recovery = &gen.MachineRecovery{State: gen.NeedsRecovery, Reason: gen.MachineRecoveryReason(r.Reason), Has: r.Has, Needs: r.Needs, Since: r.Since, Armed: r.Armed}
	}
	if m.PauseReason != "" {
		out.PauseReason = machinesPtr(gen.PauseReason(m.PauseReason))
	}
	pol := gen.MachinePolicy{OnBattery: v.Policy.OnBattery, BatteryKnown: v.Policy.BatteryKnown}
	if v.Policy.CPUCapState != "" {
		pol.CpuCapState = machinesPtr(gen.CpuCapState(v.Policy.CPUCapState))
	}
	if v.Policy.CPUCapDetail != "" {
		pol.CpuCapDetail = machinesPtr(v.Policy.CPUCapDetail)
	}
	out.Policy = &pol
	if cs := v.Compact; cs.Disk.Reported {
		d := gen.DockerCompactStatus{Enabled: cs.Enabled, Running: cs.Running, DiskPresent: cs.Disk.Present, CanCompact: cs.Disk.CanCompact}
		if cs.Reason != "" {
			d.Reason = machinesPtr(cs.Reason)
		}
		if cs.Disk.Present {
			d.FileBytes, d.UsedBytes, d.ReclaimableBytes = machinesPtr(cs.Disk.FileBytes), machinesPtr(cs.Disk.UsedBytes), machinesPtr(cs.Disk.Reclaimable())
		}
		if !cs.LastAt.IsZero() {
			d.LastAt = machinesPtr(cs.LastAt)
		}
		if cs.LastResult != "" {
			d.LastResult = machinesPtr(cs.LastResult)
		}
		out.DockerCompactStatus = &d
	}
	if probs := v.RunnerProblems(); len(probs) > 0 {
		list := make([]gen.RunnerServiceProblem, 0, len(probs))
		for _, p := range probs {
			item := gen.RunnerServiceProblem{Runner: p.Runner, Service: p.Service, Account: p.Account, Reason: p.Problem}
			if p.LogonKnown {
				item.LogonRight = machinesPtr(p.LogonHeld)
			}
			list = append(list, item)
		}
		out.RunnerProblems = &list
	}
	for _, b := range v.Backends {
		bs := gen.BackendSupport{Name: gen.BackendName(b.Name), Present: b.Present, Usable: b.Usable, Allowed: b.Allowed}
		if b.Version != "" {
			bs.Version = machinesPtr(b.Version)
		}
		if b.Reason != "" {
			bs.Reason = machinesPtr(b.Reason)
		}
		if b.Rootless {
			bs.Rootless = machinesPtr(true)
		}
		out.Backends = append(out.Backends, bs)
	}
	f := m.Facts
	facts := gen.MachineFacts{
		AgentVersion: machinesPtr(m.AgentVersion), Cores: machinesPtr(f.Cores), CpuModel: machinesPtr(f.CPUModel), DiskFreeBytes: machinesPtr(f.DiskFreeBytes),
		DiskTotalBytes: machinesPtr(f.DiskTotalBytes), HostName: machinesPtr(f.HostName), KernelVersion: machinesPtr(f.KernelVersion),
		MemoryBytes: machinesPtr(f.MemoryBytes), OsName: machinesPtr(f.OSName), OsVersion: machinesPtr(f.OSVersion), Threads: machinesPtr(f.Threads),
	}
	if !f.DetectedAt.IsZero() {
		facts.DetectedAt = machinesPtr(f.DetectedAt)
	}
	gpus := []struct {
		MemoryBytes *int64  `json:"memory_bytes,omitempty"`
		Model       *string `json:"model,omitempty"`
		Vendor      *string `json:"vendor,omitempty"`
	}{}
	for _, g := range f.GPUs {
		gpus = append(gpus, struct {
			MemoryBytes *int64  `json:"memory_bytes,omitempty"`
			Model       *string `json:"model,omitempty"`
			Vendor      *string `json:"vendor,omitempty"`
		}{MemoryBytes: machinesPtr(g.MemoryBytes), Model: machinesPtr(g.Model), Vendor: machinesPtr(g.Vendor)})
	}
	facts.Gpus = &gpus
	out.Facts = &facts
	if v.Load != nil {
		l := gen.Load{MemoryKnown: v.Load.MemoryKnown, CpuKnown: v.Load.CPUKnown, SampledAt: v.Load.SampledAt, Stale: v.Load.Stale(now)}
		if v.Load.MemoryKnown {
			l.MemoryPct = f32(v.Load.MemoryPct)
		}
		if v.Load.CPUKnown {
			l.CpuPct = f32(v.Load.CPUPct)
		}
		if v.Load.UnknownReason != "" {
			l.UnknownReason = machinesPtr(v.Load.UnknownReason)
		}
		out.Load = &l
	}
	jobs := make([]gen.Job, 0, len(v.RunningJobs))
	for _, j := range v.RunningJobs {
		jobs = append(jobs, jobToAPI(j))
	}
	out.RunningJobs = &jobs
	if v.PendingChange != nil {
		c := changeToAPI(*v.PendingChange)
		out.PendingChange = &c
	}
	return out
}

func installToAPI(svc MachinesService, code string) gen.InstallCommands {
	addr, port, fp := svc.ControllerInfo()
	out := gen.InstallCommands{ControllerAddress: addr, AgentPort: port, CertSha256: fp}
	for _, c := range svc.InstallCommands(code) {
		out.Items = append(out.Items, struct {
			Command string `json:"command"`
			Os      gen.OS `json:"os"`
		}{Command: c.Command, Os: gen.OS(c.OS)})
	}
	return out
}

func codeToAPI(c machines.JoinCode, st machines.CodeState) gen.JoinCode {
	out := gen.JoinCode{Id: c.ID, State: gen.JoinCodeState(st), CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt}
	switch st {
	case machines.CodeWaiting:
		out.Progress = machinesPtr(gen.JoinCodeProgressWaiting)
	case machines.CodeJoined:
		out.Progress = machinesPtr(gen.JoinCodeProgressConnected)
	}
	if c.MachineID != "" {
		out.MachineId = machinesPtr(c.MachineID)
	}
	return out
}

// ---------------------------------------------------------------- handlers

// ListMachines implements GET /api/v1/machines (machines R5.50).
func (s *server) ListMachines(ctx context.Context, _ gen.ListMachinesRequestObject) (gen.ListMachinesResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.ListMachinesdefaultJSONResponse](), nil
	}
	vs, err := s.opts.Machines.List(ctx)
	if err != nil {
		return fail[gen.ListMachinesdefaultJSONResponse](s, err), nil
	}
	now := time.Now()
	out := gen.ListMachines200JSONResponse{Items: make([]gen.Machine, 0, len(vs))}
	for _, v := range vs {
		out.Items = append(out.Items, machineToAPI(v, now))
	}
	return out, nil
}

// GetMachine implements GET /api/v1/machines/{machineId}.
func (s *server) GetMachine(ctx context.Context, r gen.GetMachineRequestObject) (gen.GetMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.GetMachinedefaultJSONResponse](), nil
	}
	v, err := s.opts.Machines.Get(ctx, r.MachineId)
	if err != nil {
		return fail[gen.GetMachinedefaultJSONResponse](s, err), nil
	}
	return gen.GetMachine200JSONResponse(machineToAPI(v, time.Now())), nil
}

// UpdateMachine implements PATCH /api/v1/machines/{machineId} (machines R5.22 to R5.26).
func (s *server) UpdateMachine(ctx context.Context, r gen.UpdateMachineRequestObject) (gen.UpdateMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.UpdateMachinedefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.UpdateMachinedefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	b := r.Body
	u := machines.SettingsUpdate{
		Name: b.Name, BudgetMemoryPct: b.BudgetMemoryPct, BudgetCPUPct: b.BudgetCpuPct, JobSlots: b.JobSlots,
		LightSlots: b.LightSlots, HeavySlots: b.HeavySlots, LightJobs: b.LightJobs, HeavyJobs: b.HeavyJobs,
		CacheCapBytes: b.CacheCapBytes, DrainTimeoutMinutes: b.DrainTimeoutMinutes, WindowsSymlinkRight: b.WindowsSymlinkRight,
		PoolMemoryGiB: b.PoolMemoryGib, PoolCPUs: b.PoolCpus,
		Confirm: b.Confirm != nil && *b.Confirm, ConfirmShrink: b.ConfirmShrink != nil && *b.ConfirmShrink,
	}
	if b.GateOnTimeout != nil {
		u.GateOnTimeout = machinesPtr(string(*b.GateOnTimeout))
	}
	if b.Mode != nil {
		u.Mode = machinesPtr(string(*b.Mode))
	}
	u.PauseOnBattery = b.PauseOnBattery
	if b.DockerCompact != nil {
		mode := string(*b.DockerCompact)
		if mode == string(gen.DockerCompactModeAuto) {
			mode = machines.CompactAuto
		}
		u.CompactMode = &mode
	}
	u.CompactThresholdGiB, u.CompactIdleMinutes = b.DockerCompactThresholdGib, b.DockerCompactIdleMinutes
	if b.CpuCapPct != nil {
		u.CPUCapPct = machinesPtr(int(*b.CpuCapPct))
	}
	if b.Pause != nil {
		u.Pause = machinesPtr(string(*b.Pause))
	}
	if b.AllowedBackends != nil {
		names := make([]string, 0, len(*b.AllowedBackends))
		for _, n := range *b.AllowedBackends {
			names = append(names, string(n))
		}
		u.AllowedBackends = &names
	}
	v, ch, err := s.opts.Machines.UpdateSettings(ctx, s.actor(ctx), r.MachineId, u)
	if err != nil {
		return fail[gen.UpdateMachinedefaultJSONResponse](s, err), nil
	}
	if ch != nil {
		return gen.UpdateMachine202JSONResponse(changeToAPI(*ch)), nil
	}
	return gen.UpdateMachine200JSONResponse(machineToAPI(v, time.Now())), nil
}

// RemoveMachine implements DELETE /api/v1/machines/{machineId} (machines R5.40 to R5.42).
func (s *server) RemoveMachine(ctx context.Context, r gen.RemoveMachineRequestObject) (gen.RemoveMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RemoveMachinedefaultJSONResponse](), nil
	}
	ch, err := s.opts.Machines.Remove(ctx, s.actor(ctx), r.MachineId, r.Params.Confirm != nil && *r.Params.Confirm)
	if err != nil {
		return fail[gen.RemoveMachinedefaultJSONResponse](s, err), nil
	}
	return gen.RemoveMachine202JSONResponse(changeToAPI(ch)), nil
}

// DrainMachine implements POST /api/v1/machines/{machineId}/drain.
func (s *server) DrainMachine(ctx context.Context, r gen.DrainMachineRequestObject) (gen.DrainMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.DrainMachinedefaultJSONResponse](), nil
	}
	reason := ""
	if r.Body != nil && r.Body.Reason != nil {
		reason = *r.Body.Reason
	}
	ch, err := s.opts.Machines.Drain(ctx, s.actor(ctx), r.MachineId, reason)
	if err != nil {
		return fail[gen.DrainMachinedefaultJSONResponse](s, err), nil
	}
	return gen.DrainMachine202JSONResponse(changeToAPI(ch)), nil
}

// RunMachineCommand implements POST /api/v1/machines/{machineId}/command (cli R5.29).
func (s *server) RunMachineCommand(ctx context.Context, r gen.RunMachineCommandRequestObject) (gen.RunMachineCommandResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RunMachineCommanddefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.RunMachineCommanddefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	var timeout time.Duration
	if r.Body.TimeoutSeconds != nil {
		timeout = time.Duration(*r.Body.TimeoutSeconds) * time.Second
	}
	out, err := s.opts.Machines.RunHostCommand(ctx, s.actor(ctx), r.MachineId, r.Body.Command, timeout)
	if err != nil {
		return fail[gen.RunMachineCommanddefaultJSONResponse](s, err), nil
	}
	res := gen.HostCommandResult{Ok: out.OK, ExitCode: out.ExitCode, Output: out.Output}
	if out.Message != "" {
		res.Message = &out.Message
	}
	return gen.RunMachineCommand200JSONResponse(res), nil
}

// ResumeMachine implements POST /api/v1/machines/{machineId}/resume.
func (s *server) ResumeMachine(ctx context.Context, r gen.ResumeMachineRequestObject) (gen.ResumeMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.ResumeMachinedefaultJSONResponse](), nil
	}
	v, err := s.opts.Machines.Resume(ctx, s.actor(ctx), r.MachineId, r.Params.Confirm != nil && *r.Params.Confirm)
	if err != nil {
		return fail[gen.ResumeMachinedefaultJSONResponse](s, err), nil
	}
	return gen.ResumeMachine200JSONResponse(machineToAPI(v, time.Now())), nil
}

// CheckMachinePoolSize implements POST /api/v1/machines/{machineId}/pool-size-check
// (machines R5.58): the dry run behind the console's Review step.
func (s *server) CheckMachinePoolSize(ctx context.Context, r gen.CheckMachinePoolSizeRequestObject) (gen.CheckMachinePoolSizeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.CheckMachinePoolSizedefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.CheckMachinePoolSizedefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	c, err := s.opts.Machines.PoolSizeCheck(ctx, r.MachineId, r.Body.PoolMemoryGib, r.Body.PoolCpus)
	if err != nil {
		return fail[gen.CheckMachinePoolSizedefaultJSONResponse](s, err), nil
	}
	res := c.Result
	out := gen.PoolSizeCheck{Known: c.Known, Ok: c.Known && (res.OK || res.NeedsShrinkConfirm), NeedsShrinkConfirm: c.Shrinks,
		Nearest: nonNil(res.Nearest), CurrentMemoryGib: c.CurMemGiB, CurrentCpus: c.CurCPUs,
		TotalGib: &res.TotalGiB, UsedOutsideGib: &res.UsedOutsideGiB, MemoryBudgetGib: &res.MemBudgetGiB, RoomGib: &res.RoomGiB, CpuBudget: &res.CPUBudget}
	if c.Why != "" {
		out.Why = &c.Why
	}
	if res.Reason != "" {
		out.Reason = &res.Reason
	}
	if res.Short != "" {
		short := gen.PoolSizeCheckShort(res.Short)
		out.Short = &short
	}
	if res.DockerCeilingGiB > 0 {
		out.DockerCeilingGib = &res.DockerCeilingGiB
	}
	if res.Decided != "" {
		out.Decided = &res.Decided
	}
	if c.Known && res.TotalGiB > 0 {
		out.RecommendedHalf = &gen.PoolSizeRecommended{MemoryGib: res.Half.MemGiB, Cpus: res.Half.CPUs}
		out.RecommendedThreeQuarters = &gen.PoolSizeRecommended{MemoryGib: res.ThreeQuarters.MemGiB, Cpus: res.ThreeQuarters.CPUs}
		measured := gen.PoolSizeCheckBasis(res.Basis)
		margin, wslPool, wslSet := poolsize.SafetyMarginGiB, res.WSLPool, res.WSLCapSet
		out.Basis, out.HostSpanHours, out.PresetRoomGib, out.SafetyMarginGib = &measured, &res.HostSpanHours, &res.PresetRoomGiB, &margin
		out.WslPool, out.WslCapSet = &wslPool, &wslSet
		if res.WSLCapSet {
			out.WslCapGib, out.WslCapAbovePc = &res.WSLCapGiB, &res.CapAbovePC
		}
		if res.Basis == poolsize.BasisMeasured {
			out.HostP95Gib, out.MeasuredRoomGib, out.LeavesGib, out.LeavesLessThanHost = &res.HostP95GiB, &res.MeasuredRoomGiB, &res.LeavesGiB, &res.LeavesLessThanHost
		}
	}
	return gen.CheckMachinePoolSize200JSONResponse(out), nil
}

// RedetectMachine implements POST /api/v1/machines/{machineId}/redetect (Check again, machines R5.13).
func (s *server) RedetectMachine(ctx context.Context, r gen.RedetectMachineRequestObject) (gen.RedetectMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RedetectMachinedefaultJSONResponse](), nil
	}
	if err := s.opts.Machines.Redetect(ctx, s.actor(ctx), r.MachineId); err != nil {
		return fail[gen.RedetectMachinedefaultJSONResponse](s, err), nil
	}
	return gen.RedetectMachine202Response{}, nil
}

// RecoverMachine implements POST /api/v1/machines/{machineId}/recovery (operations R5.44).
func (s *server) RecoverMachine(ctx context.Context, r gen.RecoverMachineRequestObject) (gen.RecoverMachineResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RecoverMachinedefaultJSONResponse](), nil
	}
	if err := s.opts.Machines.ArmRecovery(ctx, s.actor(ctx), r.MachineId); err != nil {
		return fail[gen.RecoverMachinedefaultJSONResponse](s, err), nil
	}
	return gen.RecoverMachine202Response{}, nil
}

// GetMachineLoad implements GET /api/v1/machines/{machineId}/load (machines R6.8).
func (s *server) GetMachineLoad(ctx context.Context, r gen.GetMachineLoadRequestObject) (gen.GetMachineLoadResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.GetMachineLoaddefaultJSONResponse](), nil
	}
	var from, to time.Time
	if r.Params.From != nil {
		from = *r.Params.From
	}
	if r.Params.To != nil {
		to = *r.Params.To
	}
	pts, err := s.opts.Machines.LoadHistory(ctx, r.MachineId, from, to)
	if err != nil {
		return fail[gen.GetMachineLoaddefaultJSONResponse](s, err), nil
	}
	out := gen.GetMachineLoad200JSONResponse{}
	out.Items = make([]struct {
		At        time.Time `json:"at"`
		CpuPct    *float32  `json:"cpu_pct,omitempty"`
		MemoryPct *float32  `json:"memory_pct,omitempty"`
	}, 0, len(pts))
	for _, p := range pts {
		item := struct {
			At        time.Time `json:"at"`
			CpuPct    *float32  `json:"cpu_pct,omitempty"`
			MemoryPct *float32  `json:"memory_pct,omitempty"`
		}{At: p.At}
		if p.MemoryPct != nil {
			item.MemoryPct = f32(*p.MemoryPct)
		}
		if p.CPUPct != nil {
			item.CpuPct = f32(*p.CPUPct)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// GetMachineChange implements GET /api/v1/machines/{machineId}/changes/{changeId}.
func (s *server) GetMachineChange(ctx context.Context, r gen.GetMachineChangeRequestObject) (gen.GetMachineChangeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.GetMachineChangedefaultJSONResponse](), nil
	}
	c, err := s.opts.Machines.GetChange(ctx, r.MachineId, r.ChangeId)
	if err != nil {
		return fail[gen.GetMachineChangedefaultJSONResponse](s, err), nil
	}
	return gen.GetMachineChange200JSONResponse(changeToAPI(c)), nil
}

// DecideMachineChange implements POST /api/v1/machines/{machineId}/changes/{changeId} (machines R5.33).
func (s *server) DecideMachineChange(ctx context.Context, r gen.DecideMachineChangeRequestObject) (gen.DecideMachineChangeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.DecideMachineChangedefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.DecideMachineChangedefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	wait := 0
	if r.Body.WaitMinutes != nil {
		wait = *r.Body.WaitMinutes
	}
	c, err := s.opts.Machines.DecideChange(ctx, s.actor(ctx), r.MachineId, r.ChangeId, machines.Decision(r.Body.Decision), wait, r.Body.Confirm != nil && *r.Body.Confirm)
	if err != nil {
		return fail[gen.DecideMachineChangedefaultJSONResponse](s, err), nil
	}
	return gen.DecideMachineChange200JSONResponse(changeToAPI(c)), nil
}

// CreateJoinCode implements POST /api/v1/join-codes (machines R5.3 to R5.6).
func (s *server) CreateJoinCode(ctx context.Context, r gen.CreateJoinCodeRequestObject) (gen.CreateJoinCodeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.CreateJoinCodedefaultJSONResponse](), nil
	}
	var ttl time.Duration
	if r.Body != nil && r.Body.TtlMinutes != nil {
		ttl = time.Duration(*r.Body.TtlMinutes) * time.Minute
	}
	c, err := s.opts.Machines.CreateJoinCode(ctx, s.actor(ctx), ttl)
	if err != nil {
		return fail[gen.CreateJoinCodedefaultJSONResponse](s, err), nil
	}
	return gen.CreateJoinCode201JSONResponse{
		Code: c.Code, JoinCode: codeToAPI(c.JoinCode, c.JoinCode.State(time.Now())), Commands: installToAPI(s.opts.Machines, c.Code),
	}, nil
}

// GetJoinCode implements GET /api/v1/join-codes/{joinCodeId}: the state, never the value.
func (s *server) GetJoinCode(ctx context.Context, r gen.GetJoinCodeRequestObject) (gen.GetJoinCodeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.GetJoinCodedefaultJSONResponse](), nil
	}
	c, st, err := s.opts.Machines.GetJoinCode(ctx, r.JoinCodeId)
	if err != nil {
		return fail[gen.GetJoinCodedefaultJSONResponse](s, err), nil
	}
	out := codeToAPI(c, st)
	if st == machines.CodeJoined && c.MachineID != "" {
		// Ready from the machine's first heartbeat (RES3).
		if v, err := s.opts.Machines.Get(ctx, c.MachineID); err == nil && v.Machine.Online {
			out.Progress = machinesPtr(gen.JoinCodeProgressReady)
		}
	}
	return gen.GetJoinCode200JSONResponse(out), nil
}

// RevokeJoinCode implements DELETE /api/v1/join-codes/{joinCodeId}.
func (s *server) RevokeJoinCode(ctx context.Context, r gen.RevokeJoinCodeRequestObject) (gen.RevokeJoinCodeResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RevokeJoinCodedefaultJSONResponse](), nil
	}
	if err := s.opts.Machines.RevokeJoinCode(ctx, s.actor(ctx), r.JoinCodeId); err != nil {
		return fail[gen.RevokeJoinCodedefaultJSONResponse](s, err), nil
	}
	return gen.RevokeJoinCode204Response{}, nil
}

// GetInstallCommand implements GET /api/v1/install-command: no join code in it.
func (s *server) GetInstallCommand(_ context.Context, _ gen.GetInstallCommandRequestObject) (gen.GetInstallCommandResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.GetInstallCommanddefaultJSONResponse](), nil
	}
	return gen.GetInstallCommand200JSONResponse(installToAPI(s.opts.Machines, "")), nil
}

// GetCaCertificate implements GET /api/v1/ca.pem: the CA certificate, public data.
func (s *server) GetCaCertificate(_ context.Context, _ gen.GetCaCertificateRequestObject) (gen.GetCaCertificateResponseObject, error) {
	if s.opts.CA == nil {
		return machinesUnavailable[gen.GetCaCertificatedefaultJSONResponse](), nil
	}
	pem := s.opts.CA.CAPEM()
	return gen.GetCaCertificate200ApplicationxPemFileResponse{Body: bytes.NewReader(pem), ContentLength: int64(len(pem))}, nil
}

// JoinMachineOverSsh implements POST /api/v1/join-codes/ssh (machines R5.3a, RES3).
// The key file is passed on once and not kept; nothing here logs the body.
func (s *server) JoinMachineOverSsh(ctx context.Context, r gen.JoinMachineOverSshRequestObject) (gen.JoinMachineOverSshResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.JoinMachineOverSshdefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.JoinMachineOverSshdefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	b := r.Body
	in := machines.SSHJoinInput{OS: string(b.Os), SSHTarget: machines.SSHTarget{Host: b.Host, User: b.User}}
	if b.Identity != nil {
		in.Identity = []byte(*b.Identity)
	}
	if b.Port != nil {
		in.Port = *b.Port
	}
	if b.HostFingerprint != nil {
		in.HostFingerprint = *b.HostFingerprint
	}
	c, err := s.opts.Machines.JoinOverSSH(ctx, s.actor(ctx), in)
	if err != nil {
		return fail[gen.JoinMachineOverSshdefaultJSONResponse](s, err), nil
	}
	return gen.JoinMachineOverSsh201JSONResponse(codeToAPI(c.JoinCode, c.JoinCode.State(time.Now()))), nil
}

// RemoveRunnerLeftovers implements POST /api/v1/machines/{machineId}/runner-leftovers/remove
// (doctor R5.48, OPS33).
func (s *server) RemoveRunnerLeftovers(ctx context.Context, r gen.RemoveRunnerLeftoversRequestObject) (gen.RemoveRunnerLeftoversResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.RemoveRunnerLeftoversdefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.RemoveRunnerLeftoversdefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	refs := make([]leftovers.Ref, 0, len(r.Body.Items))
	for _, it := range r.Body.Items {
		refs = append(refs, leftovers.Ref{Kind: leftovers.Kind(it.Kind), Name: it.Name})
	}
	res, err := s.opts.Machines.RemoveRunnerLeftovers(ctx, s.actor(ctx), r.MachineId, refs)
	if err != nil {
		return fail[gen.RemoveRunnerLeftoversdefaultJSONResponse](s, err), nil
	}
	out := gen.RemoveRunnerLeftoversResult{Results: make([]leftoverResultRow, 0, len(res))}
	for _, x := range res {
		row := leftoverResultRow{
			Kind: gen.RunnerLeftoverKind(x.Kind), Name: x.Name, Outcome: gen.RemoveRunnerLeftoversResultResultsOutcome(x.Outcome),
			Steps: append([]string{}, x.Steps...),
		}
		if x.Error != "" {
			e := x.Error
			row.Error = &e
		}
		out.Results = append(out.Results, row)
	}
	return gen.RemoveRunnerLeftovers200JSONResponse(out), nil
}

// leftoverResultRow is one element of the generated RemoveRunnerLeftoversResult.
type leftoverResultRow = struct {
	Error   *string                                       `json:"error,omitempty"`
	Kind    gen.RunnerLeftoverKind                        `json:"kind"`
	Name    string                                        `json:"name"`
	Outcome gen.RemoveRunnerLeftoversResultResultsOutcome `json:"outcome"`
	Steps   []string                                      `json:"steps"`
}

// StartDockerCompact implements POST /api/v1/machines/{machineId}/docker-compact
// (caches R5.58): the owner's Compact now.
func (s *server) StartDockerCompact(ctx context.Context, r gen.StartDockerCompactRequestObject) (gen.StartDockerCompactResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.StartDockerCompactdefaultJSONResponse](), nil
	}
	confirm := r.Params.Confirm != nil && *r.Params.Confirm
	if err := s.opts.Machines.StartCompact(ctx, s.actor(ctx), r.MachineId, confirm); err != nil {
		return fail[gen.StartDockerCompactdefaultJSONResponse](s, err), nil
	}
	return gen.StartDockerCompact202JSONResponse{Started: true}, nil
}
