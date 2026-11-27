package runnerswap

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHost struct {
	calls     []string
	failStart map[int]bool // 1-based start call numbers that fail
	failStop  bool
	starts    int
}

func (h *fakeHost) Stop(_ context.Context, s string) error {
	h.calls = append(h.calls, "stop "+s)
	if h.failStop {
		return errors.New("stop refused")
	}
	return nil
}

func (h *fakeHost) Start(_ context.Context, s string) error {
	h.starts++
	h.calls = append(h.calls, "start "+s)
	if h.failStart[h.starts] {
		return errors.New("start refused")
	}
	return nil
}

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func read(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, body string, parts ...string) {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runnerFolder makes a runner folder on version 2.329.0 with state the swap must keep.
func runnerFolder(t *testing.T) string {
	t.Helper()
	f := t.TempDir()
	write(t, `{"name":"r1","runner_version":"2.329.0","account":"crsp-r1"}`, f, ".crsp.json")
	write(t, "old-listener", f, "bin", "Runner.Listener")
	write(t, "old-run", f, "run.sh")
	write(t, "registration", f, ".runner")
	write(t, "creds", f, ".credentials")
	write(t, "env", f, ".env")
	write(t, "a job file", f, "_work", "job.txt")
	return f
}

func newBundle(t *testing.T) []byte {
	t.Helper()
	return tgz(t, map[string]string{"bin/Runner.Listener": "new-listener", "run.sh": "new-run", "externals/node/node": "node"})
}

func TestSwapOK(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{}
	res, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "2.329.0" || res.Current || res.RolledBack {
		t.Fatalf("result %+v", res)
	}
	if got := strings.Join(h.calls, ","); got != "stop svc,start svc" {
		t.Fatalf("calls %s", got)
	}
	if read(t, f, "bin", "Runner.Listener") != "new-listener" || read(t, f, "run.sh") != "new-run" || read(t, f, "externals", "node", "node") != "node" {
		t.Fatal("new files not in place")
	}
	// state is untouched, the previous version is kept, the stage is gone
	for p, want := range map[string]string{".runner": "registration", ".credentials": "creds", ".env": "env", filepath.Join("_work", "job.txt"): "a job file"} {
		if read(t, f, p) != want {
			t.Fatalf("%s changed", p)
		}
	}
	if read(t, f, PreviousDir, "bin", "Runner.Listener") != "old-listener" || read(t, f, PreviousDir, "run.sh") != "old-run" || read(t, f, PreviousDir, ".version") != "2.329.0" {
		t.Fatal("previous files not kept")
	}
	if _, err := os.Stat(filepath.Join(f, StageDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stage left behind")
	}
	if !strings.Contains(read(t, f, ".crsp.json"), `"2.330.0"`) || !strings.Contains(read(t, f, ".crsp.json"), "crsp-r1") {
		t.Fatalf("meta: %s", read(t, f, ".crsp.json"))
	}
}

func TestSwapCurrentChangesNothing(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{}
	res, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.329.0", SHA256: sum(b), Bundle: b})
	if err != nil || !res.Current || len(h.calls) != 0 {
		t.Fatalf("res %+v err %v calls %v", res, err, h.calls)
	}
}

func TestChecksumMismatchRefused(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{}
	bad := append([]byte(nil), b...)
	bad[len(bad)-1] ^= 0xff
	_, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: bad})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err %v", err)
	}
	if len(h.calls) != 0 || read(t, f, "run.sh") != "old-run" {
		t.Fatal("something was touched")
	}
	if _, err := os.Stat(filepath.Join(f, StageDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stage made before the check")
	}
}

