package machines_test

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// R5.35: the agent heartbeats every 10 seconds (HelloAck says so, the controller
// answers each heartbeat); the controller marks a machine offline after 30
// seconds without one.
func TestOfflineAfter30SecondsWithoutHeartbeat(t *testing.T) {
	e, id, key := setup(t)
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fa.Ack.GetHeartbeatSeconds() != 10 {
		t.Fatalf("heartbeat seconds = %d", fa.Ack.GetHeartbeatSeconds())
	}
	if v, _ := e.Svc.Get(context.Background(), id); !v.Machine.Online {
		t.Fatal("not online after Hello")
	}
	fa.Send(heartbeat())
	fa.Next(func(m *agentv1.SessionResponse) bool { return m.GetHeartbeat() != nil })

	e.Clk.Add(29 * time.Second)
	_ = e.Svc.Tick(context.Background())
	if v, _ := e.Svc.Get(context.Background(), id); !v.Machine.Online {
		t.Fatal("offline after 29 s")
	}
	e.Clk.Add(2 * time.Second)
	_ = e.Svc.Tick(context.Background())
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Machine.Online {
		t.Fatal("still online after 31 s without a heartbeat")
	}
	// A heartbeat brings it back.
	fa.Send(heartbeat())
	waitFor(t, "online again", func() bool { v, _ := e.Svc.Get(context.Background(), id); return v.Machine.Online })
	// The change was announced to live views.
	e.Note.mu.Lock()
	defer e.Note.mu.Unlock()
	if len(e.Note.state) < 2 {
		t.Fatalf("machine changes announced: %d", len(e.Note.state))
	}
}

// R5.38: on reconnect the agent sends its facts, load and running jobs. The
// controller takes them as what runs now and tells scheduling (OnHello) so slots
// of jobs that ended while offline are freed.
func TestReconnectReportsRunningJobsToScheduling(t *testing.T) {
	var got *agentv1.Hello
	e, id, key := setup(t)
	e.Svc.SetHooks(machinesHooks(func(h *agentv1.Hello) { got = h }))
	fa, err := e.session(id, key, &agentv1.Hello{
		Load:    &agentv1.LoadReport{MemoryPct: 40, MemoryKnown: true, CpuKnown: true, CpuPct: 10},
		Holders: &agentv1.HolderReport{Alive: []*agentv1.Holder{{HolderPid: 7}}, Running: []*agentv1.JobRef{oneJob}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fa.Close()
	if got == nil || len(got.GetHolders().GetAlive()) != 1 || got.GetHolders().GetAlive()[0].GetHolderPid() != 7 {
		t.Fatalf("OnHello got %+v", got)
	}
	v, _ := e.Svc.Get(context.Background(), id)
	if len(v.RunningJobs) != 1 || v.RunningJobs[0].RunID != 42 || v.Load == nil || v.Load.MemoryPct != 40 {
		t.Fatalf("view = running %v load %+v", v.RunningJobs, v.Load)
	}
	// The jobs of the next reconnect replace the old ones: the job ended offline.
	fa2, _ := e.session(id, key, &agentv1.Hello{Holders: &agentv1.HolderReport{}})
	defer fa2.Close()
	if v, _ := e.Svc.Get(context.Background(), id); len(v.RunningJobs) != 0 {
		t.Fatalf("a job that ended while offline is still listed: %v", v.RunningJobs)
	}
}

// R5.35: a machine that sent nothing yet is offline and gets no new job: the
// view says so.
func TestNeverConnectedMachineIsOffline(t *testing.T) {
	e, id, _ := setup(t)
	if v, _ := e.Svc.Get(context.Background(), id); v.Machine.Online || e.Svc.Connected(id) {
		t.Fatal("a machine with no session must be offline")
	}
}
