package machines_test

import (
	"context"
	"sync"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// TestLogBatchReachesTheSink: an agent's LogBatch reaches the hook with the machine id (R5.20).
func TestLogBatchReachesTheSink(t *testing.T) {
	e, id, key := setup(t)
	var mu sync.Mutex
	var got []*agentv1.LogBatch
	var machine string
	e.Svc.SetHooks(machines.Hooks{OnLogBatch: func(_ context.Context, mid string, m *agentv1.LogBatch) {
		mu.Lock()
		defer mu.Unlock()
		machine = mid
		got = append(got, m)
	}})
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_LogBatch{LogBatch: &agentv1.LogBatch{Dropped: 3,
		Lines: []*agentv1.LogLine{{Message: "runner setup: register", RunnerId: "r1"}}}}})
	waitFor(t, "batch", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if machine != id || got[0].GetDropped() != 3 || got[0].GetLines()[0].GetRunnerId() != "r1" {
		t.Fatalf("machine %q batch %+v", machine, got[0])
	}
}
