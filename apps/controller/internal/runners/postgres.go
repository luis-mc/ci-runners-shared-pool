package runners

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Postgres per pool and per runner (runners R5.45, R5.54). The server is a
// PostgreSQL 16 container started through the container Engine; each runner
// lane gets a login, a database it owns and the repository's declared NOLOGIN
// roles. Every password is random and lives only in the OS secret store and in
// the job's own db.env; SQL goes to psql on stdin and the admin password in a
// secret environment value, so no password is in an argument, a plan or a log.

// PostgresImage is the server image; the major version is fixed (R5.54).
const PostgresImage = "postgres:16"

// PostgresPort is the port inside the pool network. It is never published.
const PostgresPort = 5432

// Errors of the role rules.
var (
	ErrRoleName  = errors.New("role name refused")
	ErrRoleOwned = errors.New("role is declared by another repository")
)

var roleRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidateRole refuses a declared role name that is not lowercase letters,
// digits and underscore (at most 63), or is `postgres`, `public` or `pg_*`.
func ValidateRole(name string) error {
	switch {
	case !roleRe.MatchString(name):
		return fmt.Errorf("%w: %q must be lowercase letters, digits and underscore, at most 63", ErrRoleName, name)
	case name == "postgres", name == "public", strings.HasPrefix(name, "pg_"):
		return fmt.Errorf("%w: %q is reserved", ErrRoleName, name)
	}
	return nil
}

// RoleOwners remembers which repository declared which role, so two
// repositories never share one.
type RoleOwners interface {
	Owner(ctx context.Context, role string) (repo string, ok bool, err error)
	Claim(ctx context.Context, role, repo string) error
}

// MemoryRoleOwners is an in-process RoleOwners.
type MemoryRoleOwners struct {
	mu sync.Mutex
	m  map[string]string
}

// Owner implements RoleOwners.
func (o *MemoryRoleOwners) Owner(_ context.Context, role string) (string, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r, ok := o.m[role]
	return r, ok, nil
}

// Claim implements RoleOwners.
func (o *MemoryRoleOwners) Claim(_ context.Context, role, repo string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]string{}
	}
	o.m[role] = repo
	return nil
}

// DBSpec is what one runner lane asks for.
type DBSpec struct {
	Repo     string   // owner/name, for the role conflict message
	RunnerID string   // names the login and the database
	CreateDB bool     // repository setting db_createdb
	Roles    []string // declared NOLOGIN roles
}

// DBAccess is what the job needs to connect. Password is secret. Role is the
// repository's shared owner role (R5.97): the job runs as the login but
// `SET ROLE`s to it at connect, through PGOPTIONS, so what it creates is owned
// by the role every runner of the repository shares.
type DBAccess struct {
	Host, Port, User, Database, Password string
	Role                                 string
}

// DBEnv returns the job's settings in a stable order.
func (a DBAccess) DBEnv() [][2]string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(a.User, a.Password),
		Host: a.Host + ":" + a.Port, Path: "/" + a.Database}
	kv := [][2]string{
		{"DATABASE_URL", u.String()}, {"PGHOST", a.Host}, {"PGPORT", a.Port},
		{"PGUSER", a.User}, {"PGPASSWORD", a.Password}, {"PGDATABASE", a.Database},
	}
	if a.Role != "" {
		kv = append(kv, [2]string{"PGOPTIONS", "-c role=" + a.Role})
	}
	return kv
}

