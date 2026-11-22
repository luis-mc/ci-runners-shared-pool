package main

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// CRSP233: the KVM and engine sources of the doctor read the machines through a
// fleet that has only the machines service. That must list them and not panic
// (a nil runner store was the "Check crashed: runtime error" on a Windows PC).
func TestDoctorFleetWithOnlyMachinesListsThem(t *testing.T) {
	ctx := context.Background()
	repo := machines.NewMemRepo()
	svc := machines.New(machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461})
	cred, err := svc.LocalCredential(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, machines.JoinInput{Code: cred, Local: true, Facts: machines.Facts{HostName: "GameRoomPC", OS: "windows", Arch: "x64", Threads: 4}}); err != nil {
		t.Fatal(err)
	}
	ms, err := doctorFleet{machines: svc}.Machines(ctx)
	if err != nil || len(ms) != 1 || ms[0].OS != "windows" {
		t.Fatalf("machines = %+v err %v", ms, err)
	}
}
