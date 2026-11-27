package machines_test

import (
	"context"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// CRSP127, R5.18: the service records the engine of a MachineReport and the
// view shows which OS its containers run.
func TestMachineReportRecordsTheEngine(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	v, _ := e.Svc.Get(ctx, id)
	if v.Engine.Reported || v.Engine.RunsLinuxContainers() {
		t.Fatalf("before a report: %+v", v.Engine)
	}
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{Engine: &agentv1.EngineReport{
		Kind: "docker-desktop", Usable: true, Version: "29.9.3", ContainerOs: " Linux "}})
	v, _ = e.Svc.Get(ctx, id)
	if !v.Engine.RunsLinuxContainers() || v.Engine.Kind != "docker-desktop" || v.Engine.Version != "29.9.3" {
		t.Fatalf("engine %+v", v.Engine)
	}
	// The engine goes down: nothing runs Linux containers any more.
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{Engine: &agentv1.EngineReport{Kind: "docker-desktop", Reason: "stopped"}})
	if v, _ = e.Svc.Get(ctx, id); v.Engine.RunsLinuxContainers() || v.Engine.Reason != "stopped" {
		t.Fatalf("engine down: %+v", v.Engine)
	}
	// A bad word is not a container OS; a report without an engine changes nothing.
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{Engine: &agentv1.EngineReport{Usable: true, ContainerOs: "plan9"}})
	if v, _ = e.Svc.Get(ctx, id); v.Engine.ContainerOS != "" {
		t.Fatalf("bad word kept: %+v", v.Engine)
	}
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{})
	if v, _ = e.Svc.Get(ctx, id); !v.Engine.Reported {
		t.Fatal("an empty report wiped the engine")
	}
}

// The report travels over the agent session, as the real agent sends it.
func TestEngineReportOverTheLinkReachesTheView(t *testing.T) {
	e, id, key := setup(t)
	fa, _ := e.session(id, key, nil)
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: &agentv1.MachineReport{
		Engine: &agentv1.EngineReport{Kind: "docker-desktop", Usable: true, Version: "29.9.3", ContainerOs: "linux"}}}})
	waitFor(t, "engine", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Engine.RunsLinuxContainers()
	})
}
