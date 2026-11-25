package agentlink

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

type depsSender struct {
	mu   sync.Mutex
	sent []*agentv1.SessionResponse
}

func (f *depsSender) Send(_ string, m *agentv1.SessionResponse) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	return nil
}

type depsSink struct {
	mu        sync.Mutex
	events    []*agentv1.DepsEvent
	connected chan string
}

func (d *depsSink) HandleEvent(_ context.Context, _ string, ev *agentv1.DepsEvent) {
	d.mu.Lock()
	d.events = append(d.events, ev)
	d.mu.Unlock()
}
func (d *depsSink) OnAgentConnected(id string) { d.connected <- id }

func depsLink(t *testing.T, flag bool) (*Link, *depsSender, *depsSink) {
	t.Helper()
	fs := &depsSender{}
	l := New(fs)
	sink := &depsSink{connected: make(chan string, 4)}
	l.Deps = sink
	l.OnMessage("m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: &agentv1.MachineReport{DependencySetup: flag}}})
	return l, fs, sink
}

func TestDepsCheckRoutesTheReport(t *testing.T) {
	l, fs, _ := depsLink(t, true)
	go func() {
		// Poll by the clock, not by a count of yields: on a slow runner the
		// yields end before SendCheck has sent, and nothing would answer.
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
			fs.mu.Lock()
			var id string
			for _, m := range fs.sent {
				if c := m.GetDepsCheck(); c != nil {
					id = c.GetCommandId()
				}
			}
			fs.mu.Unlock()
			if id != "" {
				l.OnMessage("m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_DepsReport{DepsReport: &agentv1.DepsReport{CommandId: id, Os: "windows", Admin: true}}})
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rep, err := l.SendCheck(ctx, "m1", "linux", "persistent")
	if err != nil || rep.GetOs() != "windows" || !rep.GetAdmin() {
		t.Fatalf("%v %v", rep, err)
	}
	if c := fs.sent[0].GetDepsCheck(); c.GetRunnerOs() != "linux" || c.GetRunnerKind() != "persistent" {
		t.Fatalf("%v", c)
	}
}

func TestDepsOlderAgentGetsNothing(t *testing.T) {
	l, fs, _ := depsLink(t, false)
	if _, err := l.SendCheck(context.Background(), "m1", "linux", "persistent"); !errors.Is(err, ErrAgentCannotSetup) {
		t.Fatalf("%v", err)
	}
	if err := l.SendInstall(context.Background(), "m1", &agentv1.DepsInstall{JobId: "j"}); !errors.Is(err, ErrAgentCannotSetup) {
		t.Fatalf("%v", err)
	}
	if err := l.SendUndo(context.Background(), "m1", &agentv1.DepsUndo{}); !errors.Is(err, ErrAgentCannotSetup) {
		t.Fatalf("%v", err)
	}
	if err := l.SendRestart(context.Background(), "m1", "j"); !errors.Is(err, ErrAgentCannotSetup) {
		t.Fatalf("%v", err)
	}
	if len(fs.sent) != 0 {
		t.Fatalf("sent to an old agent: %v", fs.sent)
	}
	select {
	case <-l.Deps.(*depsSink).connected:
		t.Fatal("an old agent triggered a resume")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDepsEventsAndReconnect(t *testing.T) {
	l, fs, sink := depsLink(t, true)
	select {
	case id := <-sink.connected:
		if id != "m1" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("no resume hook after the report")
	}
	if !l.OnMessage("m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_DepsEvent{DepsEvent: &agentv1.DepsEvent{JobId: "j", Final: true, Outcome: "done"}}}) {
		t.Fatal("event not handled")
	}
	if len(sink.events) != 1 || sink.events[0].GetJobId() != "j" {
		t.Fatalf("%v", sink.events)
	}
	if err := l.SendInstall(context.Background(), "m1", &agentv1.DepsInstall{JobId: "j", ResumeFrom: "verify"}); err != nil {
		t.Fatal(err)
	}
	last := fs.sent[len(fs.sent)-1].GetDepsInstall()
	if last == nil || last.GetCommandId() == "" || last.GetResumeFrom() != "verify" {
		t.Fatalf("%v", last)
	}
	if err := l.SendStop(context.Background(), "m1", "j"); err != nil {
		t.Fatal(err)
	}
	if st := fs.sent[len(fs.sent)-1].GetDepsInstall(); st.GetCommandId() != "stop:j" || len(st.GetItemIds()) != 0 {
		t.Fatalf("%v", st)
	}
}
