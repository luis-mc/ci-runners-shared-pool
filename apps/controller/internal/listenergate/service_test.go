package listenergate

import (
	"context"
	"testing"
)

type memSettings struct{ s Settings }

func (m *memSettings) Get(context.Context) Settings { return m.s }
func (m *memSettings) Save(_ context.Context, n Settings, _ string) error {
	m.s = n
	return nil
}

func TestServiceStartsInObserveModeAndSavesEnforce(t *testing.T) {
	r := newRig(false)
	svc := &Control{G: r.g, Settings: &memSettings{}}
	r.g.Settings = svc.Current
	ctx := context.Background()
	v, err := svc.View(ctx)
	if err != nil || v.Enforce || v.WakeEvery != WakeEvery || len(v.Runners) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	if on, _ := svc.GateEnforced(ctx); on {
		t.Error("the gate starts enforcing")
	}
	if _, err := svc.Save(ctx, true, "owner"); err != nil {
		t.Fatal(err)
	}
	taken, err := r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	if err != nil || !taken {
		t.Errorf("the saved setting did not reach the gate: taken=%v err=%v", taken, err)
	}
}
