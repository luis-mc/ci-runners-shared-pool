package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

func TestStrayRegistrationNeedsAYesAndNoRecord(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	r.gh.runners = []github.Runner{{ID: 9, Name: "crsp-ghost", Status: "offline"}}
	in := StrayRegistration{InstallationID: 7, GitHubID: 9, Name: "crsp-ghost"}

	var nc *NeedsConfirmation
	if err := r.svc.RemoveStrayRegistration(ctx, in, "crsp-", false, "u"); !errors.As(err, &nc) || len(r.gh.deleted) != 0 {
		t.Fatalf("without a yes nothing goes: %v %v", err, r.gh.deleted)
	}
	if err := r.svc.RemoveStrayRegistration(ctx, in, "crsp-", true, "u"); err != nil || len(r.gh.deleted) != 1 || r.gh.deleted[0] != 9 {
		t.Fatalf("err %v deleted %v", err, r.gh.deleted)
	}
}

func TestStrayRegistrationRefusals(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := r.add(t) // local record box-1
	r.gh.runners = []github.Runner{{ID: 3, Name: "box-1"}, {ID: 4, Name: "other"}, {ID: 5, Name: "crsp-busy", Busy: true}}
	cases := []struct {
		name string
		in   StrayRegistration
		pfx  string
		want string
	}{
		{"a record owns it", StrayRegistration{GitHubID: 3, Name: rec.Name}, "", "local record"},
		{"wrong prefix", StrayRegistration{GitHubID: 4, Name: "other"}, "crsp-", "name prefix"},
		{"not listed", StrayRegistration{GitHubID: 99, Name: "crsp-x"}, "crsp-", "does not list"},
		{"busy", StrayRegistration{GitHubID: 5, Name: "crsp-busy"}, "crsp-", "has a job"},
		{"no id", StrayRegistration{Name: "crsp-x"}, "crsp-", "needs the GitHub runner id"},
	}
	for _, c := range cases {
		err := r.svc.RemoveStrayRegistration(ctx, c.in, c.pfx, true, "u")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if len(r.gh.deleted) != 0 {
		t.Errorf("deleted %v", r.gh.deleted)
	}
}

func TestRecordWithoutRegistrationIsRemovedAfterAYes(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()
	r.gh.runners = nil // GitHub lists nothing

	var nc *NeedsConfirmation
	if _, err := r.svc.RemoveRecordWithoutRegistration(ctx, rec.ID, false, "u"); !errors.As(err, &nc) {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.svc.RemoveRecordWithoutRegistration(ctx, rec.ID, true, "u"); err != nil {
		t.Fatal(err)
	}
	if all, _ := r.store.ListRunners(ctx, Filter{}); len(all) != 0 {
		t.Errorf("record stays: %+v", all)
	}
}

func TestRecordStaysWhenGitHubListsTheRunner(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	r.gh.runners = []github.Runner{{ID: 1, Name: rec.Name, Status: "online"}}
	if _, err := r.svc.RemoveRecordWithoutRegistration(context.Background(), rec.ID, true, "u"); err == nil || !strings.Contains(err.Error(), "registered") {
		t.Fatalf("err = %v", err)
	}
}
