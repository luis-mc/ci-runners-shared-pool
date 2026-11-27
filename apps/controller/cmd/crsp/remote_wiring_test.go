package main

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

type fleetStub struct {
	doctor.Fleet
	ms []doctor.Machine
}

func (f fleetStub) Machines(context.Context) ([]doctor.Machine, error) { return f.ms, nil }

type nopSender struct{}

func (nopSender) Send(string, *agentv1.SessionResponse) error { return nil }

func report(l *agentlink.Link, id string, m *agentv1.MachineReport) {
	l.OnMessage(id, &agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: m}})
}

// doctor R5.32, R5.36: the checks read what the agent reported.
func TestReportSourcesReadTheAgentReport(t *testing.T) {
	ctx := context.Background()
	lk := agentlink.New(nopSender{})
	src := reportSources{fleet: fleetStub{ms: []doctor.Machine{
		{ID: "w", Name: "pc", OS: "windows", Online: true},
		{ID: "off", Name: "old", OS: "linux", Online: false},
		{ID: "quiet", Name: "quiet", OS: "linux", Online: true},
	}}, link: lk}
	if _, err := src.KVM(ctx); !errors.Is(err, doctor.ErrUnreachable) {
		t.Fatalf("no report yet: %v", err)
	}
	rep := &agentv1.MachineReport{
		Kvm:    &agentv1.KvmReport{DeviceKnown: true, DevicePresent: true, NestedKnown: true, NestedVirtualization: true, ModuleKnown: true, ModuleLoaded: true},
		Engine: &agentv1.EngineReport{Kind: "docker-desktop", Usable: true, Version: "27.0"},
	}
	report(lk, "w", rep)
	report(lk, "off", rep)
	kv, err := src.KVM(ctx)
	if err != nil || len(kv) != 1 || kv[0].Machine.ID != "w" || !kv[0].Nested || !kv[0].ModuleLoaded || !kv[0].HostDevice {
		t.Fatalf("kvm = %+v %v", kv, err)
	}
	en, err := src.Engines(ctx)
	if err != nil || len(en) != 1 || en[0].Kind != "docker-desktop" || !en[0].Usable {
		t.Fatalf("engines = %+v %v", en, err)
	}
}

// The reported checks run on the reported state and the live doctor registers
// them (doctor R5.32).
func TestKVMReportedChecksRunOnTheReport(t *testing.T) {
	lk := agentlink.New(nopSender{})
	report(lk, "w", &agentv1.MachineReport{Kvm: &agentv1.KvmReport{NestedKnown: true, ModuleKnown: true}})
	src := reportSources{fleet: fleetStub{ms: []doctor.Machine{{ID: "w", Name: "pc", OS: "windows", Online: true}}}, link: lk}
	sts, err := src.KVM(context.Background())
	if err != nil || len(sts) != 1 || sts[0].Nested || sts[0].ModuleLoaded {
		t.Fatalf("%+v %v", sts, err)
	}
}

func TestRemoteAgentNilLinkIsNilAgent(t *testing.T) {
	if remoteAgent(nil) != nil {
		t.Fatal("a nil link made a non-nil agent")
	}
	if remoteAgent(agentlink.New(nopSender{})) == nil {
		t.Fatal("no agent for a link")
	}
}
