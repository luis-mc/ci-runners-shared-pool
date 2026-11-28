package machines_test

import (
	"context"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// R5.89 (runners): a runner service the agent could not start is a problem on
// the machine view, with its reason; it clears when a later report says the
// service runs.
func TestRunnerServiceProblemShowsAndClears(t *testing.T) {
	e, id, key := setup(t)
	_, _ = e.session(id, key, nil) // the machine is online
	ctx := context.Background()
	if v, _ := e.Svc.Get(ctx, id); len(v.RunnerProblems()) != 0 {
		t.Fatalf("before a report: %+v", v.RunnerServices)
	}
	why := "the service did not start: Logon failure: the user has not been granted the requested logon type at this computer (Windows error 1069)"
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{RunnerServices: []*agentv1.RunnerServiceReport{
		{RunnerName: "crsp-a", ServiceName: "actions.runner.o-r.crsp-a", Account: `.\crsp-a`, Running: false, LogonKnown: true, LogonHeld: false, Problem: why},
		{RunnerName: "crsp-b", ServiceName: "actions.runner.o-r.crsp-b", Account: `.\crsp-b`, Running: true, LogonKnown: true, LogonHeld: true},
	}})
	v, _ := e.Svc.Get(ctx, id)
	p := v.RunnerProblems()
	if len(p) != 1 || p[0].Runner != "crsp-a" || p[0].Account != `.\crsp-a` || p[0].Problem != why {
		t.Fatalf("problems = %+v", p)
	}
	// The service runs now: the problem is gone.
	e.Svc.HandleMachineReport(id, &agentv1.MachineReport{RunnerServices: []*agentv1.RunnerServiceReport{
		{RunnerName: "crsp-a", ServiceName: "actions.runner.o-r.crsp-a", Account: `.\crsp-a`, Running: true, LogonKnown: true, LogonHeld: true, Regranted: true},
	}})
	if v, _ = e.Svc.Get(ctx, id); len(v.RunnerProblems()) != 0 {
		t.Fatalf("a running service is no problem: %+v", v.RunnerProblems())
	}
}

// R5.178 (runners): a service the agent started again after it stopped is
// recorded in the audit log and shown as running; one it could not start is
// recorded once, not on every pass.
func TestRunnerServiceRestartAndFailureAreAudited(t *testing.T) {
	e, id, key := setup(t)
	_, _ = e.session(id, key, nil)
	ctx := context.Background()
	failed := func() *agentv1.MachineReport {
		return &agentv1.MachineReport{RunnerServices: []*agentv1.RunnerServiceReport{
			{RunnerName: "crsp-a", ServiceName: "svc-a", Running: false, Problem: "disabled"},
			{RunnerName: "crsp-b", ServiceName: "svc-b", Running: true, Problem: "started again after it stopped: no list right"},
		}}
	}
	e.Svc.HandleMachineReport(id, failed())
	e.Svc.HandleMachineReport(id, failed())
	count := map[string]int{}
	for _, a := range e.Repo.AuditLog() {
		count[a.Action]++
	}
	if count["runner_service.start_failed"] != 1 {
		t.Errorf("start_failed audited %d times, want 1 (%v)", count["runner_service.start_failed"], count)
	}
	if count["runner_service.restarted"] != 2 {
		t.Errorf("restarted audited %d times, want 2, one per report that carried it (%v)", count["runner_service.restarted"], count)
	}
	v, _ := e.Svc.Get(ctx, id)
	if p := v.RunnerProblems(); len(p) != 1 || p[0].Runner != "crsp-a" {
		t.Errorf("only the failed service is a problem: %+v", p)
	}
}
