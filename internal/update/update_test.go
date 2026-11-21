package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionsStableOnly(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"1.4.0", "1.5.0", true},
		{"1.4.0", "v1.4.1", true},
		{"1.4.0", "1.4.0", false},
		{"1.4.0", "1.3.9", false},
		{"1.9.0", "1.10.0", true},
		{"dev", "1.5.0", false},          // a developer build is never replaced
		{"edge-abc1234", "1.5.0", false}, // nor an edge build
		{"1.4.0", "edge-abc1234", false}, // edge is never offered
		{"1.4.0", "1.5.0-rc1", false},    // no pre-release
		{"1.4.0", "01.5.0", false},       // malformed
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestAssetNamePerOS(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "crsp-windows-amd64.msi",
		"linux":   "crsp-linux-amd64.tar.gz",
		"darwin":  "crsp-darwin-amd64.tar.gz",
	} {
		if got := AssetName(goos, "amd64"); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
	if AssetName("windows", "arm64") != "crsp-windows-arm64.msi" {
		t.Error("arm64 msi name")
	}
}

func TestVersionedAssetNames(t *testing.T) {
	for plain, want := range map[string]string{
		"crsp-windows-amd64.msi":  "crsp-windows-amd64_0.1.15.msi",
		"crsp-windows-arm64.msi":  "crsp-windows-arm64_0.1.15.msi",
		"crsp-darwin-arm64.pkg":   "crsp-darwin-arm64_0.1.15.pkg",
		"crsp-linux-amd64.deb":    "crsp-linux-amd64_0.1.15.deb",
		"crsp-linux-arm64.rpm":    "crsp-linux-arm64_0.1.15.rpm",
		"crsp-linux-amd64.tar.gz": "crsp-linux-amd64_0.1.15.tar.gz",
		"crsp-windows-amd64.zip":  "crsp-windows-amd64_0.1.15.zip",
		"SHA256SUMS":              "SHA256SUMS",
		"crsp-sbom-go.spdx.json":  "crsp-sbom-go.spdx.json",
	} {
		if got := VersionedName(plain, "0.1.15"); got != want {
			t.Errorf("VersionedName(%q) = %q, want %q", plain, got, want)
		}
		if got := VersionedName(plain, "v0.1.15"); got != want {
			t.Errorf("a leading v must not reach the name: %q", got)
		}
		if got := PlainName(want); got != plain {
			t.Errorf("PlainName(%q) = %q, want %q", want, got, plain)
		}
	}
	if VersionedName("crsp-linux-amd64.deb", "") != "crsp-linux-amd64.deb" {
		t.Error("no version, no change")
	}
}

func TestPickAssetPrefersTheVersionedName(t *testing.T) {
	plain := "crsp-windows-amd64.msi"
	versioned := "crsp-windows-amd64_0.1.15.msi"
	both := Release{Version: "0.1.15", Assets: map[string]string{plain: "p", versioned: "v"}}
	if got := PickAsset(both, plain); got != versioned {
		t.Errorf("both present: %q", got)
	}
	old := Release{Version: "0.1.14", Assets: map[string]string{plain: "p"}}
	if got := PickAsset(old, plain); got != plain {
		t.Errorf("older release keeps the plain name: %q", got)
	}
	none := Release{Version: "0.1.15", Assets: map[string]string{}}
	if got := PickAsset(none, plain); got != versioned {
		t.Errorf("missing asset names the versioned one: %q", got)
	}
	sums := []byte(strings.Repeat("a", 64) + "  " + plain + "\n")
	if got := PickFromSums(sums, plain, "0.1.15"); got != plain {
		t.Errorf("sums with the plain line: %q", got)
	}
	sums = []byte(strings.Repeat("a", 64) + "  " + versioned + "\n")
	if got := PickFromSums(sums, plain, "0.1.15"); got != versioned {
		t.Errorf("sums with the versioned line: %q", got)
	}
}

func TestGitHubSourceStableOnly(t *testing.T) {
	body := `{"tag_name":"v1.5.0","draft":false,"prerelease":false,"html_url":"https://github.com/x/y/releases/tag/v1.5.0","assets":[{"name":"SHA256SUMS","browser_download_url":"https://github.com/s"}]}`
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	rel, err := GitHubSource{Base: srv.URL}.Latest(context.Background())
	if err != nil || rel.Version != "1.5.0" || rel.Assets["SHA256SUMS"] != "https://github.com/s" {
		t.Fatalf("%+v %v", rel, err)
	}
	if path.Load() != "/repos/"+Repo+"/releases/latest" {
		t.Fatalf("asked %v", path.Load())
	}
	for _, bad := range []string{
		strings.Replace(body, `"prerelease":false`, `"prerelease":true`, 1),
		strings.Replace(body, `"draft":false`, `"draft":true`, 1),
		strings.Replace(body, `v1.5.0"`, `edge"`, 1),
	} {
		body = bad
		if _, err := (GitHubSource{Base: srv.URL}).Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
			t.Errorf("a draft, pre-release or edge release must be refused: %v", err)
		}
	}
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type attest struct{ err error }

