package depsetup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeLink struct {
	mu       sync.Mutex
	report   *agentv1.DepsReport
	checks   int
	installs []*agentv1.DepsInstall
	undos    []*agentv1.DepsUndo
	restarts int
	err      error
}

func (f *fakeLink) SendCheck(context.Context, string, string, string) (*agentv1.DepsReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.report, f.err
}
func (f *fakeLink) SendInstall(_ context.Context, _ string, in *agentv1.DepsInstall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs = append(f.installs, in)
	return f.err
}
func (f *fakeLink) SendUndo(_ context.Context, _ string, in *agentv1.DepsUndo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undos = append(f.undos, in)
	return f.err
}
func (f *fakeLink) SendRestart(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.err
}
func (f *fakeLink) SendStop(context.Context, string, string) error { return errors.New("no stop") }

func (f *fakeLink) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks + len(f.installs) + len(f.undos) + f.restarts
}

type fakeAudit struct{ rows []string }

func (a *fakeAudit) Audit(_ context.Context, actor, action, machineID string, d map[string]any) error {
	a.rows = append(a.rows, fmt.Sprintf("%s|%s|%s|%v", actor, action, machineID, d))
	return nil
}

type fakeMachines struct{ m MachineInfo }

func (f *fakeMachines) Machine(_ context.Context, id string) (MachineInfo, error) {
	if id != f.m.ID {
		return MachineInfo{}, machines.ErrNotFound
	}
	return f.m, nil
}

type rig struct {
	svc  *Service
	link *fakeLink
	repo *MemRepo
	aud  *fakeAudit
	mach *fakeMachines
	now  time.Time
}

func newRig(t *testing.T, rp *agentv1.DepsReport) *rig {
	t.Helper()
	r := &rig{link: &fakeLink{report: rp}, repo: NewMemRepo(), aud: &fakeAudit{}, now: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC),
		mach: &fakeMachines{m: MachineInfo{ID: "m1", Name: "pc-1", OS: "windows", Role: "agent", Online: true}}}
	n := 0
	r.svc = NewService(Options{Link: r.link, Repo: r.repo, Audit: r.aud, Machines: r.mach, List: testList(), Now: func() time.Time { return r.now },
		NewID: func() string { n++; return fmt.Sprintf("dep_%d", n) }})
	return r
}

func req() StartRequest {
	return StartRequest{MachineID: "m1", Actor: "luis", RunnerOS: RunnerLinux, RunnerKind: "persistent", ChoiceID: ItemDockerWSL2, Confirmed: true}
}

func TestStartNeedsConfirmationAndCallsNothing(t *testing.T) {
	r := newRig(t, rep("windows", true))
	q := req()
	q.Confirmed = false
	_, err := r.svc.Start(context.Background(), q)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) {
		t.Fatalf("err %v", err)
	}
	if r.link.calls() != 0 || len(r.aud.rows) != 0 {
		t.Fatalf("DEP4: calls %d audit %v", r.link.calls(), r.aud.rows)
	}
	if js, _ := r.svc.List(context.Background(), "m1"); len(js) != 0 {
		t.Fatalf("a job was written: %v", js)
	}
}

func TestStartAuditsConsentAndSendsInstall(t *testing.T) {
	r := newRig(t, rep("windows", true))
	j, err := r.svc.Start(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if j.State != JobConsented || strings.Join(j.ItemIDs, ",") != "wsl2,docker-in-wsl2" || len(r.link.installs) != 1 {
		t.Fatalf("%+v installs %d", j, len(r.link.installs))
	}
	in := r.link.installs[0]
	if in.JobId != j.ID || in.ResumeFrom != "" || in.MachineName != "pc-1" || in.RunnerOs != "linux" || strings.Join(in.ItemIds, ",") != "wsl2,docker-in-wsl2" {
		t.Fatalf("install %+v", in)
	}
	if len(r.aud.rows) != 1 {
		t.Fatalf("audit %v", r.aud.rows)
	}
	row := r.aud.rows[0]
	for _, want := range []string{"luis|dependency.consent|m1", "learn.microsoft.com", "download.docker.com", "docker-in-wsl2", "WSL2 inbox", "pc-1", "2026-11-25T10:00:00Z"} {
		if !strings.Contains(row, want) {
			t.Errorf("audit row lacks %q: %s", want, row)
		}
	}
	// A second job on the same machine is refused while one runs.
	if _, err := r.svc.Start(context.Background(), req()); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("second job: %v", err)
	}
}

