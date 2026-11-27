package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type ephemeralStub struct {
	has bool
	err error
}

func (s ephemeralStub) EphemeralCapacity(context.Context) (bool, error) { return s.has, s.err }

func ephemeralRepo() doctor.Repository {
	r := repo(1, "o/pub", nil)
	r.EphemeralOnly = true
	return r
}

func TestRepoEphemeralCapacity(t *testing.T) { // doctor R5.47
	f := &fleet{repos: []doctor.Repository{ephemeralRepo(), repo(2, "o/priv", nil)}}
	fs := run(t, doctor.Env{GitHub: newGH(), Fleet: f, Ephemeral: ephemeralStub{has: false}}, "repo.ephemeral_capacity")
	if len(fs) != 1 || fs[0].Severity != doctor.Warn || !strings.Contains(fs[0].Title, "o/pub") {
		t.Fatalf("no capacity: %+v", fs)
	}
	if !strings.Contains(fs[0].Detail, "wait") || fs[0].Fix.Link != "/pool/runners/add" {
		t.Fatalf("detail and link: %+v", fs[0])
	}
	fs = run(t, doctor.Env{GitHub: newGH(), Fleet: f, Ephemeral: ephemeralStub{has: true}}, "repo.ephemeral_capacity")
	if len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("capacity: %+v", fs)
	}
	fs = run(t, doctor.Env{GitHub: newGH(), Fleet: f}, "repo.ephemeral_capacity")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("no source: %+v", fs)
	}
	fs = run(t, doctor.Env{GitHub: newGH(), Fleet: f, Ephemeral: ephemeralStub{err: errors.New("boom")}}, "repo.ephemeral_capacity")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("error: %+v", fs)
	}
	// A repository that may use any runner: nothing to say.
	f = &fleet{repos: []doctor.Repository{repo(2, "o/priv", nil)}}
	if fs := run(t, doctor.Env{GitHub: newGH(), Fleet: f, Ephemeral: ephemeralStub{}}, "repo.ephemeral_capacity"); len(fs) != 0 {
		t.Fatalf("private: %+v", fs)
	}
}

func TestRunnerUnclean(t *testing.T) { // doctor R5.46
	f := &fleet{
		machines: []doctor.Machine{{ID: "m1", Name: "box", OS: "linux", Online: true}},
		runners: []doctor.Runner{
			{ID: "r1", Name: "crsp-a", MachineID: "m1", MachineName: "box", Kind: "persistent", UncleanReason: "could not remove /tmp/x"},
			{ID: "r2", Name: "crsp-b", MachineID: "m1", MachineName: "box", Kind: "persistent"},
		}}
	fs := run(t, doctor.Env{Fleet: f}, "runner.unclean")
	if len(fs) != 1 || fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Title, "crsp-a") || !strings.Contains(fs[0].Detail, "could not remove /tmp/x") {
		t.Fatalf("%+v", fs)
	}
	f.runners[0].UncleanReason = ""
	fs = run(t, doctor.Env{Fleet: f}, "runner.unclean")
	if len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("clean: %+v", fs)
	}
}
