package machines_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

const gib = int64(1) << 30

func okInput() machines.CompactInput {
	set := machines.DefaultSettings()
	return machines.CompactInput{
		Now: time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC), OS: "windows", Online: true, State: machines.StateActive, Settings: set,
		Disk:      machines.DockerDisk{Reported: true, Present: true, FileBytes: 236 * gib, UsedKnown: true, UsedBytes: 71 * gib, CanCompact: true},
		IdleSince: time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC),
	}
}

type compactCase struct {
	name   string
	mutate func(*machines.CompactInput)
	want   string
}

func runCompactCases(t *testing.T, cases []compactCase) {
	t.Helper()
	for _, c := range cases {
		in := okInput()
		c.mutate(&in)
		got := machines.DecideCompact(in)
		if got.Reason != c.want || got.Run != (c.want == machines.CompactRun) {
			t.Errorf("%s: got %+v, want %s", c.name, got, c.want)
		}
	}
}

// caches R5.51 to R5.54: the decision, one reason at a time. Gates: setting, state, report, rights.
func TestDecideCompactGates(t *testing.T) {
	runCompactCases(t, []compactCase{
		{"all conditions met", func(*machines.CompactInput) {}, machines.CompactRun},
		{"off by the user", func(in *machines.CompactInput) { in.Settings.CompactMode = machines.CompactOff }, machines.CompactSkipOff},
		{"windows without a disk file is off by default", func(in *machines.CompactInput) { in.Disk.Present = false }, machines.CompactSkipOff},
		{"linux is off by default", func(in *machines.CompactInput) { in.OS = "linux" }, machines.CompactSkipOff},
		{"offline", func(in *machines.CompactInput) { in.Online = false }, machines.CompactSkipOffline},
		{"drained by someone else", func(in *machines.CompactInput) { in.State = machines.StateDrained }, machines.CompactSkipState},
		{"a change is open", func(in *machines.CompactInput) { in.Open = true }, machines.CompactSkipChange},
		{"already compacting", func(in *machines.CompactInput) { in.Compacting = true }, machines.CompactSkipRunning},
		{"no report yet", func(in *machines.CompactInput) { in.Disk = machines.DockerDisk{} }, machines.CompactSkipOff},
	})
}

// Disk size threshold.
func TestDecideCompactThreshold(t *testing.T) {
	runCompactCases(t, []compactCase{
		{"no admin right", func(in *machines.CompactInput) { in.Disk.CanCompact = false }, machines.CompactSkipNoAdmin},
		{"below the default 30 GiB", func(in *machines.CompactInput) { in.Disk.FileBytes = 100 * gib }, machines.CompactSkipBelow},
		{"threshold set by the user", func(in *machines.CompactInput) { in.Settings.CompactThresholdGiB = 200 }, machines.CompactSkipBelow},
		{"used unknown is nothing to reclaim", func(in *machines.CompactInput) { in.Disk.UsedKnown = false }, machines.CompactSkipBelow},
		{"any hour of the day", func(in *machines.CompactInput) { in.Now = time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC) }, machines.CompactRun},
	})
}

// Once per day, running or waiting jobs, and idle time.
func TestDecideCompactBusyAndIdle(t *testing.T) {
	runCompactCases(t, []compactCase{
		{"once per day", func(in *machines.CompactInput) { in.LastAt = in.Now.Add(-23 * time.Hour) }, machines.CompactSkipToday},
		{"a day later", func(in *machines.CompactInput) { in.LastAt = in.Now.Add(-25 * time.Hour) }, machines.CompactRun},
		{"a job runs", func(in *machines.CompactInput) { in.Running = 1 }, machines.CompactSkipBusy},
		{"a job waits", func(in *machines.CompactInput) { in.Queued = 2 }, machines.CompactSkipBusy},
		{"idle for less than 30 minutes", func(in *machines.CompactInput) { in.IdleSince = in.Now.Add(-29 * time.Minute) }, machines.CompactSkipIdle},
		{"idle for exactly 30 minutes", func(in *machines.CompactInput) { in.IdleSince = in.Now.Add(-30 * time.Minute) }, machines.CompactRun},
		{"idle time set by the user", func(in *machines.CompactInput) { in.Settings.CompactIdleMinutes = 90 }, machines.CompactSkipIdle},
		{"idle unknown", func(in *machines.CompactInput) { in.IdleSince = time.Time{} }, machines.CompactSkipIdle},
		{"on forces a Linux machine", func(in *machines.CompactInput) { in.OS = "linux"; in.Settings.CompactMode = machines.CompactOn }, machines.CompactRun},
	})
}