func (a attest) Verify(context.Context, string) error { return a.err }

func TestVerifyChecksAndProvenance(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "crsp-linux-amd64.tar.gz")
	_ = os.WriteFile(f, []byte("payload"), 0o600)
	good := []byte(sum([]byte("payload")) + "  crsp-linux-amd64.tar.gz\n")
	ctx := context.Background()
	if err := Verify(ctx, f, "crsp-linux-amd64.tar.gz", good, attest{}); err != nil {
		t.Fatal(err)
	}
	wrong := []byte(sum([]byte("other")) + "  crsp-linux-amd64.tar.gz\n")
	var fl *Failure
	if err := Verify(ctx, f, "crsp-linux-amd64.tar.gz", wrong, attest{}); !errors.As(err, &fl) || fl.Check != "checksum" {
		t.Fatalf("a wrong checksum must be refused as checksum: %v", err)
	}
	if err := Verify(ctx, f, "crsp-linux-amd64.tar.gz", []byte("nothing here\n"), attest{}); !errors.As(err, &fl) || fl.Check != "checksum" {
		t.Fatalf("a missing line must be refused: %v", err)
	}
	if err := Verify(ctx, f, "crsp-linux-amd64.tar.gz", good, attest{err: errors.New("no attestation")}); !errors.As(err, &fl) || fl.Check != "provenance" {
		t.Fatalf("a failed attestation must be refused as provenance: %v", err)
	}
	if err := Verify(ctx, f, "crsp-linux-amd64.tar.gz", good, nil); err == nil {
		t.Fatal("no attester must fail closed")
	}
}

func TestLockExcludesAndTakesOverStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	alive := func(int) bool { return true }
	l, err := TakeLock(dir, now, 100, alive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TakeLock(dir, now, 200, alive); !errors.Is(err, ErrInProgress) {
		t.Fatalf("a live lock must refuse: %v", err)
	}
	if _, err := TakeLock(dir, now, 200, func(int) bool { return false }); err != nil {
		t.Fatalf("a lock whose process is gone is stale: %v", err)
	}
	l.Release()
	// An old lock is stale even when the pid looks alive.
	_, _ = TakeLock(dir, now.Add(-2*LockMaxAge), 100, alive)
	if _, err := TakeLock(dir, now, 300, alive); err != nil {
		t.Fatalf("an old lock must be taken over: %v", err)
	}
}

func TestAutoBackoff(t *testing.T) {
	now := time.Now()
	if ok, _ := AutoAllowed(Failures{}, "1.5.0", now); !ok {
		t.Fatal("no failure: allowed")
	}
	f := Failures{Version: "1.5.0", Count: 1, FailedAt: now.Add(-30 * time.Minute)}
	if ok, why := AutoAllowed(f, "1.5.0", now); ok || why == "" {
		t.Fatal("30 minutes after the first failure it must wait an hour")
	}
	f.FailedAt = now.Add(-61 * time.Minute)
	if ok, _ := AutoAllowed(f, "1.5.0", now); !ok {
		t.Fatal("after an hour the second try may start")
	}
	f = Failures{Version: "1.5.0", Count: 2, FailedAt: now.Add(-2 * time.Hour)}
	if ok, _ := AutoAllowed(f, "1.5.0", now); ok {
		t.Fatal("the second failure waits four hours")
	}
	f.Count = AutoTries
	if ok, why := AutoAllowed(f, "1.5.0", now.Add(100*time.Hour)); ok || !strings.Contains(why, "gave up") {
		t.Fatal("after three tries the automatic path gives up")
	}
	if ok, _ := AutoAllowed(f, "1.6.0", now); !ok {
		t.Fatal("a newer release gets its own tries")
	}
}

func TestExtractBinaryOnlyTheBinary(t *testing.T) {
	dir := t.TempDir()
	arch := filepath.Join(dir, "a.tar.gz")
	f, _ := os.Create(arch)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"crsp-linux-amd64/README.md", "readme"},
		{"../../evil", "evil"},
		{"crsp-linux-amd64/crsp", "BINARY"},
	} {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	dst := filepath.Join(dir, "crsp.new")
	if err := ExtractBinary(arch, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "BINARY" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "..", "evil")); err == nil {
		t.Fatal("a path outside the target was written")
	}
}

// ---------------------------------------------------------------- manager

type fakeDrainer struct {
	minutes   int
	mu        sync.Mutex
	jobs      []Job
	begun     []Kind
	cancelled int
}

func (d *fakeDrainer) RunningJobs(context.Context) ([]Job, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs, nil
}

func (d *fakeDrainer) cancelCount() int { d.mu.Lock(); defer d.mu.Unlock(); return d.cancelled }
func (d *fakeDrainer) BeginDrain(_ context.Context, k Kind, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.begun = append(d.begun, k)
	return nil
}
func (d *fakeDrainer) DrainTimeoutMinutes(context.Context) int {
	if d.minutes != 0 {
		return d.minutes
	}
	return 60
}
func (d *fakeDrainer) CancelDrain(context.Context, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelled++
	return nil
}

type fakeSource struct{ rel Release }

