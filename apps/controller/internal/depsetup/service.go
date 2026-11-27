package depsetup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// Link carries the dependency commands to a machine's agent (agentlink.Link).
// Install, undo, restart and stop are sent and not waited for: the agent
// answers with DepsEvent messages, which reach HandleEvent.
type Link interface {
	SendCheck(ctx context.Context, machineID, runnerOS, runnerKind string) (*agentv1.DepsReport, error)
	SendInstall(ctx context.Context, machineID string, in *agentv1.DepsInstall) error
	SendUndo(ctx context.Context, machineID string, in *agentv1.DepsUndo) error
	SendRestart(ctx context.Context, machineID, jobID string) error
	SendStop(ctx context.Context, machineID, jobID string) error
}

// Audit writes one audit row (DEP4, DEP10).
type Audit interface {
	Audit(ctx context.Context, actor, action, machineID string, details map[string]any) error
}

// MachineInfo is what the service needs to know of a machine.
type MachineInfo struct {
	ID, Name, OS, Role string
	Online             bool
	RunningJobs        int
}

// Machines reads a machine. A removed or unknown machine is machines.ErrNotFound.
type Machines interface {
	Machine(ctx context.Context, id string) (MachineInfo, error)
}

type machineGetter interface {
	Get(ctx context.Context, id string) (machines.View, error)
}

// MachinesFrom adapts the machines service.
func MachinesFrom(g machineGetter) Machines { return machinesAdapter{g} }

type machinesAdapter struct{ g machineGetter }

func (a machinesAdapter) Machine(ctx context.Context, id string) (MachineInfo, error) {
	v, err := a.g.Get(ctx, id)
	if err != nil {
		return MachineInfo{}, err
	}
	m := v.Machine
	if !m.RemovedAt.IsZero() {
		return MachineInfo{}, machines.ErrNotFound
	}
	role := "agent"
	if m.IsController {
		role = "controller"
	}
	return MachineInfo{ID: m.ID, Name: m.Name, OS: m.OS, Role: role, Online: m.Online, RunningJobs: len(v.RunningJobs)}, nil
}

// Options build a Service.
type Options struct {
	Link     Link
	Repo     Repo
	Audit    Audit
	Machines Machines
	List     List
	Now      func() time.Time
	NewID    func() string
	// Log is optional; it takes a message and key/value pairs.
	Log func(msg string, kv ...any)
	// Silence is how long a running job may hear nothing before a returning
	// agent resumes it (default 90 seconds).
	Silence time.Duration
}

// Service runs dependency setup jobs.
type Service struct {
	o  Options
	mu sync.Mutex
}

// NewService returns a Service.
func NewService(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = func() string {
			var b [8]byte
			_, _ = rand.Read(b[:])
			return "dep_" + hex.EncodeToString(b[:])
		}
	}
	if o.Silence == 0 {
		o.Silence = 90 * time.Second
	}
	return &Service{o: o}
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

func (s *Service) logf(msg string, kv ...any) {
	if s.o.Log != nil {
		s.o.Log(msg, kv...)
	}
}

func conflict(format string, a ...any) error {
	return fmt.Errorf("%w: %s", machines.ErrConflict, fmt.Sprintf(format, a...))
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", machines.ErrInvalid, fmt.Sprintf(format, a...))
}

func validRunner(os, kind string) error {
	switch os {
	case RunnerLinux, RunnerWindows, RunnerMacOS:
	default:
		return invalid("the runner OS must be linux, windows or macos")
	}
	if kind != "ephemeral" && kind != "persistent" {
		return invalid("the runner kind must be ephemeral or persistent")
	}
	return nil
}

// reachable returns the machine when it is online; an offline machine is not
// offered an install (DEP12).
func (s *Service) reachable(ctx context.Context, id string) (MachineInfo, error) {
	m, err := s.o.Machines.Machine(ctx, id)
	if err != nil {
		return m, err
	}
	if !m.Online {
		return m, conflict("crsp cannot reach %s", m.Name)
	}
	return m, nil
}