func TestSettingsValidateCompact(t *testing.T) {
	rig := newCompactRig(t)
	bad := func(name string, u machines.SettingsUpdate) {
		t.Helper()
		if _, _, err := rig.svc.UpdateSettings(context.Background(), "owner", rig.id, u); !errors.Is(err, machines.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s := func(v string) *string { return &v }
	n := func(v int) *int { return &v }
	bad("mode", machines.SettingsUpdate{CompactMode: s("sometimes")})
	bad("threshold", machines.SettingsUpdate{CompactThresholdGiB: n(-1)})
	bad("idle", machines.SettingsUpdate{CompactIdleMinutes: n(5000)})
	v, _, err := rig.svc.UpdateSettings(context.Background(), "owner", rig.id, machines.SettingsUpdate{
		CompactMode: s("on"), CompactThresholdGiB: n(50), CompactIdleMinutes: n(45),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := v.Settings
	if st.CompactMode != "on" || st.CompactThresholdGiB != 50 || st.CompactIdleMinutes != 45 {
		t.Fatalf("settings %+v", st)
	}
	if !strings.Contains(rig.audit(), "docker compact") {
		t.Fatalf("audit lacks the settings change: %s", rig.audit())
	}
}

// ---- the maintain path with a fake host ----

type compactRig struct {
	t     *testing.T
	svc   *machines.Service
	repo  *machines.MemRepo
	id    string
	clk   *clock
	host  *compactHost
	note  *compactNotes
	queue *fakeQueue
}

type fakeQueue struct {
	mu    sync.Mutex
	n     int
	repos []string
	err   error
}

func (q *fakeQueue) QueuedFor(context.Context, string) (int, []string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n, q.repos, q.err
}

// compactNotes is a Notifier that stands in for the agent: once the machine
// drains, it reports it drained and closes the change, as the agent does.
type compactNotes struct {
	mu      sync.Mutex
	repo    *machines.MemRepo
	notices []machines.CompactNotice
	// neverDrain keeps the machine draining.
	neverDrain bool
}

func (n *compactNotes) LoadChanged(string) {}
func (n *compactNotes) CompactNotice(_ string, c machines.CompactNotice) {
	n.mu.Lock()
	n.notices = append(n.notices, c)
	n.mu.Unlock()
}
func (n *compactNotes) MachineChanged(id string) {
	ctx := context.Background()
	m, err := n.repo.GetMachine(ctx, id)
	if err != nil || m.State != machines.StateDraining || n.neverDrain {
		return
	}
	m.State = machines.StateDrained
	_ = n.repo.UpdateMachine(ctx, m)
	if c, ok, _ := n.repo.OpenChange(ctx, id); ok {
		c.State = machines.ChangeDone
		_ = n.repo.UpdateChange(ctx, c)
	}
}

// compactHost is the HostRunner: it records the state the machine had when the
// command arrived, and answers like the agent.
type compactHost struct {
	mu     sync.Mutex
	repo   *machines.MemRepo
	id     string
	argv   [][]string
	states []string
	answer machines.HostOutcome
	err    error
	during func()
}

func (h *compactHost) RunHostCommand(ctx context.Context, id string, argv []string, _ time.Duration) (machines.HostOutcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, _ := h.repo.GetMachine(ctx, id)
	h.states = append(h.states, m.State)
	h.argv = append(h.argv, argv)
	if h.during != nil {
		h.during()
	}
	return h.answer, h.err
}

func reportText(before, after int64) string {
	r := dockercompact.Report{Platform: "windows", Files: []dockercompact.DiskFile{{Path: "x.vhdx", Before: before, After: after}}, UsedBefore: 71 * gib, UsedAfter: 71 * gib, DockerRunning: true}
	return r.Text()
}

func newCompactRig(t *testing.T) *compactRig {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)}
	repo := machines.NewMemRepo()
	repo.Now = clk.Now
	notes := &compactNotes{repo: repo}
	svc := machines.New(machines.Options{Repo: repo, Now: clk.Now, ControllerAddress: "pool.lan", AgentPort: 8461, Notify: notes})
	svc.SetCompactPoll(time.Millisecond)
	code, err := svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	f := machines.Facts{HostName: "win-1", OS: "windows", Arch: "x64", Threads: 8, MemoryBytes: 32 << 30}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, Facts: f})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMachine(context.Background(), res.MachineID)
	m.Online, m.LastSeen = true, clk.Now()
	_ = repo.UpdateMachine(context.Background(), m)
	host := &compactHost{repo: repo, id: res.MachineID, answer: machines.HostOutcome{OK: true, Output: reportText(236*gib, 75*gib)}}
	svc.SetHostRunner(host)
	q := &fakeQueue{}
	svc.SetQueueSource(q)
	rig := &compactRig{t: t, svc: svc, repo: repo, id: res.MachineID, clk: clk, host: host, note: notes, queue: q}
	rig.report(true, true)
	return rig
}