func (s fakeSource) Latest(context.Context) (Release, error) { return s.rel, nil }

type harness struct {
	m      *Manager
	d      *fakeDrainer
	auto   *bool
	spawns []Plan
	dl     []string
	dir    string
}

func newHarness(t *testing.T, goos string, auto bool, payloadOK bool) *harness {
	t.Helper()
	return newHarnessNamed(t, goos, auto, payloadOK, true)
}

// newHarnessNamed makes a release whose assets carry the version in the name
// (versioned) or the plain name of releases up to 0.1.14.
func newHarnessNamed(t *testing.T, goos string, auto bool, payloadOK, versioned bool) *harness {
	t.Helper()
	h := &harness{d: &fakeDrainer{}, auto: &auto, dir: t.TempDir()}
	asset := AssetName(goos, "amd64")
	if versioned {
		asset = VersionedName(asset, "1.5.0")
	}
	payload := []byte("payload")
	sumLine := sum(payload)
	if !payloadOK {
		sumLine = sum([]byte("tampered"))
	}
	rel := Release{Version: "1.5.0", Tag: "v1.5.0", URL: "https://github.com/x/releases/v1.5.0", Assets: map[string]string{
		asset: "https://github.com/dl/" + asset, "SHA256SUMS": "https://github.com/dl/SHA256SUMS",
	}}
	exe := filepath.Join(h.dir, "bin", "crsp")
	_ = os.MkdirAll(filepath.Dir(exe), 0o750)
	_ = os.WriteFile(exe, []byte("old"), 0o755) //nolint:gosec // test
	h.m = New(Options{
		Current: "1.4.0", Source: fakeSource{rel}, Attester: attest{}, Drainer: h.d,
		AutoUpdate: func(context.Context) bool { return *h.auto },
		Dir:        filepath.Join(h.dir, "updates"), GOOS: goos, GOARCH: "amd64", Exe: exe, HealthURL: "http://127.0.0.1:1/healthz",
		Fetch: func(_ context.Context, url, dst string) error {
			h.dl = append(h.dl, url)
			if strings.HasSuffix(url, "SHA256SUMS") {
				return os.WriteFile(dst, []byte(sumLine+"  "+asset+"\n"), 0o600)
			}
			return os.WriteFile(dst, payload, 0o600)
		},
		Extract:   func(_, dst string) error { return os.WriteFile(dst, []byte("new"), 0o755) }, //nolint:gosec // test
		SelfCheck: func(context.Context, string, string) error { return nil },
		Spawn: func(_ context.Context, _ string, p Plan, _ string) (int, error) {
			h.spawns = append(h.spawns, p)
			return 4242, nil
		},
	})
	return h
}

// A release up to 0.1.14 has the plain name; the update still finds it, and the
// copy under the updates folder keeps that name.
func TestOlderReleaseWithPlainNamesStillInstalls(t *testing.T) {
	h := newHarnessNamed(t, "windows", false, true, false)
	h.m.o.StartMSI = func(context.Context, string, string) (int, error) { return 77, nil }
	h.m.o.MSILog = filepath.Join(h.dir, "logs", "msi-update.log")
	if _, err := h.m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	if len(h.dl) != 2 || !strings.HasSuffix(h.dl[0], "/crsp-windows-amd64.msi") {
		t.Fatalf("plain name fallback: %v", h.dl)
	}
}

func TestDownloadedCopyKeepsTheVersionedName(t *testing.T) {
	h := newHarness(t, "windows", false, true)
	h.m.o.StartMSI = func(context.Context, string, string) (int, error) { return 77, nil }
	h.m.o.MSILog = filepath.Join(h.dir, "logs", "msi-update.log")
	if _, err := h.m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(h.dir, "updates", "1.5.0", "crsp-windows-amd64_1.5.0.msi")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the downloaded copy is not at %s: %v", want, err)
	}
}

func TestApplyRefusedWithoutConfirmedListsJobs(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	h.d.jobs = []Job{{Repository: "luis-mc/app", Workflow: "ci", RunID: 7, Job: "test"}}
	ctx := context.Background()
	if _, err := h.m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range []Kind{KindUpdate, KindRestart} {
		_, err := h.m.Apply(ctx, k, false, "octo")
		var nc *NeedsConfirmation
		if !errors.As(err, &nc) || len(nc.Jobs) != 1 || nc.Jobs[0].RunID != 7 {
			t.Fatalf("%s: want needs-confirmation with the running job, got %v", k, err)
		}
	}
	if len(h.dl) != 0 || len(h.d.begun) != 0 || len(h.spawns) != 0 {
		t.Fatalf("a refused request must download, drain and install nothing: dl=%v begun=%v", h.dl, h.d.begun)
	}
}

func TestAutoOffDoesNotInstallByItself(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	st, err := h.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "1.5.0" || st.AutoUpdate || st.State != StateIdle {
		t.Fatalf("%+v", st)
	}
	if len(h.dl) != 0 || len(h.d.begun) != 0 {
		t.Fatal("with auto-update off, a check never downloads or drains")
	}
}