// Check asks the machine's agent what the runner choice needs and builds the offer (DEP1 to DEP3, DEP16).
func (s *Service) Check(ctx context.Context, machineID, runnerOS, runnerKind string) (Check, error) {
	if err := validRunner(runnerOS, runnerKind); err != nil {
		return Check{}, err
	}
	m, err := s.reachable(ctx, machineID)
	if err != nil {
		return Check{}, err
	}
	rep, err := s.o.Link.SendCheck(ctx, machineID, runnerOS, runnerKind)
	if err != nil {
		return Check{}, conflict("%v", err)
	}
	if rep.GetError() != "" {
		return Check{}, conflict("crsp could not read %s: %s", m.Name, rep.GetError())
	}
	c := BuildOffer(rep, runnerOS, runnerKind, s.o.List)
	c.MachineID = machineID
	return c, nil
}

// StartRequest is the consented request of the Review step.
type StartRequest struct {
	MachineID, Actor, RunnerOS, RunnerKind, ChoiceID string
	Confirmed, AcceptLicence                         bool
}

// Start begins a job. Without Confirmed it changes nothing and sends nothing to
// the agent (DEP4). The role check is the API's.
func (s *Service) Start(ctx context.Context, r StartRequest) (Job, error) {
	if err := validRunner(r.RunnerOS, r.RunnerKind); err != nil {
		return Job{}, err
	}
	if r.ChoiceID == "" {
		return Job{}, invalid("choose what to install")
	}
	if !r.Confirmed {
		return Job{}, &machines.NeedsConfirmationError{Message: "Nothing has changed yet. Say yes to install the items you chose.", Names: []string{r.ChoiceID}}
	}
	m, err := s.reachable(ctx, r.MachineID)
	if err != nil {
		return Job{}, err
	}
	if err := s.noRunning(ctx, r.MachineID, m.Name); err != nil {
		return Job{}, err
	}
	// The machine's own fresh check decides what may be installed (DEP16).
	rep, err := s.o.Link.SendCheck(ctx, r.MachineID, r.RunnerOS, r.RunnerKind)
	if err != nil {
		return Job{}, conflict("%v", err)
	}
	if rep.GetError() != "" {
		return Job{}, conflict("crsp could not read %s: %s", m.Name, rep.GetError())
	}
	chk := BuildOffer(rep, r.RunnerOS, r.RunnerKind, s.o.List)
	switch {
	case chk.Ready:
		return Job{}, conflict("nothing is missing on %s for this runner; nothing was changed", m.Name)
	case chk.StartOnly:
		return Job{}, conflict("the engine on %s is installed but stopped; start it instead of installing", m.Name)
	}
	ch, ok := chk.ChoiceByID(r.ChoiceID)
	if !ok {
		return Job{}, conflict("%s is not something this machine needs for this runner", r.ChoiceID)
	}
	if b := chk.ChoiceBlock(ch.ID); b != "" {
		return Job{}, conflict("%s", b)
	}
	if ch.RequiresLicenceTick && !r.AcceptLicence {
		return Job{}, invalid("tick that you have the right to use Docker Desktop before it is installed")
	}
	j := Job{ID: s.o.NewID(), MachineID: r.MachineID, State: JobConsented, ChoiceID: ch.ID, ItemIDs: ch.ItemIDs,
		RunnerOS: r.RunnerOS, RunnerKind: r.RunnerKind, Actor: r.Actor, AcceptLicence: r.AcceptLicence,
		CreatedAt: s.now(), UpdatedAt: s.now(), ManualSteps: ch.ManualSteps, Added: []Added{}, Log: []Event{}}
	s.mu.Lock()
	if err := s.noRunning(ctx, r.MachineID, m.Name); err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	if err := s.o.Repo.Put(ctx, j); err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	s.mu.Unlock()

	var items, sources []string
	for _, f := range ch.Facts {
		items = append(items, f.Name+" "+f.Version)
		sources = append(sources, f.Source)
	}
	_ = s.o.Audit.Audit(ctx, r.Actor, "dependency.consent", r.MachineID, map[string]any{
		"job": j.ID, "machine": m.Name, "item_ids": ch.ItemIDs, "items": items, "source_hosts": sources,
		"runner_os": r.RunnerOS, "runner_kind": r.RunnerKind, "accept_licence": r.AcceptLicence, "at": s.now().Format(time.RFC3339)})
	if err := s.o.Link.SendInstall(ctx, r.MachineID, s.installMsg(j, m, "")); err != nil {
		j.State, j.Error, j.UpdatedAt = JobFailed, fmt.Sprintf("crsp could not send the install to %s: %v", m.Name, err), s.now()
		_ = s.o.Repo.Put(ctx, j)
		return j, conflict("crsp cannot reach %s: %v", m.Name, err)
	}
	return j, nil
}

