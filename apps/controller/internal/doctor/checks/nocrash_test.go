package checks

import (
	"context"
	"fmt"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// fleetOf is a Fleet that knows only machines.
type fleetOf struct{ ms []doctor.Machine }

func (f fleetOf) Machines(context.Context) ([]doctor.Machine, error) { return f.ms, nil }
func (fleetOf) Runners(context.Context) ([]doctor.Runner, error)     { return nil, nil }
func (fleetOf) Services(context.Context, string) ([]doctor.OSService, error) {
	return nil, doctor.ErrUnreachable
}
func (fleetOf) Repositories(context.Context) ([]doctor.Repository, error) { return nil, nil }

type kvmOf []doctor.KVMState

func (k kvmOf) KVM(context.Context) ([]doctor.KVMState, error) { return k, nil }

type enginesOf []doctor.EngineState

func (e enginesOf) Engines(context.Context) ([]doctor.EngineState, error) { return e, nil }

func allChecks() []doctor.Check {
	reg := doctor.NewRegistry()
	Register(reg)
	RegisterKVM(reg)
	RegisterVM(reg)
	for _, c := range HealthChecks() {
		reg.Register(c)
	}
	return reg.All()
}

// CRSP233: no registered check may panic, on an empty report or on a Windows PC
// that reports no KVM facts, Docker Desktop and WSL.
func TestNoCheckCrashesOnEmptyOrWindowsReport(t *testing.T) {
	w := doctor.Machine{ID: "w1", Name: "GameRoomPC", OS: "windows", Online: true}
	envs := map[string]doctor.Env{
		"empty": {Now: now, Fleet: fleetOf{}},
		"windows": {Now: now, Fleet: fleetOf{ms: []doctor.Machine{w}},
			KVM:     kvmOf{{Machine: w}},
			Engines: enginesOf{{Machine: w, Kind: "docker-desktop", Usable: true, Version: "27.0"}}},
		"windows without reports": {Now: now, Fleet: fleetOf{ms: []doctor.Machine{w}}, KVM: kvmOf{}, Engines: enginesOf{}},
	}
	cs := allChecks()
	if len(cs) < 30 {
		t.Fatalf("only %d checks registered", len(cs))
	}
	for name, env := range envs {
		for _, c := range cs {
			t.Run(name+"/"+c.ID(), func(t *testing.T) {
				defer func() {
					if v := recover(); v != nil {
						t.Fatalf("%s crashed: %v", c.ID(), fmt.Sprint(v))
					}
				}()
				_ = c.Run(context.Background(), env)
			})
		}
	}
}

// KVM exists only on Linux: a Windows PC with no KVM facts gets a skip with a
// reason from the two reported checks, and Docker Desktop is ok.
func TestWindowsWithoutKVMFactsSkipsKVMChecks(t *testing.T) {
	w := doctor.Machine{ID: "w1", Name: "GameRoomPC", OS: "windows", Online: true}
	env := doctor.Env{Now: now, Fleet: fleetOf{ms: []doctor.Machine{w}}, KVM: kvmOf{{Machine: w}},
		Engines: enginesOf{{Machine: w, Kind: "docker-desktop", Usable: true, Version: "27.0"}}}
	for _, c := range KVMReportedChecks() {
		fs := c.Run(context.Background(), env)
		if len(fs) != 1 || fs[0].Severity != doctor.Skip || fs[0].Detail == "" {
			t.Fatalf("%s = %+v", c.ID(), fs)
		}
	}
	reg := doctor.NewRegistry()
	Register(reg)
	for _, c := range reg.All() {
		if c.ID() == "machine.engine" {
			fs := c.Run(context.Background(), env)
			if len(fs) != 1 || fs[0].Severity != doctor.OK {
				t.Fatalf("engine = %+v", fs)
			}
		}
	}
}