func TestAutoOnDownloadsVerifiesThenDrainsThenInstallsOnIdle(t *testing.T) {
	h := newHarness(t, "linux", true, true)
	ctx := context.Background()
	h.d.jobs = []Job{{Repository: "luis-mc/app", RunID: 1, Job: "build"}}
	st, err := h.m.AutoCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateWaitingForJobs || len(h.d.begun) != 1 || h.d.begun[0] != KindUpdate {
		t.Fatalf("auto-update must drain: %+v begun=%v", st, h.d.begun)
	}
	if len(h.dl) != 2 {
		t.Fatalf("the asset and SHA256SUMS: %v", h.dl)
	}
	// A job still runs when the drain calls back: nothing is installed, the drain goes on waiting.
	if err := h.m.OnIdle(ctx, KindUpdate); !errors.Is(err, ErrNotIdle) || len(h.spawns) != 0 {
		t.Fatalf("a running job must stop the install: err=%v spawns=%d", err, len(h.spawns))
	}
	h.d.jobs = nil
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil {
		t.Fatal(err)
	}
	if len(h.spawns) != 1 || h.spawns[0].To != "1.5.0" || h.spawns[0].From != "1.4.0" || h.spawns[0].Kind != "update" {
		t.Fatalf("%+v", h.spawns)
	}
	if h.m.Status(ctx).State != StateRestarting {
		t.Fatal("state after the helper started")
	}
	// The lock is handed to the helper, so a second install is refused.
	if _, err := TakeLock(filepath.Join(h.dir, "updates"), time.Now(), 1, func(int) bool { return true }); !errors.Is(err, ErrInProgress) {
		t.Fatalf("the helper holds the lock: %v", err)
	}
}

func TestBadChecksumOrProvenanceStopsBeforeDrain(t *testing.T) {
	h := newHarness(t, "linux", true, false)
	st, err := h.m.AutoCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateFailed || !strings.Contains(st.Error, "checksum") || len(h.d.begun) != 0 || len(h.spawns) != 0 {
		t.Fatalf("%+v begun=%v", st, h.d.begun)
	}
	// The failed try is recorded, so the automatic path backs off.
	f, _ := ReadFailures(filepath.Join(h.dir, "updates"))
	if f.Version != "1.5.0" || f.Count != 1 {
		t.Fatalf("%+v", f)
	}
	h2 := newHarness(t, "linux", true, true)
	h2.m.o.Attester = attest{err: &Failure{Check: "provenance", Reason: "signer mismatch"}}
	st, _ = h2.m.AutoCheck(context.Background())
	if st.State != StateFailed || !strings.Contains(st.Error, "provenance") || len(h2.d.begun) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestManualYesDrainsAndWindowsStartsTheMSIItself(t *testing.T) {
	h := newHarness(t, "windows", false, true)
	ctx := context.Background()
	var msi, log string
	h.m.o.StartMSI = func(_ context.Context, f, l string) (int, error) { msi, log = f, l; return 77, nil }
	h.m.o.MSILog = filepath.Join(h.dir, "logs", "msi-update.log")
	if _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	if len(h.dl) != 2 || !strings.HasSuffix(h.dl[0], "crsp-windows-amd64_1.5.0.msi") {
		t.Fatalf("windows downloads the MSI: %v", h.dl)
	}
	if _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second request while one waits: %v", err)
	}
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil {
		t.Fatal(err)
	}
	if len(h.spawns) != 0 {
		t.Fatal("windows needs no helper: the controller starts msiexec itself")
	}
	if !strings.HasSuffix(msi, "crsp-windows-amd64_1.5.0.msi") || log != h.m.o.MSILog {
		t.Fatalf("%s %s", msi, log)
	}
	if got := strings.Join(MSIArgs(msi, log), "|"); !strings.HasPrefix(got, "/i|") || !strings.HasSuffix(got, "|/qn|/norestart|/l*v|"+log) {
		t.Fatalf("%s", got)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "updates", PendingMSIFile)); err != nil {
		t.Fatal("the pending record must wait for the next start")
	}
}

func TestRestartNeedsNoDownload(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	if _, err := h.m.Apply(ctx, KindRestart, true, "octo"); err != nil {
		t.Fatal(err)
	}
	if len(h.dl) != 0 || len(h.d.begun) != 1 || h.d.begun[0] != KindRestart {
		t.Fatalf("dl=%v begun=%v", h.dl, h.d.begun)
	}
	if err := h.m.OnIdle(ctx, KindRestart); err != nil {
		t.Fatal(err)
	}
	if h.spawns[0].Kind != "restart" {
		t.Fatalf("%+v", h.spawns[0])
	}
}

func TestNoUpdateWhenCurrent(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	h.m.o.Current = "1.5.0"
	if _, err := h.m.Apply(context.Background(), KindUpdate, true, "octo"); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("%v", err)
	}
}

// ---------------------------------------------------------------- helper

type execLog struct {
	calls [][]string
	codes map[string]int
}

func (e *execLog) run(_ context.Context, name string, args ...string) (int, error) {
	e.calls = append(e.calls, append([]string{name}, args...))
	return e.codes[name], nil
}