// DBEnvFile is the text of db.env (KEY=value lines). It holds a secret: the
// caller writes it with mode 0600 into the runner's own folder.
func (a DBAccess) DBEnvFile() string {
	var b strings.Builder
	for _, kv := range a.DBEnv() {
		b.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	return b.String()
}

// Engine is the part of the container engine Postgres uses: the call that
// runs `docker <args>`. The container backend's Engine satisfies it; it is
// declared here because that package imports this one.
type Engine interface {
	Command(args ...string) (sys.Command, error)
}

// Postgres manages the server of one pool.
type Postgres struct {
	Engine  Engine
	Cmd     sys.Commands
	Secrets secrets.Store
	Owners  RoleOwners
	Pool    string // pool name, used in container, volume and key names
	Rand    io.Reader
	// Wait sleeps between readiness probes; Retries bounds them.
	Wait    func(context.Context, time.Duration) error
	Retries int

	red *redact.Redactor
}

func (p *Postgres) names() (ctr, net, vol string) {
	return "crsp-pg-" + p.Pool, "crsp-net-" + p.Pool, "crsp-pgdata-" + p.Pool
}

func (p *Postgres) adminKey() secrets.Key {
	return secrets.Key("crsp/postgres/" + p.Pool + "/admin")
}

func (p *Postgres) loginKey(login string) secrets.Key {
	return secrets.Key("crsp/postgres/" + p.Pool + "/login/" + login)
}

// LoginName is the login and database name of a runner: `r_` and the id,
// lowercase, other characters turned into underscore, at most 63.
func LoginName(runnerID string) string {
	var b strings.Builder
	b.WriteString("r_")
	for _, c := range strings.ToLower(runnerID) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// OwnerRole is the shared owner role of a repository (R5.97): `crsp_`, the
// repository as owner_name in lowercase letters, digits and underscore, and
// `_owner`, at most 63. A name that would be longer is cut and ends with 8 hex
// digits of the full repository name, so two long names stay apart.
func OwnerRole(repo string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(repo) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 52 {
		h := sha256.Sum256([]byte(strings.ToLower(repo)))
		s = s[:43] + "_" + hex.EncodeToString(h[:4])
	}
	return "crsp_" + s + "_owner"
}

const ownerLike = `crsp\_%\_owner`

const pwAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func (p *Postgres) newPassword() (string, error) {
	r := p.Rand
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, 32)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = pwAlphabet[int(b)%len(pwAlphabet)]
	}
	return string(out), nil
}

// secret returns the stored password, making and storing one when absent. A
// rerun keeps it, so a running job is not cut off.
func (p *Postgres) secret(ctx context.Context, key secrets.Key) (string, error) {
	v, err := p.Secrets.Get(ctx, key)
	switch {
	case err == nil && len(v) > 0:
		p.learn(string(v))
		return string(v), nil
	case err != nil && !errors.Is(err, secrets.ErrNotFound):
		return "", fmt.Errorf("secret store: %w", err)
	}
	pw, err := p.newPassword()
	if err != nil {
		return "", err
	}
	if err := p.Secrets.Put(ctx, key, []byte(pw)); err != nil {
		return "", fmt.Errorf("secret store: %w", err)
	}
	p.learn(pw)
	return pw, nil
}

func (p *Postgres) learn(v string) {
	if p.red == nil {
		p.red = redact.New()
	}
	p.red.Add(v)
}

func (p *Postgres) scrub(s string) string {
	if p.red == nil {
		return s
	}
	return p.red.Redact(s)
}

func (p *Postgres) docker(ctx context.Context, secret map[string]string, stdin string, args ...string) (sys.Result, error) {
	c, err := p.Engine.Command(args...)
	if err != nil {
		return sys.Result{}, err
	}
	c.SecretEnv, c.Stdin = secret, stdin
	return p.Cmd.Run(ctx, c)
}

func (p *Postgres) fail(what string, res sys.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %s", what, p.scrub(err.Error()))
	}
	msg := strings.TrimSpace(res.Stderr)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s: exit %d: %s", what, res.ExitCode, p.scrub(msg))
}

// EnsureServer makes the network, the data volume and the server container,
// and waits until it answers. The port is not published.
func (p *Postgres) EnsureServer(ctx context.Context) error {
	ctr, net, vol := p.names()
	admin, err := p.secret(ctx, p.adminKey())
	if err != nil {
		return err
	}
	if res, err := p.docker(ctx, nil, "", "network", "inspect", net); err != nil {
		return p.fail("network inspect", res, err)
	} else if res.ExitCode != 0 {
		if res, err := p.docker(ctx, nil, "", "network", "create", net); err != nil || res.ExitCode != 0 {
			return p.fail("network create", res, err)
		}
	}
	res, err := p.docker(ctx, nil, "", "inspect", "-f", "{{.State.Running}}", ctr)
	if err != nil {
		return p.fail("inspect", res, err)
	}
	switch {
	case res.ExitCode != 0: // no container yet
		res, err = p.docker(ctx, map[string]string{"POSTGRES_PASSWORD": admin}, "",
			"run", "-d", "--name", ctr, "--network", net, "--restart", "unless-stopped",
			"-v", vol+":/var/lib/postgresql/data", "-e", "POSTGRES_PASSWORD", PostgresImage)
		if err != nil || res.ExitCode != 0 {
			return p.fail("start postgres", res, err)
		}
	case strings.TrimSpace(res.Stdout) != "true":
		if res, err = p.docker(ctx, nil, "", "start", ctr); err != nil || res.ExitCode != 0 {
			return p.fail("start postgres", res, err)
		}
	}
	return p.waitReady(ctx, ctr)
}

