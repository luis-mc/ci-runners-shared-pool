package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

func TestRunnerLightLeftover(t *testing.T) { // OPS49, decision D110
	f := &fleet{
		machines: []doctor.Machine{{ID: "m1", Name: "box", OS: "windows", Online: true}},
		runners: []doctor.Runner{
			{ID: "r1", Name: "crsp-light-comics-organizer-windows", MachineID: "m1", MachineName: "box", LegacyLight: true, LightRemovalProblem: "clean up the service: access denied"},
			{ID: "r2", Name: "crsp-light-home-automation-windows", MachineID: "m1", MachineName: "box", LegacyLight: true}, // not tried yet: nothing to say
			{ID: "r3", Name: "crsp-box-1", MachineID: "m1", MachineName: "box"},
		}}
	fs := run(t, doctor.Env{Fleet: f}, "runner.light_leftover")
	if len(fs) != 1 || fs[0].Severity != doctor.Warn || !strings.Contains(fs[0].Title, "comics-organizer") || !strings.Contains(fs[0].Detail, "access denied") {
		t.Fatalf("%+v", fs)
	}
	f.runners = f.runners[2:]
	if fs := run(t, doctor.Env{Fleet: f}, "runner.light_leftover"); len(fs) != 0 {
		t.Fatalf("none left: %+v", fs)
	}
}

type pgSource struct {
	logins []doctor.PostgresLogin
	err    error
}

func (p pgSource) PostgresLogins(context.Context) ([]doctor.PostgresLogin, error) {
	return p.logins, p.err
}

func TestRunnerPostgresLogins(t *testing.T) { // OPS53, doctor R5.59
	f := &fleet{runners: []doctor.Runner{{ID: "r1", Name: "crsp-box-1"}, {ID: "Runner-2", Name: "crsp-box-2"}}}
	src := pgSource{logins: []doctor.PostgresLogin{
		{Name: "r_r1", OwnerRole: "crsp_o_a_owner", SelfGrant: true}, {Name: "r_runner_2", OwnerRole: "crsp_o_a_owner", SelfGrant: true},
		{Name: "r_gone", OwnerRole: "crsp_o_a_owner", SelfGrant: true}, {Name: "r_old", SelfGrant: true}}}
	fs := run(t, doctor.Env{Fleet: f, Postgres: src}, "runner.postgres_logins")
	if len(fs) != 2 || fs[0].Severity != doctor.Warn || fs[0].Subject.ID != "r_gone" || fs[1].Subject.ID != "r_old" ||
		!strings.Contains(fs[0].Detail, "Nothing was removed") || !strings.Contains(fs[1].Detail, "older build") {
		t.Fatalf("%+v", fs)
	}
	if fs := run(t, doctor.Env{Fleet: f, Postgres: pgSource{logins: src.logins[:2]}}, "runner.postgres_logins"); len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("all used: %+v", fs)
	}
	if fs := run(t, doctor.Env{Fleet: f}, "runner.postgres_logins"); len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("no source: %+v", fs)
	}
	if fs := run(t, doctor.Env{Fleet: f, Postgres: pgSource{err: doctor.ErrUnreachable}}, "runner.postgres_logins"); len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("unreachable: %+v", fs)
	}
}

func TestPostgresLoginsSettingsAndSiblings(t *testing.T) { // OPS54, runners R5.97
	f := &fleet{runners: []doctor.Runner{{ID: "r1"}, {ID: "r2"}, {ID: "r3"}, {ID: "r4"}}}
	own := "crsp_o_a_owner"
	good := func(n string) doctor.PostgresLogin {
		return doctor.PostgresLogin{Name: n, OwnerRole: own, SelfGrant: true, Attrs: "createrole,inherit", Members: []string{own}}
	}
	ok := pgSource{logins: []doctor.PostgresLogin{good("r_r1"), good("r_r2")}}
	if fs := run(t, doctor.Env{Fleet: f, Postgres: ok}, "runner.postgres_logins"); len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("same settings: %+v", fs)
	}
	noGrant := good("r_r2")
	noGrant.SelfGrant = false
	odd := good("r_r3")
	odd.Members = []string{own, "reader"}
	fs := run(t, doctor.Env{Fleet: f, Postgres: pgSource{logins: []doctor.PostgresLogin{good("r_r1"), noGrant, odd, good("r_r4")}}}, "runner.postgres_logins")
	if len(fs) != 2 || fs[0].Subject.ID != "r_r2" || !strings.Contains(fs[0].Title, "createrole_self_grant") ||
		fs[1].Subject.ID != "r_r3" || !strings.Contains(fs[1].Title, "differs") || fs[1].Severity != doctor.Warn {
		t.Fatalf("%+v", fs)
	}
}