func helperEnv(e *execLog, health func(string) (string, error)) Env {
	now := time.Unix(1_700_000_000, 0)
	return Env{
		Exec: e.run, Now: func() time.Time { now = now.Add(time.Second); return now }, Sleep: func(time.Duration) {},
		Health: func(_ context.Context, u string) (string, error) { return health(u) },
	}
}

func TestHelperRefusesWindowsUpdate(t *testing.T) {
	p := Plan{Kind: "update", GOOS: "windows", From: "1.4.0", To: "1.5.0", File: "x.msi", Dir: t.TempDir()}
	if err := RunPlan(context.Background(), p, helperEnv(&execLog{}, nil)); err == nil {
		t.Fatal("the MSI update is the controller's, not the helper's")
	}
}

func TestParseMSILog(t *testing.T) {
	if c, ok := ParseMSILog([]byte("x\nMainEngineThread is returning 1603\nMainEngineThread is returning 0\n")); !ok || c != 0 {
		t.Fatalf("the last line wins: %d %v", c, ok)
	}
	if _, ok := ParseMSILog([]byte("still running")); ok {
		t.Fatal("no end line yet")
	}
	// UTF-16 LE with a BOM, as msiexec may write it.
	u := []byte{0xFF, 0xFE}
	for _, r := range "MainEngineThread is returning 3010" {
		u = append(u, byte(r), 0)
	}
	if c, ok := ParseMSILog(u); !ok || c != 3010 {
		t.Fatalf("%d %v", c, ok)
	}
}

func pendingFor(t *testing.T, dir, log string, started time.Time) {
	t.Helper()
	if err := writeJSON(filepath.Join(dir, PendingMSIFile), PendingMSI{From: "1.4.0", To: "1.5.0", File: "x.msi", Log: log, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverMSIReportsAndBacksOff(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	log := filepath.Join(dir, "msi-update.log")

	// Success: the new version runs after the restart.
	pendingFor(t, dir, log, now)
	if !RecoverMSI(dir, "1.5.0", now) {
		t.Fatal("must settle")
	}
	if r, ok := ReadResult(dir); !ok || !r.OK || r.To != "1.5.0" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, PendingMSIFile)); err == nil {
		t.Fatal("the pending record must go")
	}

	// Failure: exit code 1603, old version still runs; counted for the retry rule.
	_ = os.WriteFile(log, []byte("MainEngineThread is returning 1603\n"), 0o600)
	pendingFor(t, dir, log, now)
	if !RecoverMSI(dir, "1.4.0", now) {
		t.Fatal("must settle")
	}
	if r, _ := ReadResult(dir); r.OK || !strings.Contains(r.Error, "1603") || !strings.Contains(r.Error, log) {
		t.Fatalf("%+v", r)
	}
	if f, _ := ReadFailures(dir); f.Count != 1 || f.Version != "1.5.0" {
		t.Fatalf("%+v", f)
	}
	for i := 0; i < 2; i++ {
		pendingFor(t, dir, log, now)
		RecoverMSI(dir, "1.4.0", now)
	}
	if ok, _ := AutoAllowed(readFailures(dir), "1.5.0", now.Add(100*time.Hour)); ok {
		t.Fatal("after three tries the automatic path gives up on the version")
	}

	// Still running: leave it for the next start.
	_ = os.WriteFile(log, []byte("busy"), 0o600)
	pendingFor(t, dir, log, now)
	if RecoverMSI(dir, "1.4.0", now.Add(time.Minute)) {
		t.Fatal("a running install is not settled")
	}
	// Dead chain: no end line for longer than the lock age.
	if !RecoverMSI(dir, "1.4.0", now.Add(LockMaxAge+time.Minute)) {
		t.Fatal("a dead install must be settled")
	}

	// A new Manager shows the failure.
	pendingFor(t, dir, log, now)
	_ = os.WriteFile(log, []byte("MainEngineThread is returning 1603\n"), 0o600)
	m := New(Options{Current: "1.4.0", Dir: dir, GOOS: "windows", Exe: filepath.Join(dir, "crsp.exe")})
	if st := m.Status(context.Background()); st.State != StateFailed || !strings.Contains(st.Error, "1603") {
		t.Fatalf("%+v", st)
	}
}

func unixPlan(t *testing.T, goos string) (Plan, string, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "crsp")
	nw := exe + ".new"
	_ = os.WriteFile(exe, []byte("OLD"), 0o755) //nolint:gosec // test
	_ = os.WriteFile(nw, []byte("NEW"), 0o755)  //nolint:gosec // test
	return Plan{Kind: "update", GOOS: goos, From: "1.4.0", To: "1.5.0", File: nw, Exe: exe, Service: "crsp", HealthURL: "x", HealthTimeout: 5 * time.Second, Dir: dir}, exe, dir
}

