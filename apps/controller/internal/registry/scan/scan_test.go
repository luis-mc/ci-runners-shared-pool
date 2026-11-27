package scan

import (
	"archive/tar"
	"bytes"
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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

const (
	digA      = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sbomDoc   = `{"spdxVersion":"SPDX-2.3","packages":[{"name":"a"},{"name":"b"},{"name":"c"}]}`
	reportDoc = `{"SchemaVersion":2,"Results":[{"Vulnerabilities":[{"Severity":"CRITICAL"},{"Severity":"HIGH"},{"Severity":"HIGH"},{"Severity":"LOW"},{"Severity":"UNKNOWN"}]},{"Vulnerabilities":[{"Severity":"MEDIUM"}]},{}]}`
)

// fakeQueries is an in-memory Queries.
type fakeQueries struct {
	mu     sync.Mutex
	scans  map[string]*sqlcgen.RegistryScan
	images map[string]*sqlcgen.RegistryImage
	order  []string
}

func newFake(images ...sqlcgen.RegistryImage) *fakeQueries {
	f := &fakeQueries{scans: map[string]*sqlcgen.RegistryScan{}, images: map[string]*sqlcgen.RegistryImage{}}
	for i := range images {
		im := images[i]
		f.images[im.ID] = &im
	}
	return f
}

func (f *fakeQueries) QueueRegistryScan(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.scans[id]; !ok {
		f.scans[id] = &sqlcgen.RegistryScan{ImageID: id, State: "queued"}
		f.order = append(f.order, id)
	}
	return nil
}

func (f *fakeQueries) ListQueuedRegistryScans(context.Context) ([]sqlcgen.ListQueuedRegistryScansRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlcgen.ListQueuedRegistryScansRow
	for _, id := range f.order {
		if f.scans[id].State == "queued" {
			out = append(out, sqlcgen.ListQueuedRegistryScansRow{ImageID: id, PushedBy: f.images[id].PushedBy})
		}
	}
	return out, nil
}

func (f *fakeQueries) ClaimRegistryScan(_ context.Context, a sqlcgen.ClaimRegistryScanParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.scans[a.ImageID]
	if s == nil || s.State != "queued" {
		return 0, nil
	}
	s.State, s.MachineID, s.ClaimedAt = "scanning", a.MachineID, a.ClaimedAt
	s.Attempts++
	return 1, nil
}

func (f *fakeQueries) FinishRegistryScan(_ context.Context, a sqlcgen.FinishRegistryScanParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.scans[a.ImageID]
	s.State, s.Packages, s.Critical, s.High, s.Medium, s.Low = a.State, a.Packages, a.Critical, a.High, a.Medium, a.Low
	s.SbomDigest, s.ReportDigest, s.Error, s.FinishedAt = a.SbomDigest, a.ReportDigest, a.Error, a.FinishedAt
	return nil
}

func (f *fakeQueries) GetRegistryScan(_ context.Context, id string) (sqlcgen.RegistryScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.scans[id]; s != nil {
		return *s, nil
	}
	return sqlcgen.RegistryScan{}, errNoRows
}

func (f *fakeQueries) SetRegistryImageScanState(_ context.Context, a sqlcgen.SetRegistryImageScanStateParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[a.ID].ScanState = a.ScanState
	return nil
}

func (f *fakeQueries) GetRegistryImage(_ context.Context, id string) (sqlcgen.RegistryImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if im := f.images[id]; im != nil {
		return *im, nil
	}
	return sqlcgen.RegistryImage{}, errNoRows
}

var errNoRows = errors.New("no rows")

type fakeReferrers struct {
	puts []string
	fail error
}

func (r *fakeReferrers) Put(_ context.Context, name, subject, typ string, body io.Reader) (string, error) {
	if r.fail != nil {
		return "", r.fail
	}
	b, _ := io.ReadAll(body)
	r.puts = append(r.puts, name+"|"+subject+"|"+typ+"|"+string(b))
	return digestOf(b), nil
}

func image(id, pusher string) sqlcgen.RegistryImage {
	return sqlcgen.RegistryImage{ID: id, Name: "crsp/std/base", ManifestDigest: digA, PushedBy: pusher, ScanState: "not_scanned"}
}

func okResult(id, machine string) registry.ScanResult {
	return registry.ScanResult{
		ImageID: id, MachineID: machine, Packages: 3, Critical: 1, High: 2, Medium: 1, Low: 1,
		SBOM: strings.NewReader(sbomDoc), Report: strings.NewReader(reportDoc),
	}
}

func TestQueueClaimRecord(t *testing.T) { // R5.59, R5.60
	ctx := context.Background()
	q := newFake(image("img_1", "m1"))
	refs := &fakeReferrers{}
	s := New(q, refs, func() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) })

	if err := s.Hook()(ctx, registry.Image{ID: "img_1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, "img_1"); err != nil { // twice keeps one row
		t.Fatal(err)
	}
	if q.images["img_1"].ScanState != "queued" || len(q.scans) != 1 {
		t.Fatalf("after queue: %v %d", q.images["img_1"].ScanState, len(q.scans))
	}
	id, ok, err := s.Claim(ctx, "m1")
	if err != nil || !ok || id != "img_1" {
		t.Fatalf("claim: %q %v %v", id, ok, err)
	}
	if q.images["img_1"].ScanState != "scanning" || q.scans["img_1"].ClaimedAt != "2026-11-19T10:00:00.000Z" {
		t.Fatal("claim did not mark the row")
	}
	if _, ok, _ := s.Claim(ctx, "m2"); ok {
		t.Fatal("a claimed scan was offered again")
	}
	if err := s.Record(ctx, okResult("img_1", "m1")); err != nil {
		t.Fatal(err)
	}
	row := q.scans["img_1"]
	if row.State != "done" || row.Packages != 3 || row.Critical != 1 || row.High != 2 || row.Medium != 1 || row.Low != 1 {
		t.Fatalf("row: %+v", row)
	}
	if len(refs.puts) != 2 || !strings.Contains(refs.puts[0], "crsp/std/base|"+digA+"|"+SBOMArtifactType+"|") ||
		!strings.Contains(refs.puts[1], ReportArtifactType) {
		t.Fatalf("referrers: %v", refs.puts)
	}
	if row.SbomDigest == "" || row.ReportDigest == "" || q.images["img_1"].ScanState != "done" {
		t.Fatal("digests or image state missing")
	}
	if err := s.Record(ctx, okResult("img_1", "m1")); err == nil {
		t.Fatal("recorded a finished scan twice")
	}
}

