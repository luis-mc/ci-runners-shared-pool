package machines_test

import (
	"context"
	"sync"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.19: with no sample hook, or no registry, HelloAck keeps the old version and a stray batch is dropped.
func TestJobSamplesDefaultAckStaysOld(t *testing.T) {
	e, id, key := setup(t)
	e.Svc.SetHooks(machines.Hooks{Registry: true})
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := fa.Ack.GetProtocolVersion(); v != agentv1.ProtocolVersion_PROTOCOL_VERSION_3 {
		t.Fatalf("ack version %v, want 3", v)
	}
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_JobResourceSamples{JobResourceSamples: &agentv1.JobResourceSamples{RunnerId: "r"}}})
}

// R5.19: with the hook, HelloAck says version 4 and a batch reaches the hook with the machine id.
func TestJobSamplesReachHook(t *testing.T) {
	e, id, key := setup(t)
	var mu sync.Mutex
	var got []*agentv1.JobResourceSamples
	var machine string
	e.Svc.SetHooks(machines.Hooks{Registry: true, OnJobSamples: func(_ context.Context, mid string, m *agentv1.JobResourceSamples) {
		mu.Lock()
		defer mu.Unlock()
		machine = mid
		got = append(got, m)
	}})
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := fa.Ack.GetProtocolVersion(); v != agentv1.ProtocolVersion_PROTOCOL_VERSION_4 {
		t.Fatalf("ack version %v, want 4", v)
	}
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_JobResourceSamples{JobResourceSamples: &agentv1.JobResourceSamples{RunnerId: "r", Final: true}}})
	waitFor(t, "batch", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if machine != id || !got[0].GetFinal() {
		t.Fatalf("machine %q batch %+v", machine, got[0])
	}
}
