package collect

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// fakeGitHub answers from maps; calls are counted.
type fakeGitHub struct {
	mu        sync.Mutex
	logs      map[int64]string
	size      map[int64]int64 // size reported for a log, -1 when absent
	artifacts []Artifact
	zips      map[int64][]byte
	gone      map[int64]bool // job ids whose log is gone
	logCalls  int
	zipCalls  int
	listCalls int
}

func (f *fakeGitHub) JobLog(_ context.Context, _ int64, _, _ string, jobID int64) (io.ReadCloser, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logCalls++
	if f.gone[jobID] {
		return nil, 0, ErrGone
	}
	size := int64(-1)
	if s, ok := f.size[jobID]; ok {
		size = s
	}
	return io.NopCloser(strings.NewReader(f.logs[jobID])), size, nil
}

func (f *fakeGitHub) RunArtifacts(context.Context, int64, string, string, int64) ([]Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	return append([]Artifact(nil), f.artifacts...), nil
}

func (f *fakeGitHub) DownloadArtifact(_ context.Context, _ int64, _, _ string, id int64) (io.ReadCloser, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.zipCalls++
	b, ok := f.zips[id]
	if !ok {
		return nil, 0, ErrGone
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (f *fakeGitHub) CommitTree(context.Context, int64, string, string, string) (string, error) {
	return "", errors.New("not used")
}

type fakeRepos struct{}

func (fakeRepos) Repository(_ context.Context, id int64) (Repo, error) {
	return Repo{Owner: "octo", Name: "repo", InstallationID: 7}, nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rig is a collector over a real store and a real vault in a temp folder.
type rig struct {
	gh    *fakeGitHub
	db    *store.Store
	vault *FSVault
	svc   *Service
	clk   *clock
	rd    *redact.Redactor
	sink  *fakeSink
	lim   Limits
}

type fakeSink struct {
	mu   sync.Mutex
	got  []ReportRecord
	fail bool
}

func (s *fakeSink) ReportStored(_ context.Context, r ReportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, r)
	if s.fail {
		return errors.New("parse failed")
	}
	return nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{
		gh:   &fakeGitHub{logs: map[int64]string{}, size: map[int64]int64{}, zips: map[int64][]byte{}, gone: map[int64]bool{}},
		db:   openStore(t),
		clk:  &clock{t: time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)},
		rd:   redact.New(),
		sink: &fakeSink{},
		lim:  Limits{Keep: true, Days: 30 * 24 * time.Hour, BytesPerRepository: 1 << 30},
	}
	r.vault = &FSVault{Dir: t.TempDir(), Redactor: r.rd}
	r.build(&HourlyBudget{Now: r.clk.now})
	return r
}

func (r *rig) build(b Budget) {
	r.svc = New(Config{
		GitHub: r.gh, Vault: r.vault, Index: r.db, Repos: fakeRepos{}, Budget: b, Sink: r.sink,
		Limits: func(context.Context) (Limits, error) { return r.lim, nil },
		Now:    r.clk.now, GuardrailArtifact: "test-guardrails*", TempDir: "",
	})
}

func (r *rig) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 100; i++ {
		handled, _, err := r.svc.Step(context.Background())
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if !handled {
			return
		}
	}
	t.Fatal("the queue did not drain")
}

