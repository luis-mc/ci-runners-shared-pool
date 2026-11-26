package logs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// readAll joins every file under dir.
func readAll(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

type emptySnapshot struct{}

func (emptySnapshot) Snapshot(context.Context) ([]byte, error) { return []byte(`{}`), nil }

func jsonString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// TestNoSecretInAnySink plants one value of every secret kind through each
// sink that exists today and fails when a value or its base64 form comes out
// (R6.6). The agent log line has no sink yet (R5.20).
func TestNoSecretInAnySink(t *testing.T) {
	ctx := context.Background()
	r := plantedRedactor()
	dir := t.TempDir()

	logger, closer, err := Setup(Options{Dir: filepath.Join(dir, "logs"), Mode: "controller", Version: "t", Redactor: r})
	if err != nil {
		t.Fatal(err)
	}
	crash := &Crasher{Dir: filepath.Join(dir, "crash"), Mode: "controller", Version: "t", R: r,
		Now: func() time.Time { return time.Date(2026, 11, 26, 1, 2, 3, 0, time.UTC) }}

	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	audit.SetRedactor(r.Tagged)
	api.SetErrorRedactor(r.Tagged)
	t.Cleanup(func() { audit.SetRedactor(nil); api.SetErrorRedactor(nil) })
	alog := audit.New(db)

	hub := events.NewHub(events.Options{Snapshotter: emptySnapshot{}, Redact: r.TaggedBytes})
	sub, err := hub.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	var stream, apiBody strings.Builder
	var auditSummaries []string
	for i, c := range secretCases {
		text := c.planted()
		logger.Info("log call "+text, "attr", text)
		logger.Error("error value", "err", fmt.Errorf("wrapped: %w", errors.New(text)))
		crash.Recover(func() { panic(text) })

		if err := alog.Record(ctx, audit.System, "test.plant", audit.Target{Type: "t", ID: fmt.Sprint(i)}, map[string]any{"summary": "did " + text}); err != nil {
			t.Fatal(err)
		}

		hub.Publish(events.JobStarted, []byte(`{"name":"`+jsonString(text)+`"}`))

		rec := httptest.NewRecorder()
		api.WriteError(rec, http.StatusBadRequest, api.CodeBadRequest, "bad value "+text)
		apiBody.WriteString(rec.Body.String())
	}
	_ = closer.Close()
	for range len(secretCases) + 1 { // the snapshot comes first
		select {
		case ev := <-sub.Events():
			stream.Write(ev.Data)
		case <-time.After(5 * time.Second):
			t.Fatal("stream event missing")
		}
	}
	page, err := alog.List(ctx, audit.Filter{Limit: audit.MaxPage})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		auditSummaries = append(auditSummaries, it.Summary)
	}
	var rawAudit strings.Builder
	rows, err := db.ListAudit(ctx, store.AuditFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		rawAudit.WriteString(row.Detail)
	}

	sinks := map[string]string{
		"log file (log call and error)": readAll(t, filepath.Join(dir, "logs")),
		"crash file":                    readAll(t, filepath.Join(dir, "crash")),
		"audit summary":                 strings.Join(auditSummaries, "\n"),
		"audit stored detail":           rawAudit.String(),
		"stream event":                  stream.String(),
		"API error body":                apiBody.String(),
	}
	for name, out := range sinks {
		if out == "" {
			t.Errorf("%s: nothing was written, the test proves nothing", name)
		}
		for _, c := range secretCases {
			for _, f := range c.forms() {
				if strings.Contains(out, f) || strings.Contains(out, jsonString(f)) {
					t.Errorf("%s: secret kind %s appears as %q", name, c.kind, f)
				}
			}
		}
	}
}
