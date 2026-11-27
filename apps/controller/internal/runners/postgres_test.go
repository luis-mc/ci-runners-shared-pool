package runners

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// fakeEngine builds `docker <args>` calls and never runs one.
type fakeEngine struct{}

func (fakeEngine) Command(args ...string) (sys.Command, error) {
	return sys.Command{Name: "docker", Args: args}, nil
}

// fakePG answers docker calls and records them. SQL on stdin is kept.
type fakePG struct {
	mu        sync.Mutex
	calls     []sys.Command
	hasCtr    bool
	running   string
	dbExists  bool
	failSQLOn string
	// answers to the SELECTs of Remove and Logins, keyed by a text of the SQL
	answers map[string]string
}

func (f *fakePG) Run(_ context.Context, c sys.Command) (sys.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	a := strings.Join(c.Args, " ")
	switch {
	case strings.HasPrefix(a, "network inspect"):
		return sys.Result{ExitCode: 1}, nil
	case strings.HasPrefix(a, "inspect"):
		if !f.hasCtr {
			return sys.Result{ExitCode: 1}, nil
		}
		return sys.Result{Stdout: f.running + "\n"}, nil
	case strings.HasPrefix(a, "run "):
		f.hasCtr, f.running = true, "true"
	case strings.Contains(a, "psql"):
		if f.failSQLOn != "" && strings.Contains(c.Stdin, f.failSQLOn) {
			return sys.Result{ExitCode: 3, Stderr: "ERROR: boom " + c.SecretEnv["PGPASSWORD"] + "\n"}, nil
		}
		for k, v := range f.answers {
			if strings.Contains(c.Stdin, k) {
				return sys.Result{Stdout: v}, nil
			}
		}
		if strings.Contains(c.Stdin, "FROM pg_database") && f.dbExists {
			return sys.Result{Stdout: "1\n"}, nil
		}
		if strings.Contains(c.Stdin, "CREATE DATABASE") {
			f.dbExists = true
		}
	}
	return sys.Result{}, nil
}

func (f *fakePG) sql() string {
	var b strings.Builder
	for _, c := range f.calls {
		b.WriteString(c.Stdin)
	}
	return b.String()
}

func newPG(t *testing.T) (*Postgres, *fakePG, *secrets.Memory) {
	t.Helper()
	f, sec := &fakePG{}, secrets.NewMemory()
	return &Postgres{Engine: fakeEngine{}, Cmd: f, Secrets: sec, Owners: &MemoryRoleOwners{}, Pool: "p1", Retries: 2}, f, sec
}

func TestRolePerRunnerIsolatedAndRemoved(t *testing.T) {
	ctx := context.Background()
	p, f, sec := newPG(t)
	if err := p.EnsureServer(ctx); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, c := range f.calls {
		if len(c.Args) > 0 && c.Args[0] == "run" {
			run = strings.Join(c.Args, " ")
			if c.SecretEnv["POSTGRES_PASSWORD"] == "" {
				t.Error("admin password not passed as a secret value")
			}
		}
	}
	if !strings.Contains(run, PostgresImage) || strings.Contains(run, " -p ") || strings.Contains(run, "--publish") {
		t.Errorf("server must be postgres 16 with no published port: %s", run)
	}
	a, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "Runner-1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Provision(ctx, DBSpec{Repo: "o/b", RunnerID: "runner-2"})
	if err != nil {
		t.Fatal(err)
	}
	if a.User == b.User || a.Database == b.Database || a.Password == b.Password || len(a.Password) < 32 {
		t.Errorf("logins must differ with random passwords: %+v %+v", a, b)
	}
	if a.User != "r_runner_1" || a.Database != a.User {
		t.Errorf("name %q", a.User)
	}
	sql := f.sql()
	if !strings.Contains(sql, "REVOKE ALL ON DATABASE r_runner_1 FROM PUBLIC") || !strings.Contains(sql, "CREATE DATABASE r_runner_1 OWNER crsp_o_a_owner") {
		t.Errorf("database not owned and closed to others: %s", sql)
	}
	if stored, _ := sec.Get(ctx, p.loginKey(a.User)); string(stored) != a.Password {
		t.Error("password not kept in the secret store")
	}
	if err := p.Remove(ctx, "Runner-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.sql(), "DROP DATABASE IF EXISTS r_runner_1") || !strings.Contains(f.sql(), "DROP ROLE IF EXISTS r_runner_1") {
		t.Error("login and database not dropped")
	}
	if _, err := sec.Get(ctx, p.loginKey(a.User)); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("password left in the secret store")
	}
	if _, err := sec.Get(ctx, p.loginKey(b.User)); err != nil {
		t.Error("other runner's password touched")
	}
}

