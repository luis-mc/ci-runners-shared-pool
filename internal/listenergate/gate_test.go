package listenergate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// The fakes: the labels API, the service manager of the agent, the audit and the clock.
type fakeLabels struct {
	calls   []string
	failAdd error
}

func (f *fakeLabels) AddRunnerLabels(_ context.Context, _ int64, _ github.RunnerScope, id int64, ls []string) error {
	f.calls = append(f.calls, fmt.Sprintf("add %d %s", id, strings.Join(ls, ",")))
	return f.failAdd
}

func (f *fakeLabels) RemoveRunnerLabel(_ context.Context, _ int64, _ github.RunnerScope, id int64, l string) error {
	f.calls = append(f.calls, fmt.Sprintf("remove %d %s", id, l))
	return nil
}

type fakeService struct {
	calls     []string
	state     ListenerState
	failStart error
	failStop  error
}

func (f *fakeService) StartListener(_ context.Context, m, ref string, maint bool) error {
	f.calls = append(f.calls, fmt.Sprintf("start %s %s maint=%v", m, ref, maint))
	return f.failStart
}

func (f *fakeService) StopListener(_ context.Context, m, ref string, assigned bool) error {
	f.calls = append(f.calls, fmt.Sprintf("stop %s %s assigned=%v", m, ref, assigned))
	return f.failStop
}

func (f *fakeService) ListenerState(context.Context, string, string) (ListenerState, error) {
	if f.state == "" {
		return ListenerOffline, nil
	}
	return f.state, nil
}

type fakeAudit struct{ events []Event }

func (a *fakeAudit) Record(_ context.Context, e Event) error {
	a.events = append(a.events, e)
	return nil
}

func (a *fakeAudit) actions() string {
	var s []string
	for _, e := range a.events {
		s = append(s, e.Action)
	}
	return strings.Join(s, ",")
}

type fakeRunners struct{ rs []Runner }

