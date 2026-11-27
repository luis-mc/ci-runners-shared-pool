package agentinstallers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

type fakeSource struct {
	rel update.Release
	err error
}

func (f fakeSource) Latest(context.Context) (update.Release, error) { return f.rel, f.err }

func sum(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }

const (
	winName = "CrspAgent-1.2.3-win-x64.zip"
	linName = "CrspAgent-1.2.3-linux-arm64.tar.gz"
	badName = "CrspAgent-1.2.3-macos-x64.tar.gz"
)

func newStore(t *testing.T, calls *[]string) *Store {
	t.Helper()
	files := map[string]string{
		"https://example.test/SHA256SUMS": sum("win") + "  " + winName + "\n" + sum("lin") + "  " + linName + "\n" + sum("expected") + "  " + badName + "\n" +
			sum("msi") + "  Crsp-1.2.3-win-x64.msi\n",
		"https://example.test/win": "win", "https://example.test/lin": "lin", "https://example.test/bad": "tampered",
	}
	return &Store{
		Version: "1.2.3",
		Source: fakeSource{rel: update.Release{Version: "1.2.3", Assets: map[string]string{
			"SHA256SUMS": "https://example.test/SHA256SUMS", winName: "https://example.test/win",
			linName: "https://example.test/lin", badName: "https://example.test/bad"}}},
		Dir:      t.TempDir(),
		AllowURL: func(u string) bool { return strings.HasPrefix(u, "https://example.test/") },
		Fetch: func(_ context.Context, url, dst string) error {
			*calls = append(*calls, url)
			return os.WriteFile(dst, []byte(files[url]), 0o600)
		},
	}
}

func TestNameAndParse(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"windows", "amd64", "CrspAgent-1.2.3-win-x64.zip"},
		{"linux", "arm64", "CrspAgent-1.2.3-linux-arm64.tar.gz"},
		{"macos", "amd64", "CrspAgent-1.2.3-macos-x64.tar.gz"},
		{"darwin", "arm64", "CrspAgent-1.2.3-macos-arm64.tar.gz"},
	}
	for _, c := range cases {
		got, err := Name("v1.2.3", c.os, c.arch)
		if err != nil || got != c.want {
			t.Fatalf("Name(%s,%s) = %q, %v; want %q", c.os, c.arch, got, err, c.want)
		}
		in, ok := ParseName(got)
		if !ok || in.Version != "1.2.3" || in.Arch != c.arch {
			t.Fatalf("ParseName(%q) = %+v, %v", got, in, ok)
		}
	}
	for _, bad := range []string{"Crsp-1.2.3-win-x64.msi", "CrspAgent-1.2.3-win-x64.tar.gz", "CrspAgent-1.2.3-linux-x64.zip", "../CrspAgent-1.2.3-win-x64.zip", "CrspAgent-1.2.3-bsd-x64.zip"} {
		if _, ok := ParseName(bad); ok {
			t.Fatalf("ParseName(%q) accepted", bad)
		}
	}
	if _, err := Name("1", "plan9", "amd64"); err == nil {
		t.Fatal("unknown os accepted")
	}
}

func TestListShowsOnlyAgentInstallers(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	got, err := s.List(context.Background())
	if err != nil || len(got) != 3 {
		t.Fatalf("List = %+v, %v; want the 3 agent installers and not the controller MSI", got, err)
	}
	for _, in := range got {
		if in.Cached {
			t.Fatalf("%s cached before any download", in.Name)
		}
	}
}

func TestOpenFetchesChecksAndCaches(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	f, size, err := s.Open(context.Background(), winName)
	if err != nil || size != 3 {
		t.Fatalf("Open = %v, %d", err, size)
	}
	_ = f.Close()
	n := len(calls)
	if f, _, err = s.Open(context.Background(), winName); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if len(calls) != n { // nothing is fetched again: SHA256SUMS is kept for the process, the installer is cached
		t.Fatalf("the cached installer was fetched again: %v", calls)
	}
}

func TestOpenRefusesUnlistedAndMismatch(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	if _, _, err := s.Open(context.Background(), "CrspAgent-1.2.3-win-arm64.zip"); !errors.Is(err, ErrNotListed) {
		t.Fatalf("unlisted: %v", err)
	}
	if _, _, err := s.Open(context.Background(), "Crsp-1.2.3-win-x64.msi"); !errors.Is(err, ErrNotListed) {
		t.Fatalf("controller installer served as an agent installer: %v", err)
	}
	if _, _, err := s.Open(context.Background(), badName); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir(), badName)); err == nil {
		t.Fatal("a file that does not match was kept")
	}
	if _, err := os.Stat(filepath.Join(s.dir(), badName+".part")); err == nil {
		t.Fatal("a partial file was kept")
	}
}

