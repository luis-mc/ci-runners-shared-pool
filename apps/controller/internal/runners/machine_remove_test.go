package runners

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Machines R5.40 and R5.41: a removed machine's runners are deregistered at GitHub, and an
// online agent also cleans up their files.
func TestRemoveMachineRunnersOnline(t *testing.T) {
	r, rec := idleRig(t)
	r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "online"}}
	got := r.svc.RemoveMachineRunners(context.Background(), "m1", "owner", true)
	if len(got) != 1 || !got[0].Done || got[0].Reason != "" {
		t.Fatalf("%+v", got)
	}
	if len(r.ag.stopped) != 1 || len(r.ag.folders) != 1 {
		t.Fatalf("an online agent cleans up: %v %v", r.ag.stopped, r.ag.folders)
	}
	if n, _ := r.svc.EphemeralOnMachine(context.Background(), "m1"); n != 0 {
		t.Fatal("the record stayed")
	}
	assertAudit(t, r.au, "runner.remove_machine")
}

// Machines R5.42: an offline machine still has its runners deregistered at GitHub; its files stay.
func TestRemoveMachineRunnersOffline(t *testing.T) {
	r, rec := idleRig(t)
	r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "offline"}}
	got := r.svc.RemoveMachineRunners(context.Background(), "m1", "owner", false)
	if len(got) != 1 || !got[0].Done || !strings.Contains(got[0].Reason, "files stay") {
		t.Fatalf("%+v", got)
	}
	if len(r.ag.stopped) != 0 || len(r.ag.folders) != 0 {
		t.Fatalf("an offline agent is not asked: %v %v", r.ag.stopped, r.ag.folders)
	}
}

// A runner GitHub refuses to delete is listed with the reason and its record stays.
func TestRemoveMachineRunnersKeepsWhatItCouldNotRemove(t *testing.T) {
	r, rec := idleRig(t)
	r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "online"}}
	r.gh.delErr = fmt.Errorf("boom")
	got := r.svc.RemoveMachineRunners(context.Background(), "m1", "owner", true)
	if len(got) != 1 || got[0].Done || !strings.Contains(got[0].Reason, "boom") {
		t.Fatalf("%+v", got)
	}
	if n, _ := r.svc.EphemeralOnMachine(context.Background(), "m1"); n != 1 {
		t.Fatal("the record of a runner that is still at GitHub was dropped")
	}
	assertAudit(t, r.au, "runner.remove_machine_failed")
}