func TestHelperUnixSwapsRestartsAndKeepsPrevious(t *testing.T) {
	for goos, want := range map[string]string{"linux": "systemctl|restart|crsp", "darwin": "launchctl|kickstart|-k|system/crsp"} {
		p, exe, _ := unixPlan(t, goos)
		e := &execLog{}
		if err := RunPlan(context.Background(), p, helperEnv(e, func(string) (string, error) { return "1.5.0", nil })); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(exe); string(b) != "NEW" {
			t.Fatalf("%s: binary not replaced: %q", goos, b)
		}
		if b, _ := os.ReadFile(exe + ".previous"); string(b) != "OLD" {
			t.Fatalf("%s: previous not kept", goos)
		}
		if strings.Join(e.calls[0], "|") != want {
			t.Fatalf("%s: %v", goos, e.calls[0])
		}
	}
}

func TestHelperUnixRollsBackWhenUnhealthy(t *testing.T) {
	p, exe, dir := unixPlan(t, "linux")
	e := &execLog{}
	err := RunPlan(context.Background(), p, helperEnv(e, func(string) (string, error) { return "1.4.0", nil }))
	if err == nil || !strings.Contains(err.Error(), "previous build is back") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatalf("the old binary must be back: %q", b)
	}
	if len(e.calls) != 2 { // restart onto the new build, restart onto the old one
		t.Fatalf("%v", e.calls)
	}
	// An unhealthy build is marked bad: the automatic path never tries it again.
	f, _ := ReadFailures(dir)
	if ok, _ := AutoAllowed(f, "1.5.0", time.Now().Add(1000*time.Hour)); ok {
		t.Fatal("a build that did not become healthy must not be tried again on its own")
	}
	if r, _ := ReadResult(dir); r.OK {
		t.Fatal("result must say failed")
	}
}

func TestHelperReleasesLock(t *testing.T) {
	p, _, dir := unixPlan(t, "linux")
	if _, err := TakeLock(dir, time.Now(), 1, func(int) bool { return true }); err != nil {
		t.Fatal(err)
	}
	_ = RunPlan(context.Background(), p, helperEnv(&execLog{}, func(string) (string, error) { return "1.5.0", nil }))
	if _, err := os.Stat(filepath.Join(dir, LockFile)); !os.IsNotExist(err) {
		t.Fatal("the helper releases the lock as its last step")
	}
}

func TestPlanJSONRoundTrip(t *testing.T) {
	b, _ := json.Marshal(Plan{Kind: "update", HealthTimeout: time.Minute})
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil || p.HealthTimeout != time.Minute {
		t.Fatalf("%+v %v", p, err)
	}
}

// Owner decision: an automatic update waits at most the limit; then it cancels the drain and retries at the next check.
func TestAutoUpdateCancelsDrainAfterTheLimit(t *testing.T) {
	h := newHarness(t, "linux", true, true)
	h.m.o.AutoDrainLimit = 20 * time.Millisecond
	h.d.jobs = []Job{{Repository: "a/b", RunID: 1, Job: "x"}}
	if _, err := h.m.AutoCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCancelled(t, h)
	st := h.m.Status(context.Background())
	if h.d.cancelCount() != 1 || st.State != StateIdle || !strings.Contains(st.Error, "tries again at the next check") {
		t.Fatalf("cancelled=%d %+v", h.d.cancelCount(), st)
	}
	if len(h.spawns) != 0 {
		t.Fatal("a job was running; nothing may install")
	}
	// The lock is free again, so the next check can start over.
	if _, err := h.m.AutoCheck(context.Background()); err != nil || len(h.d.begun) != 2 {
		t.Fatalf("%v begun=%v", err, h.d.begun)
	}
}

// Machines R5.33: the automatic wait is the machine's drain timeout setting.
func TestAutoDrainLimitIsTheMachineDrainTimeout(t *testing.T) {
	h := newHarness(t, "linux", true, true)
	ctx := context.Background()
	for _, c := range []struct {
		minutes int
		limit   time.Duration
		want    time.Duration
	}{
		{0, 0, 60 * time.Minute},
		{90, 0, 90 * time.Minute},
		{5, 0, 5 * time.Minute},
		{-1, 0, 60 * time.Minute},
		{90, 20 * time.Millisecond, 20 * time.Millisecond}, // an explicit option still wins
	} {
		h.d.minutes, h.m.o.AutoDrainLimit = c.minutes, c.limit
		if got := h.m.autoDrainLimit(ctx); got != c.want {
			t.Errorf("setting %d option %s: limit %s, want %s", c.minutes, c.limit, got, c.want)
		}
	}
}

func TestInstallKindPicksTheAsset(t *testing.T) {
	for kind, want := range map[string]string{
		KindMSI: "crsp-windows-amd64.msi", KindPKG: "crsp-darwin-amd64.pkg", KindDEB: "crsp-linux-amd64.deb",
		KindRPM: "crsp-linux-amd64.rpm",
	} {
		goos := map[string]string{KindMSI: "windows", KindPKG: "darwin"}[kind]
		if got := AssetFor(kind, goos, "amd64"); got != want {
			t.Errorf("%s: %s", kind, got)
		}
	}
	if AssetFor(KindTarball, "darwin", "arm64") != "crsp-darwin-arm64.tar.gz" {
		t.Error("tarball")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "crsp")
	if InstallKindFor(exe, "linux") != KindTarball || InstallKindFor(exe, "windows") != KindMSI {
		t.Error("no marker: tarball, MSI on Windows")
	}
	_ = os.WriteFile(filepath.Join(dir, "install-kind"), []byte("deb"), 0o600)
	if InstallKindFor(exe, "linux") != KindDEB {
		t.Error("marker")
	}
}