func TestOfflineServesTheCache(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	done, err := s.Prefetch(context.Background())
	if len(done) != 2 || !errors.Is(err, ErrMismatch) {
		t.Fatalf("Prefetch = %v, %v; want the two good installers and the mismatch", done, err)
	}
	off := &Store{Version: "1.2.3", Dir: s.Dir, Source: fakeSource{err: errors.New("no network")}}
	f, _, err := off.Open(context.Background(), linName)
	if err != nil {
		t.Fatalf("offline Open: %v", err)
	}
	_ = f.Close()
	ref, err := off.For(context.Background(), "windows", "amd64", "https://192.168.1.5:8460")
	if err != nil || ref.URL != "https://192.168.1.5:8460/agent-installers/"+winName || ref.SHA256 != sum("win") || !ref.SelfSigned {
		t.Fatalf("offline For = %+v, %v", ref, err)
	}
	// without a console address the release is needed for an address
	if _, err := off.For(context.Background(), "windows", "amd64", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offline release ref: %v", err)
	}
}

func TestNothingCachedAndOffline(t *testing.T) {
	off := &Store{Version: "1.2.3", Dir: t.TempDir()}
	if _, err := off.List(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("List = %v", err)
	}
	if _, _, err := off.Open(context.Background(), winName); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open = %v", err)
	}
}

func TestManualImport(t *testing.T) {
	off := &Store{Version: "1.2.3", Dir: t.TempDir()}
	sums := sum("win") + "  " + winName + "\n"
	if err := off.ImportSums(strings.NewReader("abc  Crsp-1.2.3-win-x64.msi\n")); !errors.Is(err, ErrNotListed) {
		t.Fatalf("a SHA256SUMS without agent installers was accepted: %v", err)
	}
	if err := off.ImportSums(strings.NewReader(sums)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := off.Import(ctx, winName, strings.NewReader("other")); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong bytes: %v", err)
	}
	if err := off.Import(ctx, "CrspAgent-1.2.3-linux-x64.tar.gz", strings.NewReader("win")); !errors.Is(err, ErrNotListed) {
		t.Fatalf("unlisted import: %v", err)
	}
	if err := off.Import(ctx, winName, strings.NewReader("win")); err != nil {
		t.Fatal(err)
	}
	list, _ := off.List(ctx)
	if len(list) != 1 || !list[0].Cached {
		t.Fatalf("List = %+v", list)
	}
}

func TestHandler(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	h := s.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return rec
	}
	if rec := get(PathPrefix + winName); rec.Code != 200 || rec.Body.String() != "win" {
		t.Fatalf("installer: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(PathPrefix + SumsName); rec.Code != 200 || !strings.Contains(rec.Body.String(), winName) {
		t.Fatalf("sums: %d", rec.Code)
	}
	for path, want := range map[string]int{
		PathPrefix + "Crsp-1.2.3-win-x64.msi": 404, PathPrefix + "../x": 404, PathPrefix + "CrspAgent-1.2.3-win-arm64.zip": 404, PathPrefix + badName: 503,
	} {
		if rec := get(path); rec.Code != want {
			t.Fatalf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, PathPrefix+winName, io.NopCloser(strings.NewReader(""))))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}

func TestManualCommand(t *testing.T) {
	ref := Ref{Name: linName, URL: LANLink("https://10.0.0.2:8460/", linName), SHA256: sum("lin"), SelfSigned: true}
	got := ManualCommand(ref, "linux")
	for _, want := range []string{"https://10.0.0.2:8460/agent-installers/" + linName, sum("lin") + "  " + linName, "curl -fsSLk", "install.sh"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}
	rel := ManualCommand(Ref{Name: linName, URL: "https://github.com/x", SHA256: sum("lin")}, "linux")
	if strings.Contains(rel, "-fsSLk") {
		t.Fatal("the GitHub address skips the TLS check")
	}
	w := ManualCommand(Ref{Name: winName, URL: LANLink("https://10.0.0.2:8460", winName), SHA256: sum("win"), SelfSigned: true}, "windows")
	for _, want := range []string{"Get-FileHash", "Expand-Archive", "install.ps1", sum("win")} {
		if !strings.Contains(w, want) {
			t.Fatalf("%q lacks %q", w, want)
		}
	}
}

func TestOfflineDoesNotWaitOnTheNetworkForEveryCall(t *testing.T) {
	n := 0
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	s := &Store{Version: "1.2.3", Dir: t.TempDir(), Now: func() time.Time { return now },
		Source: srcFunc(func() (update.Release, error) { n++; return update.Release{}, errors.New("no network") })}
	for i := 0; i < 3; i++ {
		_, _ = s.List(context.Background())
	}
	if n != 1 {
		t.Fatalf("the release was asked for %d times in a minute", n)
	}
	now = now.Add(RetryAfter + time.Second)
	_, _ = s.List(context.Background())
	if n != 2 {
		t.Fatalf("the release was not asked for again after %v: %d", RetryAfter, n)
	}
}

type srcFunc func() (update.Release, error)

func (f srcFunc) Latest(context.Context) (update.Release, error) { return f() }