func (r *compactRig) report(present, canCompact bool) {
	r.svc.HandleMachineReport(r.id, &agentv1.MachineReport{DockerDisk: &agentv1.DockerDiskReport{
		Present: present, Files: []string{`C:\d\docker_data.vhdx`}, FileBytes: 236 * gib, UsedKnown: true, UsedBytes: 71 * gib, CanCompact: canCompact,
	}})
}

func (r *compactRig) audit() string {
	var b strings.Builder
	for _, a := range r.repo.AuditLog() {
		fmt.Fprintf(&b, "%s %s %s\n", a.Action, a.Subject, a.Detail)
	}
	return b.String()
}

func (r *compactRig) state() string {
	m, _ := r.repo.GetMachine(context.Background(), r.id)
	return m.State
}

// Hold, drain, compact, release: the command meets a drained machine, the
// machine is active again, and the projects get a notice before and after with
// sizes.
func TestCompactMachineHoldsDrainsRunsAndReleases(t *testing.T) {
	r := newCompactRig(t)
	r.queue.repos = []string{"acme/api", "acme/web"}
	out, err := r.svc.CompactMachine(context.Background(), "system", r.id)
	if err != nil || !out.OK || !out.Released {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(r.host.argv) != 1 || r.host.argv[0][0] != "docker-compact" || r.host.states[0] != machines.StateDrained {
		t.Fatalf("host saw %v in states %v", r.host.argv, r.host.states)
	}
	if r.state() != machines.StateActive {
		t.Fatalf("machine left %s", r.state())
	}
	if _, open, _ := r.repo.OpenChange(context.Background(), r.id); open {
		t.Fatal("a change stayed open")
	}
	n := r.note.notices
	if len(n) != 2 || n[0].Phase != machines.NoticeBefore || n[1].Phase != machines.NoticeAfter {
		t.Fatalf("notices %+v", n)
	}
	if n[0].FileBefore != 236*gib || n[0].Used != 71*gib || n[1].FileAfter != 75*gib || len(n[0].Repositories) != 2 || n[0].MachineName != "win-1" {
		t.Fatalf("notice sizes %+v", n)
	}
	if !strings.Contains(n[0].Text(), "236.0 GiB") || !strings.Contains(n[1].Text(), "236.0 GiB to 75.0 GiB") {
		t.Fatalf("notice text: %q / %q", n[0].Text(), n[1].Text())
	}
	log := r.audit()
	for _, want := range []string{"machine.compact.start", "machine.drain", "machine.resume", "machine.compact.done"} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit lacks %s:\n%s", want, log)
		}
	}
	if strings.Index(log, "machine.compact.start") > strings.Index(log, "machine.compact.done") {
		t.Fatalf("audit order:\n%s", log)
	}
	st := r.svc.CompactStatus(mustMachine(t, r), machines.DefaultSettings())
	if st.Running || st.LastResult != "236.0 GiB to 75.0 GiB" || st.LastAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
}