func TestCreatedbOptInAndDeclaredRoles(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPG(t)
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"}); err != nil {
		t.Fatal(err)
	}
	// the sibling copy (OPS54) names CREATEDB inside a CASE; the declared setting is the rest
	s0 := f.sql()
	if i := strings.Index(s0, "DECLARE sib oid"); i >= 0 {
		s0 = s0[:i] + s0[i+strings.Index(s0[i:], "END $b$;"):]
	}
	if s := s0; !strings.Contains(s, "NOCREATEDB") || strings.Contains(strings.ReplaceAll(s, "NOCREATEDB", ""), "CREATEDB") {
		t.Errorf("CREATEDB must be off by default: %s", s)
	}
	f.calls = nil
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1", CreateDB: true, Roles: []string{"app_ro", "app_rw"}}); err != nil {
		t.Fatal(err)
	}
	s := f.sql()
	for _, want := range []string{"PASSWORD", " CREATEDB;", "CREATE ROLE app_ro NOLOGIN", "CREATE ROLE app_rw NOLOGIN", "GRANT app_ro TO r_r1 WITH ADMIN OPTION", "IF NOT EXISTS"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	for _, bad := range []string{"postgres", "public", "pg_monitor", "Upper", "1abc", "a-b", "", strings.Repeat("a", 64)} {
		if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1", Roles: []string{bad}}); !errors.Is(err, ErrRoleName) {
			t.Errorf("role %q accepted: %v", bad, err)
		}
	}
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/b", RunnerID: "r2", Roles: []string{"app_ro"}}); !errors.Is(err, ErrRoleOwned) ||
		!strings.Contains(err.Error(), "o/a") || !strings.Contains(err.Error(), "o/b") {
		t.Errorf("foreign role must name both repositories: %v", err)
	}
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r3", Roles: []string{"app_ro"}}); err != nil {
		t.Errorf("same repository may reuse its role: %v", err)
	}
}

func TestRerunKeepsPassword(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPG(t)
	first, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	second, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Password != second.Password {
		t.Error("rerun changed the password")
	}
	if strings.Contains(f.sql(), "CREATE DATABASE") {
		t.Error("rerun created the database again")
	}
	// A restart of an existing, stopped server starts it and does not recreate it.
	f.hasCtr, f.running = true, "false"
	f.calls = nil
	if err := p.EnsureServer(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if len(c.Args) > 0 && c.Args[0] == "run" {
			t.Error("existing server container recreated")
		}
	}
}

func TestDbEnvMasksSecrets(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPG(t)
	f.failSQLOn = "CREATE ROLE"
	_, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"})
	if err == nil {
		t.Fatal("want a failure")
	}
	admin, _ := p.Secrets.Get(ctx, p.adminKey())
	login, _ := p.Secrets.Get(ctx, p.loginKey("r_r1"))
	if strings.Contains(err.Error(), string(admin)) || strings.Contains(err.Error(), string(login)) {
		t.Errorf("a password reached the error: %v", err)
	}
	for _, c := range f.calls {
		for _, a := range c.Args {
			if strings.Contains(a, string(admin)) || strings.Contains(a, string(login)) {
				t.Errorf("a password is in an argument: %v", c.Args)
			}
		}
		if c.Stdin != "" && strings.Contains(c.Stdin, string(admin)) {
			t.Error("admin password in SQL")
		}
	}
	f.failSQLOn = ""
	acc, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	file := acc.DBEnvFile()
	for _, k := range []string{"DATABASE" + "_URL=postgres://r_r1:", "PGHOST=crsp-pg-p1", "PGPORT=5432", "PGUSER=r_r1", "PGPASSWORD=" + acc.Password, "PGDATABASE=r_r1"} {
		if !strings.Contains(file, k) {
			t.Errorf("db.env lacks %q", k)
		}
	}
}