func TestStartRefusals(t *testing.T) {
	// Offline machine: DEP12, no check and no install.
	r := newRig(t, rep("windows", true))
	r.mach.m.Online = false
	if _, err := r.svc.Start(context.Background(), req()); !errors.Is(err, machines.ErrConflict) || !strings.Contains(err.Error(), "crsp cannot reach pc-1") {
		t.Fatalf("offline start: %v", err)
	}
	if _, err := r.svc.Check(context.Background(), "m1", RunnerLinux, "persistent"); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("offline check: %v", err)
	}
	if r.link.calls() != 0 {
		t.Fatalf("calls to an offline machine: %d", r.link.calls())
	}
	// Ready machine, DEP11 and DEP16: nothing changes.
	r = newRig(t, rep("windows", true, ItemDockerWSL2, StateOK))
	if _, err := r.svc.Start(context.Background(), req()); !errors.Is(err, machines.ErrConflict) || len(r.link.installs) != 0 || len(r.aud.rows) != 0 {
		t.Fatalf("ready: %v", err)
	}
	c, err := r.svc.Check(context.Background(), "m1", RunnerLinux, "persistent")
	if err != nil || !c.Ready || len(c.Choices) != 0 || c.MachineID != "m1" {
		t.Fatalf("second run: %+v %v", c, err)
	}
	// An item the fresh check did not offer.
	r = newRig(t, rep("windows", true))
	q := req()
	q.ChoiceID = ItemDockerEngine
	if _, err := r.svc.Start(context.Background(), q); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("foreign choice: %v", err)
	}
	// Docker Desktop needs the licence tick.
	q.ChoiceID = ItemDockerDesktop
	if _, err := r.svc.Start(context.Background(), q); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("licence: %v", err)
	}
	q.AcceptLicence = true
	if j, err := r.svc.Start(context.Background(), q); err != nil || !j.AcceptLicence || !r.link.installs[0].AcceptLicence {
		t.Fatalf("licence ticked: %v %+v", err, j)
	}
	// Blocked (no admin): refused, only manual steps.
	r = newRig(t, rep("linux", false))
	r.mach.m.OS = "linux"
	q = req()
	q.ChoiceID = ItemDockerEngine
	if _, err := r.svc.Start(context.Background(), q); !errors.Is(err, machines.ErrConflict) || len(r.link.installs) != 0 {
		t.Fatalf("no admin: %v", err)
	}
}

func final(job, outcome string, added ...*agentv1.DepsAdded) *agentv1.DepsEvent {
	return &agentv1.DepsEvent{JobId: job, Phase: PhaseInstall, Final: true, Outcome: outcome, Added: added, Ts: 1}
}

var addedWSL = &agentv1.DepsAdded{ItemId: ItemWSL2, Kind: "wsl-feature", Ref: "Microsoft-Windows-Subsystem-Linux", Version: "inbox"}

func TestEventsDriveTheStateMachine(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseDownload, PhaseCount: 4, Stream: "status", Line: "downloading"})
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobInstalling || g.Last == nil || len(g.Log) != 1 {
		t.Fatalf("%+v", g)
	}
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseVerify})
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobVerifying {
		t.Fatalf("%s", g.State)
	}
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhasePrepare})
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobPreparing {
		t.Fatalf("%s", g.State)
	}
	d := final(j.ID, OutcomeDone, addedWSL)
	d.EngineVersion = "27.3.1"
	r.svc.HandleEvent(ctx, "m1", d)
	g, _ := r.svc.Get(ctx, "m1", j.ID)
	if g.State != JobDone || g.EngineVersion != "27.3.1" || len(g.Added) != 1 || g.Added[0].Ref != "Microsoft-Windows-Subsystem-Linux" {
		t.Fatalf("%+v", g)
	}
	// A late event of an ended job changes nothing; an event for another machine's job is ignored.
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseInstall})
	r.svc.HandleEvent(ctx, "other", final(j.ID, OutcomeFailed))
	if g2, _ := r.svc.Get(ctx, "m1", j.ID); g2.State != JobDone || len(g2.Log) != len(g.Log) {
		t.Fatalf("late event changed the job: %+v", g2)
	}
	// A failure keeps the reason.
	r = newRig(t, rep("windows", true))
	j, _ = r.svc.Start(ctx, req())
	f := final(j.ID, OutcomeFailed)
	f.Error = "the file did not match; nothing was installed"
	r.svc.HandleEvent(ctx, "m1", f)
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobFailed || g.Error != f.Error {
		t.Fatalf("%+v", g)
	}
}