func (r *rig) readLog(t *testing.T, path string) string {
	t.Helper()
	rc, err := r.vault.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const sampleLog = `2026-11-20T10:00:00.1000000Z ##[group]Run npm test
2026-11-20T10:00:00.1000000Z npm test
2026-11-20T10:00:00.1000000Z shell: /usr/bin/bash -e {0}
2026-11-20T10:00:00.1000000Z env:
2026-11-20T10:00:00.1000000Z   DEPLOY_TARGET: hunter2-super-value
2026-11-20T10:00:00.1000000Z   NODE_ENV: production
2026-11-20T10:00:00.1000000Z   MULTI: first line
2026-11-20T10:00:00.1000000Z     second line of the value
2026-11-20T10:00:00.1000000Z ##[endgroup]
2026-11-20T10:00:01.0000000Z PASS src/a.test.ts
`

// R5.2: the log is redacted, the values of the env: block are dropped (names kept), the file is gzipped
// under ci-data/<repository>/logs and indexed.
func TestLogRedactedAndEnvValuesDropped(t *testing.T) {
	r := newRig(t)
	r.rd.Add("hunter2-super-value")
	r.gh.logs[55] = sampleLog + "2026-11-20T10:00:02.0000000Z token ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789 used\n"
	r.svc.JobCompleted(context.Background(), 3, 900, 2, 55)
	r.drain(t)

	row, ok, err := r.db.CIJobLogOf(context.Background(), 55)
	if err != nil || !ok {
		t.Fatalf("row = %v %v", ok, err)
	}
	if row.State != "stored" || row.Path != "ci-data/3/logs/900-2-55.log.gz" {
		t.Fatalf("row = %+v", row)
	}
	if row.Lines != 11 || row.Sha256 == "" || row.Bytes <= 0 || row.RawBytes <= 0 || row.ExpiresAt != "2026-12-20T10:30:00Z" {
		t.Fatalf("row = %+v", row)
	}
	text := r.readLog(t, row.Path)
	for _, leaked := range []string{"hunter2-super-value", "production", "first line", "second line of the value", "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the stored log still holds %q:\n%s", leaked, text)
		}
	}
	for _, kept := range []string{"DEPLOY_TARGET: ***", "NODE_ENV: ***", "MULTI: ***", "npm test", "PASS src/a.test.ts"} {
		if !strings.Contains(text, kept) {
			t.Errorf("the stored log lost %q:\n%s", kept, text)
		}
	}
	if strings.Count(text, "\n") != 10 { // the continuation line of MULTI is gone
		t.Errorf("lines = %d:\n%s", strings.Count(text, "\n"), text)
	}
}

// R5.2: a log over the cap is not stored; its row says too_large. A log GitHub no longer has says missing.
func TestTooLargeNotStored(t *testing.T) {
	r := newRig(t)
	r.vault.MaxLog = 1024
	r.gh.logs[1] = strings.Repeat("a line of text\n", 200) // 3000 bytes, size not reported
	r.gh.logs[2] = "small"
	r.gh.size[2] = MaxLogBytes + 1 // reported over the real cap: not even read
	r.gh.gone[3] = true
	for id := int64(1); id <= 3; id++ {
		r.svc.JobCompleted(context.Background(), 3, 900, 1, id)
	}
	r.drain(t)
	for id, want := range map[int64]string{1: "too_large", 2: "too_large", 3: "missing"} {
		row, ok, err := r.db.CIJobLogOf(context.Background(), id)
		if err != nil || !ok || row.State != want || row.Path != "" || row.Reason == "" {
			t.Errorf("job %d row = %+v %v %v, want %s", id, row, ok, err, want)
		}
	}
	// nothing is left behind on disk, not even a temp file
	entries, _ := readTree(r.vault.Dir)
	if len(entries) != 0 {
		t.Errorf("files left: %v", entries)
	}
}

// A second JobCompleted for a stored log does not call GitHub again; keeping off downloads nothing.
func TestLogOnceAndKeepOff(t *testing.T) {
	r := newRig(t)
	r.gh.logs[1] = "hello\n"
	r.svc.JobCompleted(context.Background(), 3, 900, 1, 1)
	r.drain(t)
	r.svc.JobCompleted(context.Background(), 3, 900, 1, 1)
	r.drain(t)
	if r.gh.logCalls != 1 {
		t.Fatalf("log calls = %d, want 1", r.gh.logCalls)
	}
	r.lim.Keep = false
	r.gh.logs[2] = "hello\n"
	r.svc.JobCompleted(context.Background(), 3, 900, 1, 2)
	r.drain(t)
	if _, ok, _ := r.db.CIJobLogOf(context.Background(), 2); ok || r.gh.logCalls != 1 {
		t.Fatalf("keeping is off but job 2 was fetched (%d calls)", r.gh.logCalls)
	}
}

// R6.8: a redactor that panics stores nothing and the loop goes on (spec section 8: closed).
func TestRedactorFailureStoresNothing(t *testing.T) {
	r := newRig(t)
	r.vault.Redactor = panicRedactor{}
	r.gh.logs[1] = "line one\nline two\n"
	r.svc.JobCompleted(context.Background(), 3, 900, 1, 1)
	r.drain(t)
	row, ok, _ := r.db.CIJobLogOf(context.Background(), 1)
	if !ok || row.State != "failed" || row.Path != "" {
		t.Fatalf("row = %+v", row)
	}
	if entries, _ := readTree(r.vault.Dir); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

type panicRedactor struct{}

func (panicRedactor) Redact(string) string { panic("boom") }

func readTree(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return err
	})
	return out, err
}