func (f fakeRunners) Gated(context.Context) ([]Runner, error) { return f.rs, nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var host = Runner{ID: "r1", Name: "mac-1", MachineID: "m1", OS: "macos", ServiceRef: "svc-r1", GitHubRunnerID: 7, InstallationID: 1}

type rig struct {
	g   *Gate
	lb  *fakeLabels
	svc *fakeService
	aud *fakeAudit
	clk *clock
	set Settings
}

func newRig(enforce bool, rs ...Runner) *rig {
	r := &rig{lb: &fakeLabels{}, svc: &fakeService{}, aud: &fakeAudit{},
		clk: &clock{t: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}, set: Settings{Enforce: enforce}}
	if len(rs) == 0 {
		rs = []Runner{host}
	}
	r.g = &Gate{Settings: func() Settings { return r.set }, Store: &MemStore{}, Labels: r.lb, Service: r.svc,
		Runners: fakeRunners{rs}, Audit: r.aud, Now: r.clk.now}
	return r
}

func TestEnforceAdmitLabelsThenStarts(t *testing.T) {
	r := newRig(true)
	taken, err := r.g.Admit(context.Background(), Job{JobID: 1, JobLabel: "crsp-build-ab12", Runner: host})
	if err != nil || !taken {
		t.Fatalf("taken=%v err=%v", taken, err)
	}
	if got := strings.Join(r.lb.calls, ";"); got != "add 7 crsp-build-ab12" {
		t.Errorf("labels: %s", got)
	}
	if got := strings.Join(r.svc.calls, ";"); got != "start m1 svc-r1 maint=false" {
		t.Errorf("service: %s", got)
	}
	if r.aud.actions() != ActionStart {
		t.Errorf("audit: %s", r.aud.actions())
	}
}

func TestAdmitTwiceForOneJobChangesNothingMore(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	taken, err := r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err != nil || !taken {
		t.Fatalf("taken=%v err=%v", taken, err)
	}
	if len(r.lb.calls) != 1 || len(r.svc.calls) != 1 {
		t.Errorf("a repeat acted again: %v %v", r.lb.calls, r.svc.calls)
	}
}

func TestCompletedStopsThenRemovesLabel(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err := r.g.Completed(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.calls[len(r.svc.calls)-1]; got != "stop m1 svc-r1 assigned=false" {
		t.Errorf("stop: %s", got)
	}
	if got := r.lb.calls[len(r.lb.calls)-1]; got != "remove 7 crsp-a-0001" {
		t.Errorf("label: %s", got)
	}
	if r.aud.actions() != ActionStart+","+ActionStop {
		t.Errorf("audit: %s", r.aud.actions())
	}
	if err := r.g.Completed(ctx, 1); err != nil || len(r.svc.calls) != 2 {
		t.Errorf("a second completed acted again: %v", r.svc.calls)
	}
}

func TestSecondJobKeepsTheListenerOn(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	_, _ = r.g.Admit(ctx, Job{JobID: 2, JobLabel: "crsp-b-0002", Runner: host})
	if len(r.svc.calls) != 1 {
		t.Fatalf("a second start while on: %v", r.svc.calls)
	}
	_ = r.g.Completed(ctx, 1)
	if len(r.svc.calls) != 1 {
		t.Fatalf("stopped under the second job: %v", r.svc.calls)
	}
	_ = r.g.Completed(ctx, 2)
	if got := r.svc.calls[len(r.svc.calls)-1]; !strings.HasPrefix(got, "stop ") {
		t.Errorf("the last job did not stop it: %v", r.svc.calls)
	}
}

func TestSameLabelStaysUntilLastJob(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	_, _ = r.g.Admit(ctx, Job{JobID: 2, JobLabel: "crsp-a-0001", Runner: host})
	_ = r.g.Completed(ctx, 1)
	for _, c := range r.lb.calls {
		if strings.HasPrefix(c, "remove") {
			t.Fatalf("label removed while job 2 needs it: %v", r.lb.calls)
		}
	}
	_ = r.g.Completed(ctx, 2)
	if got := r.lb.calls[len(r.lb.calls)-1]; got != "remove 7 crsp-a-0001" {
		t.Errorf("label: %s", got)
	}
}

func TestLabelFailureStartsNothing(t *testing.T) {
	r := newRig(true)
	r.lb.failAdd = errors.New("HTTP 500")
	taken, err := r.g.Admit(context.Background(), Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err == nil || taken || len(r.svc.calls) != 0 {
		t.Fatalf("taken=%v err=%v calls=%v", taken, err, r.svc.calls)
	}
	if r.aud.actions() != ActionFailed {
		t.Errorf("audit: %s", r.aud.actions())
	}
	// The next try works once GitHub answers.
	r.lb.failAdd = nil
	if taken, err := r.g.Admit(context.Background(), Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host}); err != nil || !taken {
		t.Errorf("retry: taken=%v err=%v", taken, err)
	}
}

func TestStartFailureRemovesTheLabel(t *testing.T) {
	r := newRig(true)
	r.svc.failStart = errors.New("service refused")
	taken, err := r.g.Admit(context.Background(), Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err == nil || taken {
		t.Fatalf("taken=%v err=%v", taken, err)
	}
	if got := r.lb.calls[len(r.lb.calls)-1]; got != "remove 7 crsp-a-0001" {
		t.Errorf("label left on: %v", r.lb.calls)
	}
}

func TestFailedStopIsRetriedBySweep(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	r.svc.failStop = errors.New("still exiting")
	if err := r.g.Completed(ctx, 1); err == nil {
		t.Fatal("a failed stop was not reported")
	}
	for _, c := range r.lb.calls {
		if strings.HasPrefix(c, "remove") {
			t.Fatalf("label removed before the listener stopped: %v", r.lb.calls)
		}
	}
	r.svc.failStop = nil
	if err := r.g.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.lb.calls[len(r.lb.calls)-1]; got != "remove 7 crsp-a-0001" {
		t.Errorf("sweep did not finish: %v", r.lb.calls)
	}
}

func TestObserveWritesWouldRowsAndChangesNothing(t *testing.T) {
	r := newRig(false)
	ctx := context.Background()
	taken, err := r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err != nil || taken {
		t.Fatalf("taken=%v err=%v", taken, err)
	}
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host}) // a repeat writes nothing
	_ = r.g.Completed(ctx, 1)
	_ = r.g.Completed(ctx, 1)
	if len(r.lb.calls) != 0 || len(r.svc.calls) != 0 {
		t.Errorf("observe mode acted: %v %v", r.lb.calls, r.svc.calls)
	}
	if r.aud.actions() != ActionWouldStart+","+ActionWouldStop {
		t.Errorf("audit: %s", r.aud.actions())
	}
}

func TestStatusSaysOfflineByDesign(t *testing.T) {
	r := newRig(true)
	r.g.Queue = func(context.Context, Runner) string { return "waiting for room on M1" }
	ctx := context.Background()
	st, err := r.g.Status(ctx)
	if err != nil || len(st) != 1 {
		t.Fatalf("%v %v", st, err)
	}
	if st[0].Text != "gated: offline by design" || st[0].ListenerOn || st[0].QueueText != "waiting for room on M1" {
		t.Errorf("%+v", st[0])
	}
	_, _ = r.g.Admit(ctx, Job{JobID: 4, JobLabel: "crsp-a-0001", Runner: host})
	st, _ = r.g.Status(ctx)
	if !st[0].ListenerOn || len(st[0].JobIDs) != 1 || st[0].JobIDs[0] != 4 {
		t.Errorf("%+v", st[0])
	}
}

func TestPickPrefersTheRunnerWithoutAJobAndChecksLabels(t *testing.T) {
	a := Runner{ID: "a", Name: "box-a", MachineID: "m1", OS: "linux", Labels: []string{"self-hosted", "linux", "x64"}, ServiceRef: "sa"}
	b := Runner{ID: "b", Name: "box-b", MachineID: "m1", OS: "linux", Labels: []string{"self-hosted", "linux", "x64", "gpu"}, ServiceRef: "sb"}
	other := Runner{ID: "c", Name: "box-c", MachineID: "m2", OS: "linux", Labels: []string{"self-hosted", "linux"}}
	r := newRig(true, a, b, other)
	ctx := context.Background()
	routing := func(l string) bool { return strings.HasPrefix(l, "crsp-") }
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-x-0001", Runner: a})
	got, ok, err := r.g.Pick(ctx, "m1", "linux", []string{"self-hosted", "linux", "crsp-x-0002"}, routing)
	if err != nil || !ok || got.ID != "b" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	got, ok, _ = r.g.Pick(ctx, "m1", "linux", []string{"self-hosted", "linux", "gpu"}, routing)
	if !ok || got.ID != "b" {
		t.Errorf("gpu job: %+v", got)
	}
	if _, ok, _ := r.g.Pick(ctx, "m1", "windows", nil, routing); ok {
		t.Error("a Linux runner was picked for a Windows job")
	}
	if _, ok, _ := r.g.Pick(ctx, "m3", "linux", nil, routing); ok {
		t.Error("a runner of another machine was picked")
	}
}
