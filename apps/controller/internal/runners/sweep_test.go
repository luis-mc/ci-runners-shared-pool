package runners

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// apiGitHub behaves like GitHub's runner API: it lists what it holds, refuses
// a delete of a busy runner with ErrRunnerBusy and of an unknown id with
// ErrNotFound, and counts every delete.
type apiGitHub struct {
	*fakeGitHub
	held    []github.Runner
	deletes []int64
}

func (g *apiGitHub) ListRunners(context.Context, int64, github.RunnerScope) ([]github.Runner, error) {
	return append([]github.Runner(nil), g.held...), g.listErr
}

func (g *apiGitHub) DeleteRunner(_ context.Context, _ int64, _ github.RunnerScope, id int64) error {
	g.deletes = append(g.deletes, id)
	for i, r := range g.held {
		if r.ID != id {
			continue
		}
		if r.Busy {
			return fmt.Errorf("%w: /actions/runners/%d", github.ErrRunnerBusy, id)
		}
		g.held = append(g.held[:i:i], g.held[i+1:]...)
		return nil
	}
	return fmt.Errorf("%w: /actions/runners/%d", github.ErrNotFound, id)
}

func (g *apiGitHub) names() []string {
	var out []string
	for _, r := range g.held {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

const day = 24 * time.Hour

// sweepRig has crsp records for the given runner names, all made 10 days ago,
// and an API that holds the given registrations.
func sweepRig(t *testing.T, own []string, held []github.Runner) (*rig, *apiGitHub) {
	t.Helper()
	r := newRig(dockerMachine(true))
	api := &apiGitHub{fakeGitHub: r.gh, held: held}
	r.svc.GitHub = api
	made := r.clock.Add(-10 * day)
	for i, name := range own {
		rec := Record{ID: fmt.Sprintf("r_%d", i), Name: name, MachineID: "m1", OS: "linux", Kind: "ephemeral", Managed: true,
			Scope: repoScope, State: Idle, ServiceName: "pool:" + name, GitHubRunnerID: int64(100 + i), CreatedAt: made}
		if err := r.store.InsertRunner(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return r, api
}

// R5.33: an offline registration crsp made, older than the grace, is deleted
// and its record and machine leftovers go with it.
func TestSweepDeletesOnlyOldOfflineCrspRunners(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1", "crsp-job-2"}, []github.Runner{
		{ID: 100, Name: "crsp-job-1", Status: "offline"},
		{ID: 101, Name: "crsp-job-2", Status: "online"}, // online: not swept
	})
	res, err := r.svc.SweepOffline(context.Background(), 7*day, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Deleted) != "[crsp-job-1]" {
		t.Fatalf("deleted = %v", res.Deleted)
	}
	if fmt.Sprint(api.names()) != "[crsp-job-2]" {
		t.Fatalf("GitHub holds %v", api.names())
	}
	if len(r.ag.folders) != 1 {
		t.Fatalf("clean-up %v", r.ag.folders)
	}
	if _, err := r.store.GetRunner(context.Background(), "r_0"); err == nil {
		t.Fatal("the swept record stayed")
	}
	assertAudit(t, r.au, "runner.swept")
}

// R5.33: a record younger than the grace is left, whatever GitHub says.
func TestSweepWaitsForTheGrace(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{{ID: 100, Name: "crsp-job-1", Status: "offline"}})
	res, err := r.svc.SweepOffline(context.Background(), 14*day, "sweep")
	if err != nil || len(res.Deleted) != 0 || len(api.deletes) != 0 {
		t.Fatalf("%+v %v deletes=%v", res, err, api.deletes)
	}
}

// R5.33: a busy runner is never removed, and a 422 on the delete (busy after
// the list) leaves the runner and its record.
func TestSweepLeavesBusyAndForeignRunners(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1", "crsp-job-3"}, []github.Runner{
		{ID: 100, Name: "crsp-job-1", Status: "offline", Busy: true},
		{ID: 101, Name: "crsp-job-3", Status: "offline"},
		// Not crsp's: no record, whatever the name or status.
		{ID: 500, Name: "crsp-job-99", Status: "offline"},
		{ID: 501, Name: "build-box-1", Status: "offline"},
		{ID: 502, Name: "someone-elses", Status: "offline", Labels: []string{"self-hosted", "crsp-home"}},
	})
	res, err := r.svc.SweepOffline(context.Background(), 7*day, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range api.deletes {
		if id >= 500 || id == 100 {
			t.Fatalf("the sweep tried to delete runner %d", id)
		}
	}
	if fmt.Sprint(res.Deleted) != "[crsp-job-3]" || fmt.Sprint(res.LeftBusy) != "[crsp-job-1]" {
		t.Fatalf("result = %+v", res)
	}
	for _, foreign := range []string{"crsp-job-99", "build-box-1", "someone-elses"} {
		if !strings.Contains(strings.Join(api.names(), ","), foreign) {
			t.Fatalf("foreign runner %s was removed: %v", foreign, api.names())
		}
	}
	if _, err := r.store.GetRunner(context.Background(), "r_0"); err != nil {
		t.Fatal("the record of a busy runner was dropped")
	}
}