func TestNotManagedRefused(t *testing.T) {
	b := newBundle(t)
	_, err := (&Swapper{Host: &fakeHost{}}).Swap(t.Context(), Request{Folder: t.TempDir(), Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if !errors.Is(err, ErrNotManaged) {
		t.Fatalf("err %v", err)
	}
}

func TestFailureMidSwapRollsBack(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{}
	n := 0
	s := &Swapper{Host: h, Rename: func(a, c string) error {
		n++
		if n == 4 { // two entries are through, the third fails
			return errors.New("disk full")
		}
		return os.Rename(a, c)
	}}
	res, err := s.Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if err == nil || !res.RolledBack || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("res %+v err %v", res, err)
	}
	if read(t, f, "bin", "Runner.Listener") != "old-listener" || read(t, f, "run.sh") != "old-run" {
		t.Fatal("previous files not back")
	}
	if _, err := os.Stat(filepath.Join(f, "externals")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a new entry stayed")
	}
	if !strings.Contains(read(t, f, ".crsp.json"), `"2.329.0"`) || read(t, f, "_work", "job.txt") != "a job file" {
		t.Fatal("meta or state changed")
	}
	if got := strings.Join(h.calls, ","); got != "stop svc,stop svc,start svc" {
		t.Fatalf("calls %s", got)
	}
}

func TestStartFailureRollsBack(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{failStart: map[int]bool{1: true}}
	res, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if err == nil || !res.RolledBack {
		t.Fatalf("res %+v err %v", res, err)
	}
	if read(t, f, "bin", "Runner.Listener") != "old-listener" || h.starts != 2 {
		t.Fatalf("not rolled back, starts %d", h.starts)
	}
}

func TestRollbackThatFailsSaysSo(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{failStart: map[int]bool{1: true, 2: true}}
	res, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if err == nil || res.RolledBack || !strings.Contains(err.Error(), "rollback also failed") {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestStopFailureChangesNothing(t *testing.T) {
	f, b := runnerFolder(t), newBundle(t)
	h := &fakeHost{failStop: true}
	res, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(b), Bundle: b})
	if err == nil || res.RolledBack || read(t, f, "run.sh") != "old-run" {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestBadArchiveChangesNothing(t *testing.T) {
	f := runnerFolder(t)
	junk := []byte("not an archive")
	h := &fakeHost{}
	_, err := (&Swapper{Host: h}).Swap(t.Context(), Request{Folder: f, Service: "svc", Version: "2.330.0", SHA256: sum(junk), Bundle: junk})
	if !errors.Is(err, ErrBadArchive) || len(h.calls) != 0 || read(t, f, "run.sh") != "old-run" {
		t.Fatalf("err %v calls %v", err, h.calls)
	}
}

func TestUnpackRefusesPathsOutside(t *testing.T) {
	b := tgz(t, map[string]string{"../evil": "x"})
	if err := Unpack(b, filepath.Join(t.TempDir(), "d")); !errors.Is(err, ErrBadArchive) {
		t.Fatalf("tar: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../evil")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(buf.Bytes(), filepath.Join(t.TempDir(), "d")); !errors.Is(err, ErrBadArchive) {
		t.Fatalf("zip: %v", err)
	}
}

func TestUnpackZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bin/Runner.Listener.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("exe")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(t.TempDir(), "d")
	if err := Unpack(buf.Bytes(), d); err != nil {
		t.Fatal(err)
	}
	if read(t, d, "bin", "Runner.Listener.exe") != "exe" {
		t.Fatal("not unpacked")
	}
}

func TestServiceHostCommands(t *testing.T) {
	var got []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "", nil
	}
	for _, os := range []string{"linux", "macos", "windows"} {
		h := ServiceHost{OS: os, Run: run}
		if err := h.Stop(t.Context(), "svc"); err != nil {
			t.Fatal(err)
		}
		if err := h.Start(t.Context(), "svc"); err != nil {
			t.Fatal(err)
		}
	}
	if got[0] != "systemctl stop svc" || got[1] != "systemctl start svc" || got[2] != "launchctl stop svc" || !strings.HasPrefix(got[4], "powershell.exe ") || !strings.HasSuffix(got[5], "svc") {
		t.Fatalf("%q", got)
	}
	if err := (ServiceHost{OS: "linux", Run: run}).Stop(t.Context(), "--now"); err == nil {
		t.Fatal("an option was taken as a service name")
	}
}