func (s *Service) noRunning(ctx context.Context, machineID, name string) error {
	run, err := s.o.Repo.Unfinished(ctx, machineID)
	if err != nil {
		return err
	}
	if len(run) > 0 {
		return conflict("another setup is already running on %s; wait for it to end", name)
	}
	return nil
}

func (s *Service) installMsg(j Job, m MachineInfo, resumeFrom string) *agentv1.DepsInstall {
	in := &agentv1.DepsInstall{CommandId: "", JobId: j.ID, ItemIds: j.ItemIDs, AcceptLicence: j.AcceptLicence, ResumeFrom: resumeFrom,
		RunnerOs: j.RunnerOS, RunnerKind: j.RunnerKind, MachineName: m.Name, MachineRole: m.Role}
	for _, a := range j.Added {
		in.Added = append(in.Added, &agentv1.DepsAdded{ItemId: a.ItemID, Kind: a.Kind, Ref: a.Ref, Version: a.Version})
	}
	return in
}

// Get returns one job of a machine.
func (s *Service) Get(ctx context.Context, machineID, jobID string) (Job, error) {
	if _, err := s.o.Machines.Machine(ctx, machineID); err != nil {
		return Job{}, err
	}
	j, ok, err := s.o.Repo.Get(ctx, machineID, jobID)
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, machines.ErrNotFound
	}
	return j, nil
}

// MaxList is how many jobs a list holds.
const MaxList = 20

// List returns the jobs of a machine, newest first, at most 20.
func (s *Service) List(ctx context.Context, machineID string) ([]Job, error) {
	if _, err := s.o.Machines.Machine(ctx, machineID); err != nil {
		return nil, err
	}
	js, err := s.o.Repo.List(ctx, machineID, MaxList)
	if err != nil {
		return nil, err
	}
	if js == nil {
		js = []Job{}
	}
	return js, nil
}

// Stop asks the agent to stop a running job where it is safe. The job ends as
// stopped when the agent's final event says so.
func (s *Service) Stop(ctx context.Context, actor, machineID, jobID string) (Job, error) {
	m, err := s.reachable(ctx, machineID)
	if err != nil {
		return Job{}, err
	}
	j, err := s.Get(ctx, machineID, jobID)
	if err != nil {
		return Job{}, err
	}
	switch j.State {
	case JobConsented, JobInstalling, JobVerifying, JobPreparing, JobUndoing:
	default:
		return Job{}, conflict("this setup is not running (it is %s)", j.State)
	}
	if err := s.o.Link.SendStop(ctx, machineID, jobID); err != nil {
		return Job{}, conflict("%v", err)
	}
	_ = s.o.Audit.Audit(ctx, actor, "dependency.stop", machineID, map[string]any{"job": jobID, "machine": m.Name})
	return j, nil
}

