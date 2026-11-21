package wire

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor/checks"
	"github.com/luis-mc/ci-runners-shared-pool/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/internal/overview"
)

type noFleet struct{}

func (noFleet) Machines(context.Context) ([]doctor.Machine, error)        { return nil, nil }
func (noFleet) Runners(context.Context) ([]doctor.Runner, error)          { return nil, nil }
func (noFleet) Repositories(context.Context) ([]doctor.Repository, error) { return nil, nil }
func (noFleet) Services(context.Context, string) ([]doctor.OSService, error) {
	return nil, nil
}

func TestNewLiveSharesOneHubAndStartsWithASnapshot(t *testing.T) {
	hub, live := NewLive(overview.EmptySource{}, events.Options{})
	defer hub.Stop()
	hub.Publish(events.JobQueued, []byte(`{}`))
	o, err := live.Overview(context.Background())
	if err != nil || o.LastEventId != "1" {
		t.Fatalf("overview %+v %v", o, err)
	}
	sub, err := live.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case e := <-sub.Events():
		if e.Type != events.Snapshot {
			t.Fatalf("first event %v", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
}

func TestNewDoctorRegistersCoreChecksAndRuns(t *testing.T) {
	eng, svc := NewDoctor(DoctorDeps{Fleet: noFleet{}, Store: doctor.NewMemStore()})
	if got := len(eng.Registry.All()); got != 51 {
		t.Fatalf("registered %d checks", got)
	}
	run, err := eng.Run(context.Background(), doctor.Scope{}, doctor.TriggerSystem, "system")
	if err != nil || run.State != doctor.Done {
		t.Fatalf("run %+v %v", run, err)
	}
	if _, err := svc.GetRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
}

// The live doctor adds the two KVM checks the agent report feeds: 51 core
// checks and 2 more are 53 (doctor R5.32).
func TestNewDoctorWithReportedKVMChecksRegistersFortyOne(t *testing.T) {
	eng, _ := NewDoctor(DoctorDeps{Fleet: noFleet{}, Store: doctor.NewMemStore(), Extra: checks.KVMReportedChecks()})
	if got := len(eng.Registry.All()); got != 53 {
		t.Fatalf("registered %d checks", got)
	}
}
