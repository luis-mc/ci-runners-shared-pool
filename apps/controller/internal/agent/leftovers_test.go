package agent_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

const lfRoot = `C:\ProgramData\crsp\runners`

// lfOS is a small Windows with one leftover of each kind; it records what is removed.
type lfOS struct {
	mu    sync.Mutex
	calls []string
}

func (*lfOS) GOOS() string { return "windows" }
func (*lfOS) Root() string { return lfRoot }
func (*lfOS) Admin() bool  { return true }
func (*lfOS) Services(context.Context) ([]leftovers.OSService, error) {
	return []leftovers.OSService{
		{Name: "actions.runner.o.old", Binary: lfRoot + `\old\bin\RunnerService.exe`, Account: `.\crsp-old`},
		{Name: "actions.runner.o.mine", Binary: lfRoot + `\mine\bin\RunnerService.exe`, Account: `.\crsp-mine`},
	}, nil
}
func (*lfOS) Dirs(context.Context, string) ([]leftovers.Dir, error) {
	return []leftovers.Dir{{Name: "old", Path: lfRoot + `\old`}, {Name: "mine", Path: lfRoot + `\mine`}}, nil
}
func (*lfOS) Files(context.Context, string) ([]leftovers.File, error) {
	return []leftovers.File{{Name: "_download-old.zip", Path: lfRoot + `\_download-old.zip`, Size: 42, ModTime: time.Now().Add(-time.Hour)}}, nil
}
func (*lfOS) DirSize(context.Context, string) (int64, error) { return 10, nil }
func (*lfOS) Accounts(context.Context) ([]leftovers.OSAccount, error) {
	return []leftovers.OSAccount{{Name: "crsp-old", Description: leftovers.AccountDescription}, {Name: "crsp-mine", Description: leftovers.AccountDescription}}, nil
}
func (*lfOS) Resolve(p string) (string, error) { return p, nil }
func (o *lfOS) rec(s string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, s)
	return nil
}
func (o *lfOS) StopService(_ context.Context, n string) error   { return o.rec("stop " + n) }
func (o *lfOS) DeleteService(_ context.Context, n string) error { return o.rec("delete " + n) }
func (o *lfOS) RemoveAll(p string) error                        { return o.rec("remove " + p) }
func (o *lfOS) DeleteAccount(_ context.Context, n string) error { return o.rec("account " + n) }
func (o *lfOS) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.calls...)
}

type lfRig struct {
	svc     *machines.Service
	lk      *agentlink.Link
	id      string
	mu      sync.Mutex
	reports map[string]*agentv1.RunnerLeftoversReport
}

func (r *lfRig) report(id string) *agentv1.RunnerLeftoversReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reports[id]
}

func startLeftovers(t *testing.T, fake leftovers.OS) *lfRig {
	t.Helper()
	c := startController(t)
	lk := agentlink.New(c.Svc)
	lk.Timeout = 10 * time.Second
	r := &lfRig{svc: c.Svc, lk: lk, reports: map[string]*agentv1.RunnerLeftoversReport{}}
	c.Svc.SetHooks(machines.Hooks{
		OnHello: func(_ context.Context, id string, _ *agentv1.Hello) { lk.OnHello(id) },
		OnMessage: func(_ context.Context, id string, m *agentv1.SessionRequest, _ func(*agentv1.SessionResponse)) bool {
			if rep := m.GetRunnerLeftoversReport(); rep != nil {
				r.mu.Lock()
				r.reports[rep.GetCommandId()] = rep
				r.mu.Unlock()
				return true
			}
			return lk.OnMessage(id, m)
		},
	})
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	cfg.Leftovers = fake
	runAgent(t, cfg)
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online && c.Svc.Connected(vs[0].Machine.ID) {
			r.id = vs[0].Machine.ID
			return true
		}
		return false
	})
	waitFor(t, "machine report", func() bool { _, ok := lk.Reports()[r.id]; return ok })
	return r
}

func TestMachineReportSaysTheAgentKnowsRunnerLeftovers(t *testing.T) {
	r := startLeftovers(t, &lfOS{})
	if !r.lk.Reports()[r.id].GetRunnerLeftovers() {
		t.Fatal("the machine report does not carry runner_leftovers")
	}
}

func TestScanRunnerLeftoversRoundTrip(t *testing.T) {
	fake := &lfOS{}
	r := startLeftovers(t, fake)
	err := r.svc.Send(r.id, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_ScanRunnerLeftovers{ScanRunnerLeftovers: &agentv1.ScanRunnerLeftovers{
		CommandId: "scan-1", Owned: []*agentv1.OwnedRunner{{Name: "mine", ServiceName: "actions.runner.o.mine"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "report", func() bool { return r.report("scan-1") != nil })
	rep := r.report("scan-1")
	if !rep.GetOk() || !rep.GetAdmin() || rep.GetScannedAtUnixMs() == 0 {
		t.Fatalf("report %+v", rep)
	}
	var got []string
	for _, it := range rep.GetItems() {
		got = append(got, it.GetKind()+":"+it.GetName())
		if it.GetWhy() == "" {
			t.Fatalf("no why: %+v", it)
		}
	}
	want := "service:actions.runner.o.old|folder:" + lfRoot + `\old|account:crsp-old|download:` + lfRoot + `\_download-old.zip`
	if strings.Join(got, "|") != want {
		t.Fatalf("items %v", got)
	}
	if rep.GetItems()[3].GetSizeBytes() != 42 || rep.GetItems()[1].GetSizeBytes() != 10 {
		t.Fatalf("sizes %+v", rep.GetItems())
	}
	if len(fake.seen()) != 0 {
		t.Fatalf("a scan changed things: %v", fake.seen())
	}
}

func TestRemoveRunnerLeftoversRoundTrip(t *testing.T) {
	fake := &lfOS{}
	r := startLeftovers(t, fake)
	owned := []*agentv1.OwnedRunner{{Name: "mine", ServiceName: "actions.runner.o.mine"}}
	err := r.svc.Send(r.id, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RemoveRunnerLeftovers{RemoveRunnerLeftovers: &agentv1.RemoveRunnerLeftovers{
		CommandId: "rm-1", Owned: owned,
		Items: []*agentv1.LeftoverRef{
			{Kind: "service", Name: "actions.runner.o.old"},
			{Kind: "folder", Name: lfRoot + `\old`},
			{Kind: "service", Name: "actions.runner.o.mine"},
			{Kind: "folder", Name: lfRoot + `\mine`},
			{Kind: "bogus", Name: "x"},
			{Kind: "folder", Name: lfRoot + `\..\Windows`},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "report", func() bool { return r.report("rm-1") != nil })
	rep := r.report("rm-1")
	if !rep.GetOk() || len(rep.GetResults()) != 6 {
		t.Fatalf("report %+v", rep)
	}
	var outcomes []string
	for _, res := range rep.GetResults() {
		outcomes = append(outcomes, res.GetOutcome())
	}
	if strings.Join(outcomes, ",") != "removed,removed,refused,refused,refused,refused" {
		t.Fatalf("outcomes %v", outcomes)
	}
	if len(rep.GetResults()[0].GetSteps()) != 2 || rep.GetResults()[2].GetError() == "" {
		t.Fatalf("results %+v", rep.GetResults())
	}
	want := "stop actions.runner.o.old|delete actions.runner.o.old|remove " + lfRoot + `\old`
	if strings.Join(fake.seen(), "|") != want {
		t.Fatalf("calls %v", fake.seen())
	}
}