func TestClaimOffersThePusherFirst(t *testing.T) { // R5.59
	ctx := context.Background()
	q := newFake(image("img_a", "m1"), image("img_b", "m2"), image("img_c", "m1"))
	s := New(q, &fakeReferrers{}, nil)
	for _, id := range []string{"img_a", "img_b", "img_c"} {
		if err := s.Queue(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, m := range []string{"m2", "m1", "m1", "m1"} {
		id, ok, err := s.Claim(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			got = append(got, id)
		}
	}
	if strings.Join(got, ",") != "img_b,img_a,img_c" {
		t.Fatalf("order %v", got)
	}
}

func TestFailedScanNeverBlocks(t *testing.T) { // R5.61
	ctx := context.Background()
	for name, tc := range map[string]struct {
		res  func() registry.ScanResult
		refs *fakeReferrers
		want string
	}{
		"tool error": {func() registry.ScanResult {
			return registry.ScanResult{ImageID: "img_1", MachineID: "m1", Error: "trivy: exit 1"}
		}, &fakeReferrers{}, "trivy: exit 1"},
		"referrer error": {func() registry.ScanResult { return okResult("img_1", "m1") },
			&fakeReferrers{fail: errors.New("registry read-only")}, "registry read-only"},
		"no report": {func() registry.ScanResult {
			r := okResult("img_1", "m1")
			r.Report = nil
			return r
		}, &fakeReferrers{}, "no SBOM or report"},
	} {
		t.Run(name, func(t *testing.T) {
			q := newFake(image("img_1", "m1"))
			s := New(q, tc.refs, nil)
			_ = s.Queue(ctx, "img_1")
			_, _, _ = s.Claim(ctx, "m1")
			if err := s.Record(ctx, tc.res()); err != nil {
				t.Fatalf("a failed scan must be recorded, not returned: %v", err)
			}
			row := q.scans["img_1"]
			if row.State != "failed" || !strings.Contains(row.Error, tc.want) || row.Critical != 0 || row.SbomDigest != "" {
				t.Fatalf("row: %+v", row)
			}
			if q.images["img_1"].ScanState != "failed" {
				t.Fatal("image state is not failed")
			}
		})
	}
}

func TestRecordRefusesWrongMachineOrState(t *testing.T) {
	ctx := context.Background()
	q := newFake(image("img_1", "m1"))
	s := New(q, &fakeReferrers{}, nil)
	_ = s.Queue(ctx, "img_1")
	if err := s.Record(ctx, okResult("img_1", "m1")); err == nil {
		t.Fatal("recorded a queued scan")
	}
	_, _, _ = s.Claim(ctx, "m1")
	if err := s.Record(ctx, okResult("img_1", "m9")); err == nil {
		t.Fatal("recorded another machine's scan")
	}
	if err := s.Record(ctx, okResult("img_x", "m1")); err == nil {
		t.Fatal("recorded an unknown scan")
	}
}

func TestCounts(t *testing.T) {
	n, err := CountPackages([]byte(sbomDoc))
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := CountPackages([]byte(`{"x":1}`)); err == nil {
		t.Fatal("not SPDX accepted")
	}
	c, h, m, l, err := CountSeverities([]byte(reportDoc))
	if err != nil || c != 1 || h != 2 || m != 1 || l != 1 {
		t.Fatalf("%d %d %d %d %v", c, h, m, l, err)
	}
	if _, _, _, _, err := CountSeverities([]byte(`[]`)); err == nil {
		t.Fatal("bad report accepted")
	}
}

// fakeTool writes a shell script standing in for syft or trivy. It records its
// arguments next to itself and writes the output file the real tool would.
func fakeTool(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake scanner binaries are shell scripts")
	}
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\necho \"$@\" > \"" + p + ".args\"\n" + body
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunnerWithFakeBinaries(t *testing.T) { // R5.59
	dir := t.TempDir()
	syft := fakeTool(t, dir, "syft", `for a in "$@"; do case "$a" in spdx-json=*) printf '%s' '`+sbomDoc+`' > "${a#spdx-json=}";; esac; done`+"\n")
	trivy := fakeTool(t, dir, "trivy", `while [ $# -gt 0 ]; do [ "$1" = --output ] && printf '%s' '`+reportDoc+`' > "$2"; shift; done`+"\n")
	ref := "ctl:8443/crsp/std/base@" + digA
	res, err := Runner{Syft: syft, Trivy: trivy}.Scan(context.Background(), "img_1", "m1", ref)
	if err != nil || res.Error != "" {
		t.Fatalf("%v %q", err, res.Error)
	}
	if res.Packages != 3 || res.Critical != 1 || res.High != 2 || res.Medium != 1 || res.Low != 1 {
		t.Fatalf("%+v", res)
	}
	sb, _ := io.ReadAll(res.SBOM)
	if string(sb) != sbomDoc {
		t.Fatalf("sbom %q", sb)
	}
	for tool, want := range map[string]string{syft: "registry:" + ref, trivy: ref} {
		args, _ := os.ReadFile(tool + ".args")
		if !strings.Contains(string(args), want) {
			t.Errorf("%s args %q lack %q", tool, args, want)
		}
	}
	// Nothing in the image runs: the tools get a reference, never a container command.
	if args, _ := os.ReadFile(trivy + ".args"); strings.Contains(string(args), "docker") {
		t.Errorf("trivy args %q", args)
	}
}

func TestRunnerReportsToolFailureInTheResult(t *testing.T) { // R5.61
	dir := t.TempDir()
	good := fakeTool(t, dir, "syft", `for a in "$@"; do case "$a" in spdx-json=*) printf '%s' '`+sbomDoc+`' > "${a#spdx-json=}";; esac; done`+"\n")
	bad := fakeTool(t, dir, "trivy", "echo 'db download failed' >&2\nexit 3\n")
	res, err := Runner{Syft: good, Trivy: bad}.Scan(context.Background(), "img_1", "m1", "h/n@"+digA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Error, "trivy: ") || !strings.Contains(res.Error, "db download failed") || res.SBOM != nil {
		t.Fatalf("%+v", res)
	}
	if _, err := (Runner{Syft: good, Trivy: bad}).Scan(context.Background(), "i", "m", "h/n:latest"); err == nil {
		t.Fatal("a tag reference was accepted")
	}
	garbage := fakeTool(t, dir, "syft2", `for a in "$@"; do case "$a" in spdx-json=*) echo nope > "${a#spdx-json=}";; esac; done`+"\n")
	res, _ = Runner{Syft: garbage, Trivy: bad}.Scan(context.Background(), "i", "m", "h/n@"+digA)
	if res.Error == "" {
		t.Fatal("failure not reported")
	}
}

func TestRunnerTimeout(t *testing.T) {
	dir := t.TempDir()
	slow := fakeTool(t, dir, "syft", "exec sleep 30\n")
	res, _ := Runner{Syft: slow, Trivy: slow, Timeout: 200 * time.Millisecond}.Scan(context.Background(), "i", "m", "h/n@"+digA)
	if !strings.Contains(res.Error, "timed out") {
		t.Fatalf("%q", res.Error)
	}
}

func TestRef(t *testing.T) {
	got := Ref("ctl:8443/", registry.Image{Name: "crsp/std/base", Manifest: digA})
	if got != "ctl:8443/crsp/std/base@"+digA {
		t.Fatal(got)
	}
}

// ---- pinned, sha256-checked tools (R5.62)

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestFetchChecksAndCaches(t *testing.T) { // R5.62
	archive := tarGz(t, "trivy", "#!/bin/sh\n")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	f := Fetcher{Dir: t.TempDir(), Attempts: 2, Wait: time.Millisecond}
	pin := Pin{URL: srv.URL + "/trivy_1_Linux-64bit.tar.gz", SHA256: hexSum(archive), Kind: "archive", BinDir: ".", Bin: "trivy"}
	bin, err := f.Fetch(context.Background(), "trivy", "1", pin)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(bin); err != nil || st.Mode()&0o100 == 0 && runtime.GOOS != "windows" {
		t.Fatalf("binary not usable: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "trivy", "1", pin); err != nil || hits != 1 {
		t.Fatalf("second fetch hit the network: %d %v", hits, err)
	}

	pin.SHA256 = strings.Repeat("0", 64)
	f2 := Fetcher{Dir: t.TempDir(), Attempts: 2, Wait: time.Millisecond}
	if _, err := f2.Fetch(context.Background(), "trivy", "1", pin); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a wrong sha256 was accepted: %v", err)
	}
	var left []string
	_ = filepath.Walk(f2.Dir, func(p string, i os.FileInfo, _ error) error {
		if i != nil && !i.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Fatalf("a rejected download left %v", left)
	}
}

func TestFetchRefusesUnsafeArchive(t *testing.T) {
	archive := tarGz(t, "../evil", "x")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	f := Fetcher{Dir: t.TempDir(), Attempts: 1}
	_, err := f.Fetch(context.Background(), "t", "1", Pin{URL: srv.URL + "/t.tar.gz", SHA256: hexSum(archive), Kind: "archive", BinDir: ".", Bin: "t"})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("%v", err)
	}
}

func TestLoadPin(t *testing.T) {
	js := `{"syft":{"version":"1.33.0","platforms":{"linux_x64":{"kind":"archive","url":"https://x/syft.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","bin_dir":".","bin":"syft"}}}}`
	v, p, err := LoadPin([]byte(js), "syft", "linux_x64")
	if err != nil || v != "1.33.0" || p.Bin != "syft" {
		t.Fatalf("%v %v %+v", v, err, p)
	}
	for _, c := range [][2]string{{"trivy", "linux_x64"}, {"syft", "linux_arm64"}} {
		if _, _, err := LoadPin([]byte(js), c[0], c[1]); err == nil {
			t.Fatalf("%v accepted", c)
		}
	}
}

// The repository's own pin file pins syft for both Linux platforms with a real sha256.
func TestRepositoryPinsSyft(t *testing.T) { // R5.62
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "scripts", "delivery", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plat := range []string{"linux_x64", "linux_arm64"} {
		if _, _, err := LoadPin(b, "syft", plat); err != nil {
			t.Error(err)
		}
	}
}

// ---- referrers over the registry API

func TestRegistryClientPutsAReferrer(t *testing.T) { // R5.60
	blobs := map[string][]byte{}
	var manifest []byte
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crsp/std/base/blobs/uploads/", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			w.Header().Set("Location", "/v2/crsp/std/base/blobs/uploads/u1")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if r.URL.Query().Get("digest") != digestOf(b) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		blobs[r.URL.Query().Get("digest")] = b
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v2/crsp/std/base/manifests/", func(w http.ResponseWriter, r *http.Request) {
		manifest, _ = io.ReadAll(r.Body)
		if strings.TrimPrefix(r.URL.Path, "/v2/crsp/std/base/manifests/") != digestOf(manifest) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &RegistryClient{Base: srv.URL, Token: func(context.Context) (string, error) { return "tok", nil }}
	dig, err := c.Put(context.Background(), "crsp/std/base", digA, SBOMArtifactType, strings.NewReader(sbomDoc))
	if err != nil {
		t.Fatal(err)
	}
	if dig != digestOf(manifest) || auth != "Bearer tok" {
		t.Fatalf("%s %q", dig, auth)
	}
	var m struct {
		ArtifactType string
		Subject      struct{ Digest string }
		Layers       []struct{ Digest string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ArtifactType != SBOMArtifactType || m.Subject.Digest != digA || len(m.Layers) != 1 ||
		string(blobs[m.Layers[0].Digest]) != sbomDoc {
		t.Fatalf("manifest %s", manifest)
	}

	// A refusing registry (read-only window) is an error the Scanner turns into a failed scan.
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer deny.Close()
	if _, err := (&RegistryClient{Base: deny.URL}).Put(context.Background(), "n", digA, "t", strings.NewReader("x")); err == nil {
		t.Fatal("a refusal was ignored")
	}
}