func (p *Postgres) waitReady(ctx context.Context, ctr string) error {
	tries := p.Retries
	if tries <= 0 {
		tries = 30
	}
	for i := 0; i < tries; i++ {
		res, err := p.docker(ctx, nil, "", "exec", ctr, "pg_isready", "-U", "postgres")
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		if p.Wait != nil {
			if err := p.Wait(ctx, time.Second); err != nil {
				return err
			}
		}
	}
	return errors.New("postgres did not become ready")
}

// psql runs SQL as the admin on stdin.
func (p *Postgres) psql(ctx context.Context, what, sql string) (string, error) {
	ctr, _, _ := p.names()
	admin, err := p.secret(ctx, p.adminKey())
	if err != nil {
		return "", err
	}
	res, err := p.docker(ctx, map[string]string{"PGPASSWORD": admin}, sql,
		"exec", "-i", "-e", "PGPASSWORD", ctr, "psql", "-U", "postgres", "-d", "postgres",
		"-v", "ON_ERROR_STOP=1", "-tA")
	if err != nil || res.ExitCode != 0 {
		return "", p.fail(what, res, err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

func lit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// CheckRoles refuses a bad or foreign declared role before anything is made.
func (p *Postgres) CheckRoles(ctx context.Context, repo string, roles []string) error {
	for _, r := range roles {
		if err := ValidateRole(r); err != nil {
			return err
		}
		if p.Owners == nil {
			continue
		}
		owner, ok, err := p.Owners.Owner(ctx, r)
		if err != nil {
			return err
		}
		if ok && owner != repo {
			return fmt.Errorf("%w: role %q is declared by %s and by %s", ErrRoleOwned, r, owner, repo)
		}
	}
	return nil
}

// Provision makes (or keeps) the login, its database and the declared roles
// for one runner and returns the settings for its job.
func (p *Postgres) Provision(ctx context.Context, s DBSpec) (DBAccess, error) {
	if err := p.CheckRoles(ctx, s.Repo, s.Roles); err != nil {
		return DBAccess{}, err
	}
	login := LoginName(s.RunnerID)
	pw, err := p.secret(ctx, p.loginKey(login))
	if err != nil {
		return DBAccess{}, err
	}
	createdb := "NOCREATEDB"
	if s.CreateDB {
		createdb = "CREATEDB"
	}
	owner := OwnerRole(s.Repo)
	ownerDB := "NOCREATEDB"
	if s.CreateDB {
		ownerDB = "CREATEDB"
	}
	// The shared owner role comes first: NOLOGIN, CREATEROLE (the jobs make the
	// app roles), CREATEDB only with the repository setting. A rerun alters it.
	var sql strings.Builder
	fmt.Fprintf(&sql, "DO $b$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %s) THEN CREATE ROLE %s NOLOGIN CREATEROLE %s; ELSE ALTER ROLE %s NOLOGIN CREATEROLE %s; END IF; END $b$;\n",
		lit(owner), owner, ownerDB, owner, ownerDB)
	fmt.Fprintf(&sql, "DO $b$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %s) THEN CREATE ROLE %s LOGIN PASSWORD %s %s; ELSE ALTER ROLE %s LOGIN PASSWORD %s %s; END IF; END $b$;\n",
		lit(login), login, lit(pw), createdb, login, lit(pw), createdb)
	// Every login of the repository is a member: it inherits the role's rights
	// and may SET ROLE to it.
	fmt.Fprintf(&sql, "GRANT %s TO %s WITH INHERIT TRUE, SET TRUE;\n", owner, login)
	isNew, err := p.psql(ctx, "check login", "SELECT 1 FROM pg_roles WHERE rolname = "+lit(login)+";")
	if err != nil {
		return DBAccess{}, err
	}
	if _, err := p.psql(ctx, "create login", sql.String()); err != nil {
		return DBAccess{}, err
	}
	// A new login of a repository that has one already gets that login's
	// attributes, settings and memberships (R5.97); then the setting every
	// login needs, on each provision.
	var more0 strings.Builder
	if isNew != "1" {
		more0.WriteString(copySiblingSQL(login, owner))
	}
	more0.WriteString(selfGrantSQL(login))
	if _, err := p.psql(ctx, "login settings", more0.String()); err != nil {
		return DBAccess{}, err
	}
	have, err := p.psql(ctx, "check database", "SELECT 1 FROM pg_database WHERE datname = "+lit(login)+";")
	if err != nil {
		return DBAccess{}, err
	}
	var more strings.Builder
	if have != "1" {
		fmt.Fprintf(&more, "CREATE DATABASE %s OWNER %s;\n", login, owner)
	}
	// An older build made the database owned by the login: the shared role owns it.
	fmt.Fprintf(&more, "ALTER DATABASE %s OWNER TO %s;\n", login, owner)
	// Leftover databases of any login of this repository (a test helper that
	// created one as the bare login) go to the shared role, so any runner can
	// drop them.
	more.WriteString(leftoverDBSQL(owner))
	// Only the repository's logins connect (through the role): no other
	// repository's login can.
	fmt.Fprintf(&more, "REVOKE ALL ON DATABASE %s FROM PUBLIC;\n", login)
	roles := append([]string(nil), s.Roles...)
	sort.Strings(roles)
	for _, r := range roles {
		fmt.Fprintf(&more, "DO $b$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %s) THEN CREATE ROLE %s NOLOGIN; END IF; END $b$;\n", lit(r), r)
		fmt.Fprintf(&more, "GRANT %s TO %s WITH ADMIN OPTION;\n", r, owner)
		fmt.Fprintf(&more, "GRANT %s TO %s WITH ADMIN OPTION;\n", r, login)
	}
	if _, err := p.psql(ctx, "create database and roles", more.String()); err != nil {
		return DBAccess{}, err
	}
	if p.Owners != nil {
		for _, r := range roles {
			if err := p.Owners.Claim(ctx, r, s.Repo); err != nil {
				return DBAccess{}, err
			}
		}
	}
	ctr, _, _ := p.names()
	return DBAccess{Host: ctr, Port: fmt.Sprint(PostgresPort), User: login, Database: login, Password: pw, Role: owner}, nil
}

// SelfGrantSetting is the role setting every runner login carries (R5.97).
// With CREATEROLE, PostgreSQL 16 gives the creator only ADMIN on a role it
// makes; this makes the creator a member WITH SET and INHERIT, so
// `SET LOCAL ROLE <new role>` works. ALTER ROLE ... SET applies at login (the
// shared owner role cannot log in), and it stays in force after SET ROLE.
const SelfGrantSetting = "set, inherit"

// selfGrantSQL sets createrole_self_grant on the login for all databases. It
// is idempotent: a rerun sets the same value.
func selfGrantSQL(login string) string {
	return fmt.Sprintf("ALTER ROLE %s SET createrole_self_grant = %s;\n", login, lit(SelfGrantSetting))
}

// copySiblingSQL copies, from another login that is a member of the same
// owner role, the role attributes, the role settings (pg_db_role_setting) and
// the memberships to the new login. SUPERUSER, REPLICATION and BYPASSRLS are
// never copied (they are set off). Another login is never a membership: the
// shared owner role is the only shared one, and Postgres refuses a cycle.
func copySiblingSQL(login, owner string) string {
	return fmt.Sprintf(`DO $b$
DECLARE sib oid; sibname name; a pg_roles%%ROWTYPE; s record; m record;
BEGIN
  SELECT u.oid, u.rolname INTO sib, sibname FROM pg_auth_members am
    JOIN pg_roles o ON o.oid = am.roleid JOIN pg_roles u ON u.oid = am.member
    WHERE o.rolname = %s AND u.rolcanlogin AND u.rolname <> %s AND u.rolname LIKE 'r\_%%'
    ORDER BY u.rolname LIMIT 1;
  IF sib IS NULL THEN RETURN; END IF;
  SELECT * INTO a FROM pg_roles WHERE oid = sib;
  EXECUTE format('ALTER ROLE %%I NOSUPERUSER NOREPLICATION NOBYPASSRLS %%s %%s %%s CONNECTION LIMIT %%s',
    %s,
    CASE WHEN a.rolcreatedb THEN 'CREATEDB' ELSE 'NOCREATEDB' END,
    CASE WHEN a.rolcreaterole THEN 'CREATEROLE' ELSE 'NOCREATEROLE' END,
    CASE WHEN a.rolinherit THEN 'INHERIT' ELSE 'NOINHERIT' END,
    a.rolconnlimit);
  FOR s IN SELECT c.setdatabase, split_part(c.cfg, '=', 1) AS k, substr(c.cfg, strpos(c.cfg, '=') + 1) AS v
      FROM (SELECT setdatabase, unnest(setconfig) AS cfg FROM pg_db_role_setting WHERE setrole = sib) c LOOP
    IF s.setdatabase = 0 THEN
      EXECUTE format('ALTER ROLE %%I SET %%I TO %%L', %s, s.k, s.v);
    ELSE
      EXECUTE format('ALTER ROLE %%I IN DATABASE %%I SET %%I TO %%L', %s,
        (SELECT datname FROM pg_database WHERE oid = s.setdatabase), s.k, s.v);
    END IF;
  END LOOP;
  FOR m IN SELECT g.rolname, am.admin_option, am.inherit_option, am.set_option
      FROM pg_auth_members am JOIN pg_roles g ON g.oid = am.roleid
      WHERE am.member = sib AND NOT g.rolcanlogin AND g.rolname <> %s LOOP
    EXECUTE format('GRANT %%I TO %%I WITH ADMIN %%s, INHERIT %%s, SET %%s', m.rolname, %s,
      m.admin_option, m.inherit_option, m.set_option);
  END LOOP;
END $b$;
`, lit(owner), lit(login), lit(login), lit(login), lit(login), lit(owner), lit(login))
}

// leftoverDBSQL gives every database owned by a login of the repository to the
// shared owner role.
func leftoverDBSQL(owner string) string {
	return fmt.Sprintf(`DO $b$ DECLARE d record; BEGIN
  FOR d IN SELECT db.datname FROM pg_database db
      JOIN pg_roles u ON u.oid = db.datdba AND u.rolcanlogin
      JOIN pg_auth_members am ON am.member = u.oid
      JOIN pg_roles o ON o.oid = am.roleid AND o.rolname = %s LOOP
    EXECUTE format('ALTER DATABASE %%I OWNER TO %%I', d.datname, %s);
  END LOOP;
END $b$;
`, lit(owner), lit(owner))
}

// Remove drops a runner's database and login and its stored password. The
// declared roles stay: they belong to the repository, not the runner. When it
// was the last login of its repository, the shared owner role goes too, with
// the databases it still owns (R5.97).
func (p *Postgres) Remove(ctx context.Context, runnerID string) error {
	login := LoginName(runnerID)
	owner, err := p.psql(ctx, "find owner role", fmt.Sprintf(
		"SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid JOIN pg_roles u ON u.oid = m.member WHERE u.rolname = %s AND r.rolname LIKE %s ORDER BY 1 LIMIT 1;", lit(login), lit(ownerLike)))
	if err != nil {
		return err
	}
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE);\nDROP ROLE IF EXISTS %s;\n", login, login)
	if _, err := p.psql(ctx, "remove login", sql); err != nil {
		return err
	}
	if err := p.Secrets.Delete(ctx, p.loginKey(login)); err != nil {
		return fmt.Errorf("secret store: %w", err)
	}
	if owner == "" || ValidateRole(owner) != nil {
		return nil
	}
	return p.dropOwnerIfLast(ctx, owner)
}

// dropOwnerIfLast drops the owner role when no login is a member of it.
func (p *Postgres) dropOwnerIfLast(ctx context.Context, owner string) error {
	n, err := p.psql(ctx, "count logins of owner role", fmt.Sprintf(
		"SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid JOIN pg_roles u ON u.oid = m.member WHERE r.rolname = %s AND u.rolcanlogin;", lit(owner)))
	if err != nil {
		return err
	}
	if n != "0" {
		return nil
	}
	dbs, err := p.psql(ctx, "list databases of owner role", fmt.Sprintf(
		"SELECT datname FROM pg_database WHERE datdba = (SELECT oid FROM pg_roles WHERE rolname = %s);", lit(owner)))
	if err != nil {
		return err
	}
	var sql strings.Builder
	for _, d := range strings.Fields(dbs) {
		if ValidateRole(d) == nil { // test worker databases: lowercase names only
			fmt.Fprintf(&sql, "DROP DATABASE IF EXISTS %s WITH (FORCE);\n", d)
		}
	}
	fmt.Fprintf(&sql, "DROP OWNED BY %s;\nDROP ROLE IF EXISTS %s;\n", owner, owner)
	_, err = p.psql(ctx, "drop owner role", sql.String())
	return err
}

// Login is a Postgres login the server holds: its name, the owner role it is
// a member of ("" for one made before R5.97), and what the doctor compares
// between the logins of one repository (R5.97, doctor R5.59).
type Login struct {
	Name, OwnerRole string
	SelfGrant       bool     // createrole_self_grant has both set and inherit
	Attrs           string   // role attributes, as a stable text
	Members         []string // other roles it is a member of, sorted
}

// Logins lists the `r_*` login roles on the server (doctor R5.59). One line
// each: name|owner|selfgrant|attrs|members, members separated by commas.
func (p *Postgres) Logins(ctx context.Context) ([]Login, error) {
	out, err := p.psql(ctx, "list logins", `SELECT u.rolname || '|' ||
  coalesce((SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid WHERE m.member = u.oid AND r.rolname LIKE `+lit(ownerLike)+` LIMIT 1), '') || '|' ||
  coalesce((SELECT bool_and(position(w IN lower(cfg)) > 0) FROM pg_db_role_setting s, unnest(s.setconfig) cfg, unnest(ARRAY['set','inherit']) w WHERE s.setrole = u.oid AND s.setdatabase = 0 AND cfg LIKE 'createrole\_self\_grant=%'), false) || '|' ||
  concat_ws(',', CASE WHEN u.rolcreatedb THEN 'createdb' END, CASE WHEN u.rolcreaterole THEN 'createrole' END, CASE WHEN u.rolinherit THEN 'inherit' END, CASE WHEN u.rolsuper THEN 'superuser' END, CASE WHEN u.rolreplication THEN 'replication' END, CASE WHEN u.rolbypassrls THEN 'bypassrls' END) || '|' ||
  coalesce((SELECT string_agg(g.rolname, ',' ORDER BY g.rolname) FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid WHERE m.member = u.oid), '')
  FROM pg_roles u WHERE u.rolcanlogin AND u.rolname LIKE 'r\_%' ORDER BY 1;`)
	if err != nil {
		return nil, err
	}
	var ls []Login
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		for len(f) < 5 {
			f = append(f, "")
		}
		l := Login{Name: f[0], OwnerRole: f[1], SelfGrant: f[2] == "t" || f[2] == "true", Attrs: f[3]}
		if f[4] != "" {
			l.Members = strings.Split(f[4], ",")
		}
		ls = append(ls, l)
	}
	return ls, nil
}

// RemoveLogin removes a login named by Logins, as Remove does for a runner.
// The doctor never calls it on its own (doctor R5.7, R5.59).
func (p *Postgres) RemoveLogin(ctx context.Context, login string) error {
	if !strings.HasPrefix(login, "r_") || ValidateRole(login) != nil {
		return fmt.Errorf("%w: %q is not a runner login", ErrRoleName, login)
	}
	return p.Remove(ctx, strings.TrimPrefix(login, "r_"))
}