// R5.33: the 422 path. GitHub lists the runner idle, then answers 422.
func TestSweep422IsBusy(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{{ID: 100, Name: "crsp-job-1", Status: "offline"}})
	r.svc.GitHub = &busyOnDelete{apiGitHub: api}
	res, err := r.svc.SweepOffline(context.Background(), 7*day, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.LeftBusy) != "[crsp-job-1]" || len(res.Deleted) != 0 || len(r.ag.stopped) != 0 {
		t.Fatalf("result = %+v stopped=%v", res, r.ag.stopped)
	}
	if _, err := r.store.GetRunner(context.Background(), "r_0"); err != nil {
		t.Fatal("the record of a busy runner was dropped")
	}
}

type busyOnDelete struct{ *apiGitHub }

func (b *busyOnDelete) DeleteRunner(context.Context, int64, github.RunnerScope, int64) error {
	return fmt.Errorf("%w: busy", github.ErrRunnerBusy)
}

// R5.33: zero days turns the sweep off; nothing is read or deleted.
func TestSweepOffWhenZero(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{{ID: 100, Name: "crsp-job-1", Status: "offline"}})
	res, err := r.svc.SweepOffline(context.Background(), 0, "sweep")
	if err != nil || len(res.Deleted) != 0 || len(api.deletes) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// Persistent runners, records in a live state and records of another name
// than GitHub's id are not swept; an unreadable list changes nothing.
func TestSweepSkipsLiveAndMismatched(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1", "crsp-job-2", "crsp-job-3"}, []github.Runner{
		{ID: 100, Name: "crsp-job-1", Status: "offline"},
		{ID: 101, Name: "crsp-job-2", Status: "offline"},
		{ID: 777, Name: "crsp-job-3", Status: "offline"}, // same name, not the id crsp holds
	})
	ctx := context.Background()
	rec, _ := r.store.GetRunner(ctx, "r_0")
	rec.Kind = "persistent"
	_ = r.store.InsertRunner(ctx, rec)
	rec, _ = r.store.GetRunner(ctx, "r_1")
	rec.State = Busy
	_ = r.store.InsertRunner(ctx, rec)
	res, err := r.svc.SweepOffline(ctx, 7*day, "sweep")
	if err != nil || len(res.Deleted) != 0 || len(api.deletes) != 0 {
		t.Fatalf("%+v %v deletes=%v", res, err, api.deletes)
	}
	api.listErr = fmt.Errorf("github down")
	rec, _ = r.store.GetRunner(ctx, "r_0")
	rec.Kind = "ephemeral"
	_ = r.store.InsertRunner(ctx, rec)
	if _, err := r.svc.SweepOffline(ctx, 7*day, "sweep"); err == nil || len(api.deletes) != 0 {
		t.Fatalf("an unreadable list must change nothing: %v %v", err, api.deletes)
	}
}