// Undo removes only what the job recorded as added (DEP10, D-DEP-7). Without
// confirmed it answers with the items and sends nothing.
func (s *Service) Undo(ctx context.Context, actor, machineID, jobID string, confirmed bool) (Job, error) {
	j, err := s.Get(ctx, machineID, jobID)
	if err != nil {
		return Job{}, err
	}
	switch j.State {
	case JobDone, JobFailed, JobStopped, JobPausedRestart:
	default:
		return Job{}, conflict("this setup cannot be undone now (it is %s)", j.State)
	}
	if len(j.Added) == 0 {
		return Job{}, conflict("this setup added nothing, so there is nothing to undo")
	}
	if !confirmed {
		names := make([]string, 0, len(j.Added))
		for _, a := range j.Added {
			names = append(names, strings.TrimSpace(a.Ref+" ("+a.Kind+")"))
		}
		return Job{}, &machines.NeedsConfirmationError{Message: "Undo removes only what this setup added. Nothing else on the machine is touched.", Names: names}
	}
	m, err := s.reachable(ctx, machineID)
	if err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	prev := j.State
	run, err := s.o.Repo.Unfinished(ctx, machineID)
	if err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	others := 0
	for _, o := range run {
		if o.ID != j.ID {
			others++
		}
	}
	if others > 0 {
		s.mu.Unlock()
		return Job{}, conflict("another setup is already running on %s; wait for it to end", m.Name)
	}
	j.State, j.UpdatedAt, j.Error = JobUndoing, s.now(), ""
	if err := s.o.Repo.Put(ctx, j); err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	s.mu.Unlock()
	msg := &agentv1.DepsUndo{JobId: j.ID, MachineName: m.Name, MachineRole: m.Role}
	for _, a := range j.Added {
		msg.Added = append(msg.Added, &agentv1.DepsAdded{ItemId: a.ItemID, Kind: a.Kind, Ref: a.Ref, Version: a.Version})
	}
	_ = s.o.Audit.Audit(ctx, actor, "dependency.undo", machineID, map[string]any{"job": j.ID, "machine": m.Name, "added": len(j.Added)})
	if err := s.o.Link.SendUndo(ctx, machineID, msg); err != nil {
		j.State, j.UpdatedAt = prev, s.now()
		_ = s.o.Repo.Put(ctx, j)
		return Job{}, conflict("crsp cannot reach %s: %v", m.Name, err)
	}
	return j, nil
}