func TestOwnerRoleName(t *testing.T) { // OPS53, R5.97
	if got := OwnerRole("Luis-MC/ci-runners"); got != "crsp_luis_mc_ci_runners_owner" {
		t.Errorf("name %q", got)
	}
	a, b := OwnerRole("o/"+strings.Repeat("x", 80)+"1"), OwnerRole("o/"+strings.Repeat("x", 80)+"2")
	if len(a) > 63 || len(b) > 63 || a == b || ValidateRole(a) != nil {
		t.Errorf("long names must fit and stay apart: %q %q", a, b)
	}
}

func TestRunnersOfOneRepoShareOwnerRole(t *testing.T) { // OPS53, R5.97
	ctx := context.Background()
	p, f, _ := newPG(t)
	var acc [2]DBAccess
	for i, id := range []string{"r1", "r2"} {
		a, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: id, CreateDB: true, Roles: []string{"app_rw"}})
		if err != nil {
			t.Fatal(err)
		}
		acc[i] = a
	}
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/b", RunnerID: "r3"}); err != nil {
		t.Fatal(err)
	}
	s := f.sql()
	for _, want := range []string{
		"CREATE ROLE crsp_o_a_owner NOLOGIN CREATEROLE CREATEDB",
		"GRANT crsp_o_a_owner TO r_r1 WITH INHERIT TRUE, SET TRUE",
		"GRANT crsp_o_a_owner TO r_r2 WITH INHERIT TRUE, SET TRUE",
		"GRANT crsp_o_b_owner TO r_r3 WITH INHERIT TRUE, SET TRUE",
		"CREATE DATABASE r_r1 OWNER crsp_o_a_owner",
		"ALTER DATABASE r_r1 OWNER TO crsp_o_a_owner",
		"GRANT app_rw TO crsp_o_a_owner WITH ADMIN OPTION",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "GRANT crsp_o_a_owner TO r_r3") || strings.Contains(s, "GRANT crsp_o_b_owner TO r_r1") {
		t.Error("a login joined another repository's role")
	}
	if acc[0].Role != "crsp_o_a_owner" || acc[1].Role != acc[0].Role {
		t.Errorf("both logins must use one role: %+v", acc)
	}
	if env := acc[1].DBEnvFile(); !strings.Contains(env, "PGOPTIONS=-c role=crsp_o_a_owner\n") {
		t.Errorf("job must SET ROLE through PGOPTIONS: %s", env)
	}
	if env := (DBAccess{User: "u", Password: "p"}).DBEnvFile(); strings.Contains(env, "PGOPTIONS") {
		t.Errorf("no role, no PGOPTIONS: %s", env)
	}
}

func TestRemovingLastRunnerDropsOwnerRole(t *testing.T) { // OPS53, R5.97
	ctx := context.Background()
	p, f, _ := newPG(t)
	for _, id := range []string{"r1", "r2"} {
		if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: id}); err != nil {
			t.Fatal(err)
		}
	}
	f.answers = map[string]string{
		"AND r.rolname LIKE 'crsp":                             "crsp_o_a_owner\n",
		"WHERE r.rolname = 'crsp_o_a_owner' AND u.rolcanlogin": "1\n",
	}
	f.calls = nil
	if err := p.Remove(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.sql(), "DROP ROLE IF EXISTS crsp_o_a_owner") {
		t.Error("owner role dropped while another runner uses it")
	}
	f.answers["WHERE r.rolname = 'crsp_o_a_owner' AND u.rolcanlogin"] = "0\n"
	f.answers["FROM pg_database WHERE datdba"] = "r_r2\nr_r2_w1\n"
	f.calls = nil
	if err := p.Remove(ctx, "r2"); err != nil {
		t.Fatal(err)
	}
	s := f.sql()
	for _, want := range []string{"DROP DATABASE IF EXISTS r_r2_w1 WITH (FORCE)", "DROP OWNED BY crsp_o_a_owner", "DROP ROLE IF EXISTS crsp_o_a_owner"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	// A login of an older build has no owner role: only the login goes.
	f.answers = nil
	f.calls = nil
	if err := p.Remove(ctx, "old"); err != nil || strings.Contains(f.sql(), "DROP OWNED") {
		t.Errorf("login without owner role: %v %s", err, f.sql())
	}
}

