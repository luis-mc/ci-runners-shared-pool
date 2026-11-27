package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func TestOwnedRunnersListsEveryRunnerOfTheMachine(t *testing.T) {
	ctx := context.Background()
	st := runners.NewMemStore()
	for _, r := range []runners.Record{
		{ID: "r1", Name: "box-1", MachineID: "m1", ServiceName: "actions.runner.box-1", State: runners.Idle},
		{ID: "r2", Name: "box-eph", MachineID: "m1", Kind: "ephemeral", State: runners.Removing},
		{ID: "r3", Name: "other", MachineID: "m2", ServiceName: "actions.runner.other", State: runners.Idle},
	} {
		if err := st.InsertRunner(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ownedRunners(st)(ctx, "m1")
	if err != nil || len(got) != 2 {
		t.Fatalf("owned %+v err %v", got, err)
	}
	names := got[0].Name + "," + got[1].Name
	if !strings.Contains(names, "box-1") || !strings.Contains(names, "box-eph") {
		t.Fatalf("names %s", names)
	}
}

func TestRemoteLeftoverSourceNilWithoutLink(t *testing.T) {
	if remoteLeftoverSource(nil) != nil {
		t.Fatal("a nil link became a non-nil source")
	}
}

// The unwired local source on the controller's own machine is a clear reason,
// never a crash and never "unreachable".
func TestLeftoversOnControllerWithoutLocalSourceHasAReason(t *testing.T) {
	ctx := context.Background()
	repo := machines.NewMemRepo()
	svc := machines.New(machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461})
	cred, err := svc.LocalCredential(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(ctx, machines.JoinInput{Code: cred, Local: true, Facts: machines.Facts{HostName: "ctl", OS: "windows", Arch: "x64", Threads: 4}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMachine(ctx, res.MachineID)
	m.Online = true
	_ = repo.UpdateMachine(ctx, m)
	svc.SetOwnedRunners(ownedRunners(runners.NewMemStore()))
	svc.SetLeftoverSources(nil, nil)
	_, err = doctorFleet{machines: svc}.Leftovers(ctx, res.MachineID)
	if err == nil || errors.Is(err, doctor.ErrUnreachable) || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
}