func TestHelperNativePackages(t *testing.T) {
	for kind, want := range map[string]string{
		KindPKG: "installer|-pkg|f|-target|/", KindDEB: "apt-get|install|-y|./f", KindRPM: "rpm|-U|f",
	} {
		dir := t.TempDir()
		e := &execLog{}
		goos := "linux"
		if kind == KindPKG {
			goos = "darwin"
		}
		p := Plan{Kind: "update", GOOS: goos, InstallKind: kind, From: "1.4.0", To: "1.5.0", File: "f", Service: "crsp", Dir: dir}
		if err := RunPlan(context.Background(), p, helperEnv(e, func(string) (string, error) { return "1.5.0", nil })); err != nil {
			t.Fatal(err)
		}
		if strings.Join(e.calls[0], "|") != want {
			t.Errorf("%s: %v", kind, e.calls[0])
		}
	}
	// apt-get fails: dpkg -i is tried.
	e := &execLog{codes: map[string]int{"apt-get": 100}}
	p := Plan{Kind: "update", GOOS: "linux", InstallKind: KindDEB, From: "1.4.0", To: "1.5.0", File: "f", Service: "crsp", Dir: t.TempDir()}
	_ = RunPlan(context.Background(), p, helperEnv(e, func(string) (string, error) { return "1.5.0", nil }))
	if len(e.calls) < 2 || e.calls[1][0] != "dpkg" {
		t.Errorf("%v", e.calls)
	}
}

// ------------------------------------------------------- follow the controller

func TestFollowUpdatesToTheControllersVersionAndDrains(t *testing.T) {
	h := newHarness(t, "linux", false, true) // auto-update off: follow does not depend on the setting
	h.m.o.Privileged = func() bool { return true }
	ctx := context.Background()
	if _, err := h.m.Follow(ctx, "1.4.0"); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("a build that is not older does not follow: %v", err)
	}
	if _, err := h.m.Follow(ctx, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if len(h.dl) != 2 || len(h.d.begun) != 1 {
		t.Fatalf("download, verify and drain first: dl=%v begun=%v", h.dl, h.d.begun)
	}
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil {
		t.Fatal(err)
	}
	if len(h.spawns) != 1 || h.spawns[0].To != "1.5.0" {
		t.Fatalf("%+v", h.spawns)
	}
}

func TestFollowNeedsPrivilegeAndDownloadsNothingWithout(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	h.m.o.Privileged = func() bool { return false }
	if _, err := h.m.Follow(context.Background(), "1.5.0"); !errors.Is(err, ErrUnprivileged) {
		t.Fatalf("%v", err)
	}
	if len(h.dl) != 0 || len(h.d.begun) != 0 {
		t.Fatal("nothing may be downloaded or drained")
	}
}