func TestLoginsListedAndRemovedByName(t *testing.T) { // OPS53, doctor R5.59
	ctx := context.Background()
	p, f, _ := newPG(t)
	f.answers = map[string]string{"FROM pg_roles u WHERE u.rolcanlogin": "r_r1|crsp_o_a_owner|t|createdb,createrole,inherit|crsp_o_a_owner,reader\nr_old|||inherit|\n"}
	ls, err := p.Logins(ctx)
	if err != nil || len(ls) != 2 || ls[0].Name != "r_r1" || ls[0].OwnerRole != "crsp_o_a_owner" || !ls[0].SelfGrant || ls[0].Attrs != "createdb,createrole,inherit" ||
		strings.Join(ls[0].Members, " ") != "crsp_o_a_owner reader" || ls[1].Name != "r_old" || ls[1].SelfGrant || len(ls[1].Members) != 0 {
		t.Fatalf("%+v %v", ls, err)
	}
	f.calls = nil
	if err := p.RemoveLogin(ctx, "r_old"); err != nil || !strings.Contains(f.sql(), "DROP ROLE IF EXISTS r_old;") {
		t.Errorf("%v %s", err, f.sql())
	}
	for _, bad := range []string{"postgres", "r_x; DROP ROLE y", "crsp_o_a_owner"} {
		if err := p.RemoveLogin(ctx, bad); !errors.Is(err, ErrRoleName) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestEveryLoginGetsSelfGrantSetting(t *testing.T) { // OPS54, runners R5.97
	ctx := context.Background()
	p, f, _ := newPG(t)
	for i := 0; i < 2; i++ { // a rerun sets the same value: idempotent
		if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"}); err != nil {
			t.Fatal(err)
		}
	}
	sql := f.sql()
	if n := strings.Count(sql, "ALTER ROLE r_r1 SET createrole_self_grant = 'set, inherit';"); n != 2 {
		t.Errorf("setting not set on each provision (%d): %s", n, sql)
	}
	if strings.Contains(sql, "IN DATABASE r_r1 SET createrole_self_grant") {
		t.Error("setting must apply to all databases")
	}
}

func TestNewLoginCopiesSiblingNotSuperuser(t *testing.T) { // OPS54, runners R5.97
	ctx := context.Background()
	p, f, _ := newPG(t)
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r2"}); err != nil {
		t.Fatal(err)
	}
	sql := f.sql()
	for _, want := range []string{
		"NOSUPERUSER NOREPLICATION NOBYPASSRLS", "pg_db_role_setting", "setrole = sib", "am.member = sib",
		"u.rolname <> 'r_r2'", "o.rolname = 'crsp_o_a_owner'", "AND NOT g.rolcanlogin",
		"WITH ADMIN %s, INHERIT %s, SET %s", "rolcreatedb", "rolcreaterole", "rolconnlimit",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in sibling copy: %s", want, sql)
		}
	}
	for _, bad := range []string{"SUPERUSER ", "REPLICATION ", "BYPASSRLS "} {
		for _, line := range strings.Split(sql, "\n") {
			if strings.Contains(line, bad) && !strings.Contains(line, "NO"+bad) && !strings.Contains(line, "CREATE ROLE") {
				t.Errorf("%q may be copied: %s", bad, line)
			}
		}
	}
	// a login that exists already is not overwritten by a copy
	f.calls = nil
	f.answers = map[string]string{"SELECT 1 FROM pg_roles WHERE rolname = 'r_r2'": "1\n"}
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.sql(), "pg_db_role_setting") || !strings.Contains(f.sql(), "SET createrole_self_grant") {
		t.Errorf("existing login copied again or setting missing: %s", f.sql())
	}
}

func TestLeftoverDatabasesGoToSharedRole(t *testing.T) { // OPS54, runners R5.97
	ctx := context.Background()
	p, f, _ := newPG(t)
	if _, err := p.Provision(ctx, DBSpec{Repo: "o/a", RunnerID: "r1"}); err != nil {
		t.Fatal(err)
	}
	sql := f.sql()
	for _, want := range []string{"JOIN pg_roles u ON u.oid = db.datdba AND u.rolcanlogin", "o.rolname = 'crsp_o_a_owner'",
		"ALTER DATABASE %I OWNER TO %I"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q: %s", want, sql)
		}
	}
}