func TestLogKeepsTheLast300(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	for i := 0; i < 350; i++ {
		r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseInstall, Line: fmt.Sprint(i), Ts: int64(i)})
	}
	g, _ := r.svc.Get(ctx, "m1", j.ID)
	if len(g.Log) != LogCap || g.Log[0].Line != "50" || g.Log[299].Line != "349" {
		t.Fatalf("%d first %q", len(g.Log), g.Log[0].Line)
	}
}

func TestPausedJobResumesAtVerifyAfterRestart(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	r.svc.HandleEvent(ctx, "m1", final(j.ID, OutcomePausedRestart, addedWSL))
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobPausedRestart {
		t.Fatalf("%s", g.State)
	}
	// A controller restart: a new service over the same persisted record.
	n := NewService(Options{Link: r.link, Repo: r.repo, Audit: r.aud, Machines: r.mach, List: testList(), Now: func() time.Time { return r.now }})
	n.OnAgentConnected("m1")
	if len(r.link.installs) != 2 {
		t.Fatalf("installs %d", len(r.link.installs))
	}
	res := r.link.installs[1]
	if res.ResumeFrom != PhaseInstall || res.JobId != j.ID || len(res.Added) != 1 || res.Added[0].Ref != "Microsoft-Windows-Subsystem-Linux" || strings.Join(res.ItemIds, ",") != "wsl2,docker-in-wsl2" {
		t.Fatalf("resume %+v", res)
	}
	if g, _ := n.Get(ctx, "m1", j.ID); g.State != JobVerifying {
		t.Fatalf("%s", g.State)
	}
	n.OnAgentConnected("m1") // a second report: not sent twice
	if len(r.link.installs) != 2 {
		t.Fatalf("resumed twice: %d", len(r.link.installs))
	}
	n.HandleEvent(ctx, "m1", final(j.ID, OutcomeDone))
	if g, _ := n.Get(ctx, "m1", j.ID); g.State != JobDone || len(g.Added) != 1 {
		t.Fatalf("%+v", g)
	}
}

func TestLostLinkResumesOnlyAfterSilence(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseInstall})
	r.svc.OnAgentConnected("m1") // the agent was talking a moment ago: a blip
	if len(r.link.installs) != 1 {
		t.Fatalf("resumed a talking agent: %d", len(r.link.installs))
	}
	r.now = r.now.Add(5 * time.Minute)
	r.svc.OnAgentConnected("m1")
	if len(r.link.installs) != 2 || r.link.installs[1].ResumeFrom != PhaseInstall {
		t.Fatalf("installs %d", len(r.link.installs))
	}
}

func TestRestartHostNeverWithRunningJobs(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	if _, err := r.svc.RestartHost(ctx, "luis", "m1", j.ID, true); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("not paused: %v", err)
	}
	r.svc.HandleEvent(ctx, "m1", final(j.ID, OutcomePausedRestart, addedWSL))
	var nc *machines.NeedsConfirmationError
	if _, err := r.svc.RestartHost(ctx, "luis", "m1", j.ID, false); !errors.As(err, &nc) || r.link.restarts != 0 {
		t.Fatalf("unconfirmed: %v", err)
	}
	r.mach.m.RunningJobs = 2
	if _, err := r.svc.RestartHost(ctx, "luis", "m1", j.ID, true); !errors.Is(err, machines.ErrConflict) || !strings.Contains(err.Error(), "drain") || r.link.restarts != 0 {
		t.Fatalf("running jobs: %v restarts %d", err, r.link.restarts)
	}
	r.mach.m.RunningJobs = 0
	g, err := r.svc.RestartHost(ctx, "luis", "m1", j.ID, true)
	if err != nil || r.link.restarts != 1 || !g.RestartSent || g.State != JobPausedRestart {
		t.Fatalf("%v %+v", err, g)
	}
	if got := r.aud.rows[len(r.aud.rows)-1]; !strings.Contains(got, "dependency.restart") {
		t.Fatalf("audit %s", got)
	}
}