func TestFollowPostponesAfterTheDrainLimitAndRetries(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	h.m.o.Privileged = func() bool { return true }
	h.m.o.AutoDrainLimit = 20 * time.Millisecond
	h.d.jobs = []Job{{Repository: "a/b", RunID: 1, Job: "x"}}
	if _, err := h.m.Follow(context.Background(), "1.5.0"); err != nil {
		t.Fatal(err)
	}
	waitCancelled(t, h)
	st := h.m.Status(context.Background())
	if h.d.cancelCount() != 1 || st.State != StateIdle || len(h.spawns) != 0 {
		t.Fatalf("a running job is never cut: cancelled=%d %+v", h.d.cancelCount(), st)
	}
	if _, err := h.m.Follow(context.Background(), "1.5.0"); err != nil || len(h.d.begun) != 2 {
		t.Fatalf("the next ask tries again: %v %v", err, h.d.begun)
	}
	// The second ask armed the drain limit again: end it here, so its timer does not write into the
	// test's folder while the folder is being removed.
	if _, err := h.m.Cancel(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
}

func TestFollowStopsAfterThreeFailedTriesOfAVersion(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	h.m.o.Privileged = func() bool { return true }
	now := time.Now()
	h.m.o.Now = func() time.Time { return now }
	_ = os.MkdirAll(h.m.o.Dir, 0o750)
	for i := 0; i < AutoTries; i++ {
		_ = RecordFailure(h.m.o.Dir, "1.5.0", now, false)
	}
	if _, err := h.m.Follow(context.Background(), "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if len(h.dl) != 0 || len(h.d.begun) != 0 {
		t.Fatal("after three failed tries the version is left alone")
	}
	if st := h.m.Status(context.Background()); !strings.Contains(st.Error, "gave up") {
		t.Fatalf("%+v", st)
	}
}

func TestPinnedSourceAsksForTheTag(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"tag_name":"v1.5.0","html_url":"u","assets":[]}`))
	}))
	defer srv.Close()
	p := &PinnedSource{Base: GitHubSource{Base: srv.URL}}
	if _, err := p.Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("nothing pinned: %v", err)
	}
	p.Pin("1.5.0")
	rel, err := p.Latest(context.Background())
	if err != nil || rel.Version != "1.5.0" || path != "/repos/"+Repo+"/releases/tags/v1.5.0" {
		t.Fatalf("%+v %v %s", rel, err, path)
	}
}

func TestHelperWithoutHealthURLWaitsForTheServiceToBeActive(t *testing.T) {
	p, _, _ := unixPlan(t, "linux")
	p.HealthURL = "" // an agent has no console port
	e := &execLog{}
	if err := RunPlan(context.Background(), p, helperEnv(e, nil)); err != nil {
		t.Fatal(err)
	}
	last := strings.Join(e.calls[len(e.calls)-1], "|")
	if last != "systemctl|is-active|--quiet|crsp" {
		t.Fatalf("%s", last)
	}
	// A service that never becomes active rolls the binary back.
	p2, exe, _ := unixPlan(t, "linux")
	p2.HealthURL, p2.HealthTimeout = "", time.Second
	e = &execLog{codes: map[string]int{"systemctl": 0}}
	env := helperEnv(e, nil)
	env.Exec = func(_ context.Context, name string, args ...string) (int, error) {
		e.calls = append(e.calls, append([]string{name}, args...))
		if len(args) > 0 && args[0] == "is-active" {
			return 3, nil
		}
		return 0, nil
	}
	if err := RunPlan(context.Background(), p2, env); err == nil {
		t.Fatal("must fail")
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatalf("rolled back: %q", b)
	}
}

// D38: the health read works on plain HTTP (loopback) and on the console's TLS
// (every other address), whatever certificate the controller's CA made.
func TestHTTPHealthReadsPlainAndTLS(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","version":"1.2.3"}`)
	})
	plain := httptest.NewServer(h)
	defer plain.Close()
	secure := httptest.NewTLSServer(h)
	defer secure.Close()
	for _, u := range []string{plain.URL, secure.URL} {
		v, err := httpHealth(context.Background(), u+"/healthz")
		if err != nil || v != "1.2.3" {
			t.Errorf("%s: %q %v", u, v, err)
		}
	}
}

// CRSP147: a manual check only checks. Auto-update on, a newer release, jobs or none: nothing is
// downloaded, drained or installed; Apply after a yes is what installs; the scheduled look still does.
func TestCheckNeverInstallsEvenWithAutoOn(t *testing.T) {
	h := newHarness(t, "linux", true, true)
	st, err := h.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "1.5.0" || st.State != StateIdle {
		t.Fatalf("%+v", st)
	}
	if len(h.dl) != 0 || len(h.d.begun) != 0 || len(h.spawns) != 0 {
		t.Fatalf("a manual check installed: dl=%v begun=%v", h.dl, h.d.begun)
	}
}

func TestInstallNowInstallsAndLaterDoesNot(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	if _, err := h.m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	// Later: the tray calls nothing, so nothing changes.
	if len(h.d.begun) != 0 {
		t.Fatal("Later must change nothing")
	}
	st, err := h.m.Apply(ctx, KindUpdate, true, "tray")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.d.begun) != 1 || h.d.begun[0] != KindUpdate || st.State == StateIdle {
		t.Fatalf("Install now must start the update: %+v begun=%v", st, h.d.begun)
	}
}

// CRSP147: after an update the service started under, AfterInstall runs once with the new version; a
// failed update, or no update, does not call it.
func TestAfterInstallRunsOnlyAfterASuccessfulMSIUpdate(t *testing.T) {
	now := time.Now()
	run := func(current, exit string) (got chan string) {
		dir := t.TempDir()
		log := filepath.Join(dir, "msi-update.log")
		_ = os.WriteFile(log, []byte("MainEngineThread is returning "+exit+"\n"), 0o600)
		pendingFor(t, dir, log, now)
		got = make(chan string, 2)
		New(Options{Current: current, Dir: dir, Now: func() time.Time { return now }, AfterInstall: func(v string) { got <- v }})
		return got
	}
	select {
	case v := <-run("1.5.0", "0"):
		if v != "1.5.0" {
			t.Fatalf("%q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the tray must be started after a successful update")
	}
	select {
	case v := <-run("1.4.0", "1603"):
		t.Fatalf("a failed update must not start a tray: %q", v)
	case <-time.After(150 * time.Millisecond):
	}
}

// waitCancelled waits, bounded, until the update has given up its drain.
func waitCancelled(t *testing.T, h *harness) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	limit := time.NewTimer(5 * time.Second)
	defer limit.Stop()
	for h.d.cancelCount() == 0 {
		select {
		case <-tick.C:
		case <-limit.C:
			t.Fatal("the drain was never cancelled")
		}
	}
	// the status settles right after the cancel
	for h.m.Status(context.Background()).State != StateIdle {
		select {
		case <-tick.C:
		case <-limit.C:
			t.Fatal("the update never went idle")
		}
	}
}
