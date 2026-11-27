package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// R5.33: the yes path. Without a yes the answer names the runner and nothing
// is deleted; with it the registration, the record and the folder go.
func TestOfflineRunnerNeedsAYesThatNamesIt(t *testing.T) {
	r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{{ID: 100, Name: "crsp-job-1", Status: "offline"}})
	_, err := r.svc.DeleteOfflineRegistration(context.Background(), "r_0", false, "luis")
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) == 0 || !strings.Contains(nc.Items[0].Text, "crsp-job-1") {
		t.Fatalf("err = %v", err)
	}
	if len(api.deletes) != 0 {
		t.Fatalf("deleted without a yes: %v", api.deletes)
	}
	if _, err := r.svc.DeleteOfflineRegistration(context.Background(), "r_0", true, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(api.deletes) != 1 || api.deletes[0] != 100 {
		t.Fatalf("deletes = %v", api.deletes)
	}
	if _, err := r.store.GetRunner(context.Background(), "r_0"); err == nil {
		t.Fatal("the record stayed")
	}
	if len(r.ag.folders) != 1 {
		t.Fatalf("clean-up %v", r.ag.folders)
	}
	assertAudit(t, r.au, "runner.delete_offline")
}

// R5.33: online, busy, unlisted and foreign runners are refused; nothing is deleted.
func TestOfflineDeleteRefusesWhatIsNotOfflineAndCrsps(t *testing.T) {
	cases := map[string]github.Runner{
		"online": {ID: 100, Name: "crsp-job-1", Status: "online"},
		"busy":   {ID: 100, Name: "crsp-job-1", Status: "offline", Busy: true},
		"other":  {ID: 999, Name: "crsp-job-1", Status: "offline"},
		"absent": {ID: 100, Name: "somebody", Status: "offline"},
	}
	for name, held := range cases {
		r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{held})
		_, err := r.svc.DeleteOfflineRegistration(context.Background(), "r_0", true, "luis")
		var re *RefusalError
		if !errors.As(err, &re) || len(api.deletes) != 0 {
			t.Errorf("%s: err %v deletes %v", name, err, api.deletes)
		}
	}
	r, api := sweepRig(t, []string{"crsp-job-1"}, []github.Runner{{ID: 100, Name: "crsp-job-1", Status: "offline"}})
	api.listErr = errors.New("rate limited")
	if _, err := r.svc.DeleteOfflineRegistration(context.Background(), "r_0", true, "luis"); err == nil || len(api.deletes) != 0 {
		t.Fatalf("an unreadable list must change nothing: %v %v", err, api.deletes)
	}
}

// poolOnlyResolver answers like the real resolver: a pool scope has no address.
type poolOnlyResolver struct{ fakeResolver }

func (p poolOnlyResolver) Resolve(ctx context.Context, s Scope) (Target, error) {
	if s.Kind == ScopePool {
		return Target{}, errors.New(`unknown runner scope "pool"`)
	}
	return p.fakeResolver.Resolve(ctx, s)
}

func offlinePoolRig(t *testing.T, status string, busy bool) (*rig, *apiGitHub, Record) {
	t.Helper()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, p := poolRig(t, &repos)
	var held []github.Runner
	for i, k := range r.kids(t) {
		held = append(held, github.Runner{ID: int64(500 + i), Name: k.Name, Status: status, Busy: busy && i == 0})
	}
	api := &apiGitHub{fakeGitHub: r.gh, held: held}
	r.svc.GitHub = api
	r.svc.Resolver = poolOnlyResolver{}
	return r, api, p
}

// R5.88: deleting an offline pool runner deletes its hidden registrations at
// GitHub, then its own record. The confirmation lists what goes.
func TestOfflinePoolRunnerDeletesItsRegistrations(t *testing.T) {
	ctx := context.Background()
	r, api, p := offlinePoolRig(t, "offline", false)
	_, err := r.svc.DeleteOfflineRegistration(ctx, p.ID, false, "luis")
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) < 3 {
		t.Fatalf("err = %v, want a yes naming the registrations", err)
	}
	if len(api.deletes) != 0 {
		t.Fatalf("deleted without a yes: %v", api.deletes)
	}
	if _, err := r.svc.DeleteOfflineRegistration(ctx, p.ID, true, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(api.deletes) != 2 {
		t.Fatalf("deletes = %v", api.deletes)
	}
	if left := r.list(t); len(left) != 0 {
		t.Errorf("records left: %+v", left)
	}
}

// R5.88: an online or busy registration refuses the whole delete; nothing goes.
func TestOfflinePoolRunnerRefusesOnlineOrBusy(t *testing.T) {
	for name, tc := range map[string]struct {
		status string
		busy   bool
	}{"online": {"online", false}, "busy": {"offline", true}} {
		r, api, p := offlinePoolRig(t, tc.status, tc.busy)
		before := len(r.list(t))
		_, err := r.svc.DeleteOfflineRegistration(context.Background(), p.ID, true, "luis")
		var re *RefusalError
		if !errors.As(err, &re) || len(api.deletes) != 0 || len(r.list(t)) != before {
			t.Errorf("%s: err %v deletes %v", name, err, api.deletes)
		}
	}
	r, api, p := offlinePoolRig(t, "offline", false)
	api.listErr = errors.New("rate limited")
	if _, err := r.svc.DeleteOfflineRegistration(context.Background(), p.ID, true, "luis"); err == nil || len(api.deletes) != 0 {
		t.Fatalf("an unreadable list must change nothing: %v %v", err, api.deletes)
	}
}