func TestUndoSendsOnlyTheRecordedList(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	j, _ := r.svc.Start(ctx, req())
	if _, err := r.svc.Undo(ctx, "luis", "m1", j.ID, true); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("undo of a running job: %v", err)
	}
	r.svc.HandleEvent(ctx, "m1", final(j.ID, OutcomeDone, addedWSL, &agentv1.DepsAdded{ItemId: ItemDockerWSL2, Kind: "wsl-distro", Ref: "crsp-docker", Version: "apt"}))
	var nc *machines.NeedsConfirmationError
	if _, err := r.svc.Undo(ctx, "luis", "m1", j.ID, false); !errors.As(err, &nc) || len(nc.Names) != 2 || len(r.link.undos) != 0 {
		t.Fatalf("unconfirmed: %v %+v", err, nc)
	}
	g, err := r.svc.Undo(ctx, "luis", "m1", j.ID, true)
	if err != nil || g.State != JobUndoing || len(r.link.undos) != 1 {
		t.Fatalf("%v %+v", err, g)
	}
	u := r.link.undos[0]
	if len(u.Added) != 2 || u.Added[0].Ref != "Microsoft-Windows-Subsystem-Linux" || u.Added[1].Ref != "crsp-docker" || u.JobId != j.ID {
		t.Fatalf("undo %+v", u)
	}
	if got := r.aud.rows[len(r.aud.rows)-1]; !strings.Contains(got, "dependency.undo") {
		t.Fatalf("audit %s", got)
	}
	r.svc.HandleEvent(ctx, "m1", &agentv1.DepsEvent{JobId: j.ID, Phase: PhaseUndo})
	r.svc.HandleEvent(ctx, "m1", final(j.ID, OutcomeDone))
	if g, _ := r.svc.Get(ctx, "m1", j.ID); g.State != JobUndone {
		t.Fatalf("%s", g.State)
	}
	// A job that added nothing has nothing to undo.
	r = newRig(t, rep("windows", true))
	j2, _ := r.svc.Start(ctx, req())
	r.svc.HandleEvent(ctx, "m1", final(j2.ID, OutcomeFailed))
	if _, err := r.svc.Undo(ctx, "luis", "m1", j2.ID, true); !errors.Is(err, machines.ErrConflict) || len(r.link.undos) != 0 {
		t.Fatalf("empty undo: %v", err)
	}
}

func TestListNewestFirstMax20(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, rep("windows", true))
	for i := 0; i < 25; i++ {
		r.now = r.now.Add(time.Minute)
		_ = r.repo.Put(ctx, Job{ID: fmt.Sprintf("j%02d", i), MachineID: "m1", State: JobDone, CreatedAt: r.now})
	}
	js, err := r.svc.List(ctx, "m1")
	if err != nil || len(js) != 20 || js[0].ID != "j24" || js[19].ID != "j05" {
		t.Fatalf("%v %d", err, len(js))
	}
	if _, err := r.svc.Get(ctx, "m1", "nope"); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestResumePhase(t *testing.T) {
	for plan, want := range map[string]string{"wsl2,docker-in-wsl2": PhaseInstall, "docker-in-wsl2": PhaseInstall, "wsl2": PhaseVerify, "docker-desktop": PhaseVerify, "wsl2,docker-desktop": PhaseVerify} {
		if got := resumePhase(strings.Split(plan, ",")); got != want {
			t.Errorf("%s: %s, want %s", plan, got, want)
		}
	}
	// A wsl2-only job resumes at verify end to end.
	ctx := context.Background()
	r := newRig(t, rep("windows", true, ItemDockerDesktop, StateOK))
	_ = r.repo.Put(ctx, Job{ID: "j", MachineID: "m1", State: JobPausedRestart, ItemIDs: []string{ItemWSL2}, CreatedAt: r.now, UpdatedAt: r.now})
	r.svc.OnAgentConnected("m1")
	if len(r.link.installs) != 1 || r.link.installs[0].ResumeFrom != PhaseVerify {
		t.Fatalf("%+v", r.link.installs)
	}
}
