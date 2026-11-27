package api

import (
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/configexport"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/configio"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
)

type opsEnv struct {
	mux *http.ServeMux
	ops *backup.Ops
	dir string
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	db, err := backup.OpenDBForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var mux *http.ServeMux
	ops := backup.Start(ctx, backup.StartOptions{
		DB: db, DataDir: dir, ConfigFile: filepath.Join(dir, "config.toml"), Version: "test", Logger: log,
		DrillHandler: func(d *backup.DB) http.Handler {
			m := http.NewServeMux()
			Register(m, Options{Version: "test", DB: d, Logger: log})
			return m
		},
	})
	// Cleanups run last-in first-out, after TempDir was made: stop the background
	// jobs and wait for them, then close the database, then TempDir is removed.
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { cancel(); ops.Wait() })
	cfgWho := func(ctx context.Context) (audit.Actor, auth.Principal) {
		return audit.ActorFrom(ctx), auth.Principal{Login: "octo"}
	}
	engine := &configio.Engine{Audit: ops.Audit, Version: "test", Sections: []configio.Section{
		configio.NewSettings(ops.Settings, cfgWho), configio.NewPool(ops.Settings, cfgWho), configio.NewQueue(ops.Settings, cfgWho)}}
	mux = http.NewServeMux()
	Register(mux, Options{Version: "test", ConfigAsCode: engine, DB: db, Logger: log, Settings: ops.Settings, Audit: ops.Audit, Backup: ops.Backup, Authn: asRole(auth.RoleOwner),
		Config: configexport.Source{Version: "test", Settings: ops.Settings, Secrets: secretstest.New(),
			Named: []configexport.NamedSecret{{Name: "Controller certificate key", Key: "crsp/controller/ca-key"}}},
		Now: func() time.Time { return time.Date(2026, 11, 14, 10, 5, 0, 0, time.Local) }})
	return &opsEnv{mux: mux, ops: ops, dir: dir}
}

func (e *opsEnv) do(method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(audit.WithActor(context.Background(), audit.Actor{Login: "octo", ID: "7", Source: audit.SourceWeb}),
		method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestSettingsGetAndPatch(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("GET", "/api/v1/settings", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"retention_audit_days":400`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do("PATCH", "/api/v1/settings", `{"retention_audit_days":500,"backup_time_local":"04:15"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"retention_audit_days":500`) || !strings.Contains(rec.Body.String(), "04:15") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Out of range: 400 with the range, nothing changed.
	rec = e.do("PATCH", "/api/v1/settings", `{"retention_logs_days":500}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "between 1 and 90") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeBadRequest)
	if rec := e.do("GET", "/api/v1/settings", ""); !strings.Contains(rec.Body.String(), `"retention_logs_days":14`) {
		t.Fatalf("a refused change was applied: %s", rec.Body)
	}
}

func TestEveryWriteHandlerRecords(t *testing.T) {
	e := newOpsEnv(t)
	e.do("PATCH", "/api/v1/settings", `{"max_event_streams":60}`)
	e.do("POST", "/api/v1/backups", "")
	e.do("POST", "/api/v1/backups/drills", "")
	rec := e.do("GET", "/api/v1/audit?limit=50", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var page struct {
		Items []struct {
			Action, Actor, Source, Result string
		}
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range page.Items {
		if it.Actor != "octo" || it.Source != "web" || it.Result != "done" {
			t.Errorf("row not attributed to the request's actor: %+v", it)
		}
		got[it.Action] = true
	}
	for _, a := range []string{"settings.update", "backup.run", "backup.drill"} {
		if !got[a] {
			t.Errorf("no audit row for %s: %v", a, got)
		}
	}
	// Filter by action, bad cursor.
	if rec := e.do("GET", "/api/v1/audit?action=backup.run", ""); strings.Count(rec.Body.String(), `"action"`) != 1 {
		t.Errorf("action filter: %s", rec.Body)
	}
	if rec := e.do("GET", "/api/v1/audit?cursor=zzz", ""); rec.Code != 400 {
		t.Errorf("bad cursor: %d", rec.Code)
	}
}

func TestBackupsListRunDrillRestore(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("POST", "/api/v1/backups", "")
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"state":"good"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var b struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	rec = e.do("GET", "/api/v1/backups", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), b.ID) || !strings.Contains(rec.Body.String(), `"check_result":"ok"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do("POST", "/api/v1/backups/drills", "")
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"state":"passed"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Restore without confirm: 409 needs_confirmation naming what goes.
	rec = e.do("POST", "/api/v1/backups/"+b.ID+"/restore", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "needs_confirmation") || !strings.Contains(rec.Body.String(), "the database") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), e.dir) {
		t.Fatalf("the answer names a server path: %s", rec.Body)
	}
	// Confirmed: 202.
	rec = e.do("POST", "/api/v1/backups/"+b.ID+"/restore?confirm=true", "")
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/backups/nope/restore?confirm=true", ""); rec.Code != 404 {
		t.Fatalf("unknown backup: %d", rec.Code)
	}
}

func TestDrillWithoutBackupIsAConflict(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("POST", "/api/v1/backups/drills", "")
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeConflict)
}

// R5.24 to R5.27: the export holds every matching row, newest first, named for
// what it holds then the stamp.
func TestExportAudit(t *testing.T) {
	e := newOpsEnv(t)
	for i := 0; i < 3; i++ {
		e.do("PATCH", "/api/v1/settings", `{"max_event_streams":`+string(rune('6'+i))+`0}`)
	}
	e.do("POST", "/api/v1/backups", "")
	rec := e.do("GET", "/api/v1/exports/audit", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/csv" {
		t.Fatalf("%d %s", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="crsp-audit-2026-11-14-1005.csv"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) < 5 || !strings.HasPrefix(lines[0], "id,at,actor") {
		t.Fatalf("csv: %q", rec.Body)
	}
	if !strings.Contains(lines[1], "backup") { // newest first
		t.Errorf("first row is not the newest: %s", lines[1])
	}
	// Filter and json.
	rec = e.do("GET", "/api/v1/exports/audit?format=json&action=settings.update", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "crsp-audit-2026-11-14-1005.json") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("json rows %d: %v %s", len(rows), err, rec.Body)
	}
}

// More than one page of rows still all come out.
func TestExportAuditAllPages(t *testing.T) {
	e := newOpsEnv(t)
	for i := 0; i < audit.MaxPage+30; i++ {
		if err := e.ops.Audit.Record(context.Background(), audit.System, "test.row", audit.Target{Type: "t"}, map[string]any{"summary": "=cmd"}); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.do("GET", "/api/v1/exports/audit?action=test.row", "")
	if n := strings.Count(rec.Body.String(), "\n"); n != audit.MaxPage+30+1 {
		t.Fatalf("%d lines", n)
	}
	if !strings.Contains(rec.Body.String(), "'=cmd") {
		t.Error("a formula summary was not neutralised")
	}
}

// R5.29, R5.26: YAML of the configuration; a secret only as set or not set.
func TestExportConfig(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("GET", "/api/v1/exports/config", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/yaml" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="crsp-config-2026-11-14-1005.yaml"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	body := rec.Body.String()
	for _, want := range []string{"retention:", "time_local: \"03:00\"", "name: Controller certificate key", "state: not set"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if rec := e.do("GET", "/api/v1/audit?action=config.export", ""); !strings.Contains(rec.Body.String(), "config.export") {
		t.Errorf("the export was not audited: %s", rec.Body)
	}
}
