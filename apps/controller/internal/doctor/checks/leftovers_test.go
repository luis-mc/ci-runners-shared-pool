package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

// loFleet is a fleet that scans for runner leftovers.
type loFleet struct {
	*fleet
	scans map[string]leftovers.Scan
	errs  map[string]error
	asked []string
}

func (f *loFleet) Leftovers(_ context.Context, id string) (leftovers.Scan, error) {
	f.asked = append(f.asked, id)
	if err := f.errs[id]; err != nil {
		return leftovers.Scan{}, err
	}
	return f.scans[id], nil
}

func twoLeftovers() []leftovers.Item {
	return []leftovers.Item{
		{Kind: leftovers.KindService, Name: "actions.runner.old-1", Path: `C:\crsp\runners\old-1`, Account: `.\crsp-old-1`, Why: "no runner of this controller has this service"},
		{Kind: leftovers.KindFolder, Name: `C:\crsp\runners\old-1`, Path: `C:\crsp\runners\old-1`, SizeBytes: 3 << 30, Why: "no runner of this controller uses this folder"},
	}
}

func TestRunnerLeftoversNone(t *testing.T) {
	f := &loFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, scans: map[string]leftovers.Scan{"m1": {Admin: true}}}
	fs := run(t, doctor.Env{Fleet: f}, "machine.runner_leftovers")
	g := only(t, fs, "machine.runner_leftovers")
	if g.Severity != doctor.OK || g.Title != "No runner leftovers on alpha" || len(g.Items) != 0 || g.Action != nil {
		t.Fatalf("finding %+v", g)
	}
}

func TestRunnerLeftoversFound(t *testing.T) {
	f := &loFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, scans: map[string]leftovers.Scan{"m1": {Admin: true, Items: twoLeftovers()}}}
	g := only(t, run(t, doctor.Env{Fleet: f}, "machine.runner_leftovers"), "machine.runner_leftovers")
	if g.Severity != doctor.Fail || g.Title != "2 runner leftovers no controller owns on alpha" || g.NeedsAdmin {
		t.Fatalf("finding %+v", g)
	}
	for _, want := range []string{"Service actions.runner.old-1", `account .\crsp-old-1`, `Folder C:\crsp\runners\old-1 (3.0 GiB)`, "no runner of this controller uses this folder"} {
		if !strings.Contains(g.Detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, g.Detail)
		}
	}
	if len(g.Items) != 2 || g.Items[0].Kind != "service" || g.Items[1].SizeBytes != 3<<30 || g.Items[0].Account != `.\crsp-old-1` || g.Items[1].Why == "" {
		t.Fatalf("items %+v", g.Items)
	}
	if g.Action == nil || g.Action.ID != doctor.ActionRemoveRunnerLeftovers || g.Action.Label != "Remove" {
		t.Fatalf("action %+v", g.Action)
	}
	if g.Fix.Text != "Press Remove. The agent on alpha stops and deletes the services, deletes the folders and accounts and the download, and keeps every runner of this pool." || g.Fix.Link != "/machines/m1" {
		t.Fatalf("fix %+v", g.Fix)
	}
}

func TestRunnerLeftoversSingular(t *testing.T) {
	f := &loFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, scans: map[string]leftovers.Scan{"m1": {Admin: true, Items: twoLeftovers()[:1]}}}
	g := only(t, run(t, doctor.Env{Fleet: f}, "machine.runner_leftovers"), "machine.runner_leftovers")
	if g.Title != "1 runner leftover no controller owns on alpha" {
		t.Fatalf("title %q", g.Title)
	}
}

func TestRunnerLeftoversNeedAdmin(t *testing.T) {
	f := &loFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, scans: map[string]leftovers.Scan{"m1": {Admin: false, Items: twoLeftovers()}}}
	g := only(t, run(t, doctor.Env{Fleet: f}, "machine.runner_leftovers"), "machine.runner_leftovers")
	if g.Severity != doctor.Fail || !g.NeedsAdmin || g.Action == nil ||
		!strings.Contains(g.Detail, "The agent on alpha has no administrator rights, so it cannot remove them. Run the agent as a service with rights.") {
		t.Fatalf("finding %+v", g)
	}
}

func TestRunnerLeftoversOfflineErrorsAndOldAgent(t *testing.T) {
	off := healthyMachine()
	off.ID, off.Name, off.Online = "m2", "beta", false
	bad := healthyMachine()
	bad.ID, bad.Name = "m3", "gamma"
	old := healthyMachine()
	old.ID, old.Name = "m4", "delta"
	down := healthyMachine()
	down.ID, down.Name = "m5", "eps"
	f := &loFleet{fleet: &fleet{machines: []doctor.Machine{off, bad, old, down}}, errs: map[string]error{
		"m3": errors.New("the scan could not read the service list"),
		"m4": errors.New("the agent of this machine cannot look for runner leftovers yet, update it"),
		"m5": doctor.ErrUnreachable,
	}}
	fs := run(t, doctor.Env{Fleet: f}, "machine.runner_leftovers")
	by := map[string]doctor.Finding{}
	for _, g := range fs {
		by[g.Subject.Name] = g
	}
	for name, g := range by {
		if g.Severity != doctor.Skip {
			t.Errorf("%s: severity %s", name, g.Severity)
		}
	}
	if len(by) != 4 || !strings.Contains(by["beta"].Title, "offline") || !strings.Contains(by["gamma"].Detail, "could not read the service list") ||
		!strings.Contains(by["delta"].Detail, "update it") || !strings.Contains(by["eps"].Title, "could not be reached") {
		t.Fatalf("findings %+v", by)
	}
	for _, id := range f.asked {
		if id == "m2" {
			t.Fatal("an offline machine was asked")
		}
	}
}

func TestRunnerLeftoversFleetWithoutTheFeatureSkips(t *testing.T) {
	g := only(t, run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}}, "machine.runner_leftovers"), "machine.runner_leftovers")
	if g.Severity != doctor.Skip || g.Detail == "" {
		t.Fatalf("finding %+v", g)
	}
}