func mustMachine(t *testing.T, r *compactRig) machines.Machine {
	t.Helper()
	m, err := r.repo.GetMachine(context.Background(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A failed command still lifts the hold and says why (Docker is restarted by
// the helper on the agent; the controller never leaves the machine drained).
func TestCompactMachineFailureReleasesAndNotifies(t *testing.T) {
	r := newCompactRig(t)
	rep := dockercompact.Report{Platform: "windows", Error: "compact failed: DiskPart has encountered an error", DockerRunning: true}
	r.host.answer = machines.HostOutcome{OK: false, ExitCode: 1, Output: rep.Text(), Message: "the command exited with code 1"}
	out, err := r.svc.CompactMachine(context.Background(), "system", r.id)
	if err == nil || out.OK || !out.Released {
		t.Fatalf("out %+v err %v", out, err)
	}
	if r.state() != machines.StateActive {
		t.Fatalf("machine left %s", r.state())
	}
	n := r.note.notices
	if len(n) != 2 || n[1].Phase != machines.NoticeFailed || !strings.Contains(n[1].Text(), "DiskPart") {
		t.Fatalf("notices %+v", n)
	}
	if !strings.Contains(r.audit(), "machine.compact.failed") {
		t.Fatalf("audit:\n%s", r.audit())
	}
}

func TestCompactMachineHostErrorReleases(t *testing.T) {
	r := newCompactRig(t)
	r.host.err = errors.New("agent link lost")
	if _, err := r.svc.CompactMachine(context.Background(), "system", r.id); err == nil {
		t.Fatal("no error")
	}
	if r.state() != machines.StateActive {
		t.Fatalf("machine left %s", r.state())
	}
}

// A machine that never drains is released when the drain timeout passes; the
// command never runs.
func TestCompactMachineDrainTimeoutReleases(t *testing.T) {
	r := newCompactRig(t)
	r.note.neverDrain = true
	// A drain timeout of 5 minutes is the least; the wait is cut by the context.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out, err := r.svc.CompactMachine(ctx, "system", r.id)
	if err == nil || len(r.host.argv) != 0 || !out.Released {
		t.Fatalf("out %+v err %v host %v", out, err, r.host.argv)
	}
	if r.state() != machines.StateActive {
		t.Fatalf("machine left %s", r.state())
	}
}

// A machine that someone else drained is not touched and not released.
func TestCompactMachineLeavesAnotherDrainAlone(t *testing.T) {
	r := newCompactRig(t)
	r.note.neverDrain = true
	if _, err := r.svc.Drain(context.Background(), "owner", r.id, "my own"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.CompactMachine(context.Background(), "system", r.id); !errors.Is(err, machines.ErrCompactBusy) {
		t.Fatalf("err %v", err)
	}
	if r.state() != machines.StateDraining || len(r.host.argv) != 0 {
		t.Fatalf("state %s host %v", r.state(), r.host.argv)
	}
}

// The pass: idle time counts from the first look, the compact runs once, and a
// second pass the same day does nothing.
func TestCompactPassIdleOncePerDay(t *testing.T) {
	r := newCompactRig(t)
	ctx := context.Background()
	decide := func() string {
		t.Helper()
		d, err := r.svc.CompactPass(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.svc.WaitCompact()
		return d[r.id].Reason
	}
	if got := decide(); got != machines.CompactSkipIdle {
		t.Fatalf("first look: %s", got)
	}
	r.clk.Add(29 * time.Minute)
	if got := decide(); got != machines.CompactSkipIdle {
		t.Fatalf("29 minutes: %s", got)
	}
	// A job queues: the idle time starts over.
	r.queue.n = 1
	r.clk.Add(2 * time.Minute)
	if got := decide(); got != machines.CompactSkipBusy {
		t.Fatalf("queued: %s", got)
	}
	r.queue.n = 0
	r.clk.Add(31 * time.Minute)
	if got := decide(); got != machines.CompactRun || len(r.host.argv) != 1 {
		t.Fatalf("idle: %s host %v", got, r.host.argv)
	}
	r.clk.Add(5 * time.Minute)
	if got := decide(); got != machines.CompactSkipToday || len(r.host.argv) != 1 {
		t.Fatalf("same day: %s host %v", got, r.host.argv)
	}
}

// A running job keeps the machine from compacting; a queue the controller cannot
// read counts as busy.
func TestCompactPassRunningJobAndUnreadableQueue(t *testing.T) {
	r := newCompactRig(t)
	ctx := context.Background()
	r.svc.SetRunning(r.id, []machines.JobRef{{Repository: "acme/api", Job: "build", RunnerName: "r1"}}, nil)
	r.svc.CompactPass(ctx) //nolint:errcheck // first look
	r.clk.Add(time.Hour)
	d, _ := r.svc.CompactPass(ctx)
	if d[r.id].Reason != machines.CompactSkipBusy {
		t.Fatalf("running: %+v", d[r.id])
	}
	r.svc.SetRunning(r.id, nil, nil)
	r.queue.err = errors.New("db busy")
	d, _ = r.svc.CompactPass(ctx)
	if d[r.id].Reason != machines.CompactSkipBusy {
		t.Fatalf("unreadable queue: %+v", d[r.id])
	}
}

// A job that queues while the compact runs waits in the queue: the machine is
// held (drained) for the whole command and active again after it, and nothing
// is cancelled or failed.
func TestCompactJobQueuedDuringTheCompactWaits(t *testing.T) {
	r := newCompactRig(t)
	var during string
	r.host.during = func() { during = r.state() }
	if _, err := r.svc.CompactMachine(context.Background(), "system", r.id); err != nil {
		t.Fatal(err)
	}
	if during != machines.StateDrained || r.state() != machines.StateActive {
		t.Fatalf("during %s after %s", during, r.state())
	}
	if strings.Contains(r.audit(), "cancel") {
		t.Fatalf("something was cancelled:\n%s", r.audit())
	}
}

// Caches R5.58: the owner's Compact now asks first and names what can be
// reclaimed; with the yes it drains, compacts and resumes like the automatic one.
func TestStartCompactNamesTheReclaimableSizeThenRuns(t *testing.T) {
	r := newCompactRig(t)
	ctx := context.Background()
	err := r.svc.StartCompact(ctx, "owner", r.id, false)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || !strings.Contains(strings.Join(nc.Names, "\n"), "Can be reclaimed: 165.0 GiB") {
		t.Fatalf("err = %v", err)
	}
	if len(r.host.argv) != 0 {
		t.Fatal("nothing runs without the yes")
	}
	if err := r.svc.StartCompact(ctx, "owner", r.id, true); err != nil {
		t.Fatal(err)
	}
	r.svc.WaitCompact()
	if len(r.host.argv) != 1 || r.host.argv[0][0] != "docker-compact" || r.host.states[0] != machines.StateDrained || r.state() != machines.StateActive {
		t.Fatalf("host %v states %v final %s", r.host.argv, r.host.states, r.state())
	}
	if log := r.audit(); !strings.Contains(log, "machine.compact.done") {
		t.Fatalf("audit:\n%s", log)
	}
}

func TestStartCompactRefusals(t *testing.T) {
	r := newCompactRig(t)
	ctx := context.Background()
	r.report(true, false)
	if err := r.svc.StartCompact(ctx, "owner", r.id, true); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("no admin: %v", err)
	}
	r.report(false, true)
	if err := r.svc.StartCompact(ctx, "owner", r.id, true); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("no disk file: %v", err)
	}
	if len(r.host.argv) != 0 {
		t.Fatal("a refused compact ran")
	}
}