// RestartHost restarts the machine after the user chose Restart now (DEP7).
// Never automatic: it refuses a machine with a running job, so the caller drains first.
func (s *Service) RestartHost(ctx context.Context, actor, machineID, jobID string, confirmed bool) (Job, error) {
	j, err := s.Get(ctx, machineID, jobID)
	if err != nil {
		return Job{}, err
	}
	if j.State != JobPausedRestart {
		return Job{}, conflict("this setup is not waiting for a restart (it is %s)", j.State)
	}
	if !confirmed {
		return Job{}, &machines.NeedsConfirmationError{Message: "Restart this machine now? Jobs that are running would be lost, so crsp restarts only a machine with none.", Names: []string{machineID}}
	}
	m, err := s.reachable(ctx, machineID)
	if err != nil {
		return Job{}, err
	}
	if m.RunningJobs > 0 {
		return Job{}, conflict("%s still runs %d job(s); drain it first so they finish, then restart", m.Name, m.RunningJobs)
	}
	_ = s.o.Audit.Audit(ctx, actor, "dependency.restart", machineID, map[string]any{"job": j.ID, "machine": m.Name})
	if err := s.o.Link.SendRestart(ctx, machineID, j.ID); err != nil {
		return Job{}, conflict("crsp cannot reach %s: %v", m.Name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok, _ := s.o.Repo.Get(ctx, machineID, jobID); ok {
		j = cur
	}
	j.RestartSent, j.UpdatedAt = true, s.now()
	if err := s.o.Repo.Put(ctx, j); err != nil {
		return Job{}, err
	}
	return j, nil
}

// HandleEvent takes a live or final event of the agent for a job (DEP6, DEP7).
func (s *Service) HandleEvent(ctx context.Context, machineID string, ev *agentv1.DepsEvent) {
	if ev == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok, err := s.o.Repo.Get(ctx, machineID, ev.GetJobId())
	if err != nil || !ok {
		return
	}
	switch j.State {
	case JobDone, JobUndone, JobFailed, JobStopped:
		return // a late event of an ended job
	}
	e := Event{JobID: ev.GetJobId(), Phase: ev.GetPhase(), PhaseIndex: int(ev.GetPhaseIndex()), PhaseCount: int(ev.GetPhaseCount()),
		PhaseWeight: ev.GetPhaseWeight(), Pct: ev.Pct, Reason: ev.GetReason(), Bytes: ev.GetBytes(), Total: ev.GetTotal(), Rate: ev.GetRate(),
		EtaSec: ev.GetEtaSec(), MachineName: ev.GetMachineName(), MachineRole: ev.GetMachineRole(), MachineOS: ev.GetMachineOs(),
		Command: ev.GetCommand(), Stream: ev.GetStream(), Line: ev.GetLine(), Ts: ev.GetTs()}
	if e.Stream == "" {
		e.Stream = "status"
	}
	j.Log = append(j.Log, e)
	if len(j.Log) > LogCap {
		j.Log = append([]Event(nil), j.Log[len(j.Log)-LogCap:]...)
	}
	j.Last = &e
	j.UpdatedAt = s.now()
	if len(ev.GetAdded()) > 0 {
		j.Added = j.Added[:0]
		for _, a := range ev.GetAdded() {
			j.Added = append(j.Added, Added{ItemID: a.GetItemId(), Kind: a.GetKind(), Ref: a.GetRef(), Version: a.GetVersion()})
		}
	}
	undoing := j.State == JobUndoing
	if !ev.GetFinal() {
		switch ev.GetPhase() {
		case PhaseDownload, PhaseInstall, PhaseRestart:
			j.State = JobInstalling
		case PhaseVerify:
			j.State = JobVerifying
		case PhasePrepare:
			j.State = JobPreparing
		case PhaseUndo:
			j.State = JobUndoing
		default:
			if j.State == JobConsented {
				j.State = JobInstalling
			}
		}
		if undoing {
			j.State = JobUndoing
		}
	} else {
		switch ev.GetOutcome() {
		case OutcomeDone:
			j.State, j.Error = JobDone, ""
			if undoing {
				j.State = JobUndone
			}
			if v := ev.GetEngineVersion(); v != "" {
				j.EngineVersion = v
			}
		case OutcomePausedRestart:
			j.State = JobPausedRestart
		case OutcomeStopped:
			j.State = JobStopped
		default:
			j.State = JobFailed
			j.Error = ev.GetError()
			if j.Error == "" {
				j.Error = "the agent reported a failure without a reason"
			}
		}
	}
	if err := s.o.Repo.Put(ctx, j); err != nil {
		s.logf("dependency job not saved", "job", j.ID, "err", err)
	}
}

// OnAgentConnected resumes the jobs of a machine whose agent is back: a job
// paused for a restart, or running when the link dropped, goes on at Verify
// with its recorded added list (DEP7). The record is persisted, so a controller
// restart resumes the same way.
func (s *Service) OnAgentConnected(machineID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := s.o.Machines.Machine(ctx, machineID)
	if err != nil {
		return
	}
	s.mu.Lock()
	jobs, err := s.o.Repo.Unfinished(ctx, machineID)
	if err != nil {
		s.mu.Unlock()
		s.logf("dependency jobs not read", "machine", machineID, "err", err)
		return
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].CreatedAt.Before(jobs[b].CreatedAt) })
	var resume *Job
	for i := range jobs {
		j := jobs[i]
		switch j.State {
		case JobPausedRestart:
		case JobInstalling, JobVerifying, JobPreparing:
			if s.now().Sub(j.UpdatedAt) < s.o.Silence {
				continue // the agent is still talking: a blip, not a lost run
			}
		default:
			continue
		}
		j.State, j.UpdatedAt = JobVerifying, s.now()
		if err := s.o.Repo.Put(ctx, j); err != nil {
			continue
		}
		resume = &j
		break
	}
	s.mu.Unlock()
	if resume == nil {
		return
	}
	if err := s.o.Link.SendInstall(ctx, machineID, s.installMsg(*resume, m, resumePhase(resume.ItemIDs))); err != nil {
		s.logf("dependency job not resumed", "job", resume.ID, "err", err)
		s.mu.Lock()
		resume.State, resume.UpdatedAt = JobPausedRestart, s.now().Add(-s.o.Silence)
		_ = s.o.Repo.Put(ctx, *resume)
		s.mu.Unlock()
	}
}

// resumePhase is where a job goes on after a restart. Docker in WSL2 is
// installed after the restart, so the install phase runs again (items already
// installed are skipped); a plan of wsl2 or Docker Desktop alone resumes at verify.
func resumePhase(itemIDs []string) string {
	for _, id := range itemIDs {
		if id == ItemDockerWSL2 {
			return PhaseInstall
		}
	}
	return PhaseVerify
}
