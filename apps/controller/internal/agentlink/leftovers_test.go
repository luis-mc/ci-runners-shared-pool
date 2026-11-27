package agentlink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

func leftoversLink(canDo bool, answer func(sent *agentv1.SessionResponse) *agentv1.RunnerLeftoversReport) (*Link, *[]*agentv1.SessionResponse) {
	var l *Link
	var mu sync.Mutex
	sent := &[]*agentv1.SessionResponse{}
	l = New(sendFn(func(_ string, m *agentv1.SessionResponse) error {
		mu.Lock()
		*sent = append(*sent, m)
		mu.Unlock()
		rep := answer(m)
		if m.GetScanRunnerLeftovers() != nil {
			rep.CommandId = m.GetScanRunnerLeftovers().GetCommandId()
		} else {
			rep.CommandId = m.GetRemoveRunnerLeftovers().GetCommandId()
		}
		go l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerLeftoversReport{RunnerLeftoversReport: rep}})
		return nil
	}))
	l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: &agentv1.MachineReport{RunnerLeftovers: canDo}}})
	return l, sent
}

// OPS33: an agent without runner_leftovers is never sent a command.
func TestLeftoversRefuseOldAgent(t *testing.T) {
	l, sent := leftoversLink(false, func(*agentv1.SessionResponse) *agentv1.RunnerLeftoversReport {
		return &agentv1.RunnerLeftoversReport{Ok: true}
	})
	if _, err := l.Scan(context.Background(), "m", nil); !errors.Is(err, ErrAgentCannotFindLeftovers) {
		t.Fatalf("scan: %v", err)
	}
	if _, err := l.Remove(context.Background(), "m", nil, []leftovers.Ref{{Kind: leftovers.KindService, Name: "x"}}); !errors.Is(err, ErrAgentCannotFindLeftovers) {
		t.Fatalf("remove: %v", err)
	}
	if _, err := l.Scan(context.Background(), "other", nil); !errors.Is(err, ErrAgentCannotFindLeftovers) {
		t.Fatalf("no report: %v", err)
	}
	if len(*sent) != 0 {
		t.Fatal("a command went to an old agent")
	}
}

func TestLeftoversScanMapsItemsAndOwned(t *testing.T) {
	l, sent := leftoversLink(true, func(*agentv1.SessionResponse) *agentv1.RunnerLeftoversReport {
		return &agentv1.RunnerLeftoversReport{Ok: true, Admin: true, ScannedAtUnixMs: 1700000000000, Items: []*agentv1.Leftover{
			{Kind: "folder", Name: `C:\crsp\runners\old`, Path: `C:\crsp\runners\old`, SizeBytes: 42, Account: "crsp-old", Why: "no runner owns it"},
		}}
	})
	sc, err := l.Scan(context.Background(), "m", []leftovers.Owned{{Name: "box-1", ServiceName: "actions.runner.box-1"}})
	if err != nil || !sc.Admin || len(sc.Items) != 1 || sc.Items[0].Kind != leftovers.KindFolder || sc.Items[0].SizeBytes != 42 ||
		sc.Items[0].Account != "crsp-old" || sc.Items[0].Why != "no runner owns it" || sc.At.UnixMilli() != 1700000000000 {
		t.Fatalf("scan %+v err %v", sc, err)
	}
	got := (*sent)[0].GetScanRunnerLeftovers().GetOwned()
	if len(got) != 1 || got[0].GetName() != "box-1" || got[0].GetServiceName() != "actions.runner.box-1" {
		t.Fatalf("owned sent: %v", got)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pendingLO) != 0 {
		t.Fatal("a command stayed pending")
	}
}

func TestLeftoversRemoveMapsResultsAndErrors(t *testing.T) {
	l, sent := leftoversLink(true, func(m *agentv1.SessionResponse) *agentv1.RunnerLeftoversReport {
		if len(m.GetRemoveRunnerLeftovers().GetItems()) == 1 {
			return &agentv1.RunnerLeftoversReport{Ok: false, Error: "not an administrator"}
		}
		return &agentv1.RunnerLeftoversReport{Ok: true, Results: []*agentv1.LeftoverResult{
			{Kind: "service", Name: "a", Outcome: "removed", Steps: []string{"stopped", "deleted"}},
			{Kind: "folder", Name: "b", Outcome: "refused", Error: "outside the crsp folder"},
		}}
	})
	refs := []leftovers.Ref{{Kind: leftovers.KindService, Name: "a"}, {Kind: leftovers.KindFolder, Name: "b"}}
	res, err := l.Remove(context.Background(), "m", nil, refs)
	if err != nil || len(res) != 2 || res[0].Outcome != leftovers.Removed || len(res[0].Steps) != 2 || res[1].Outcome != leftovers.Refused || res[1].Error == "" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if it := (*sent)[0].GetRemoveRunnerLeftovers().GetItems(); len(it) != 2 || it[1].GetKind() != "folder" || it[1].GetName() != "b" {
		t.Fatalf("sent items %v", it)
	}
	if _, err := l.Remove(context.Background(), "m", nil, refs[:1]); err == nil || !strings.Contains(err.Error(), "not an administrator") {
		t.Fatalf("ok false: %v", err)
	}
}

func TestLeftoversContextEndsTheWait(t *testing.T) {
	l := New(sendFn(func(string, *agentv1.SessionResponse) error { return nil }))
	l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: &agentv1.MachineReport{RunnerLeftovers: true}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Scan(ctx, "m", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pendingLO) != 0 {
		t.Fatal("a command stayed pending")
	}
}
