package depstore

import (
	"bytes"
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
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func newStore(t *testing.T, specs []Spec, body []byte, fetchErr error) *Store {
	t.Helper()
	return &Store{
		Dir:     filepath.Join(t.TempDir(), "dependencies"),
		Catalog: func(context.Context) []Spec { return specs },
		Fetch: func(_ context.Context, _, dst string) error {
			if fetchErr != nil {
				return fetchErr
			}
			return os.WriteFile(dst, body, 0o600)
		},
	}
}

func TestFetchChecksAndKeeps(t *testing.T) {
	body := []byte("installer bytes")
	s := newStore(t, []Spec{{ID: "docker-desktop-windows", Name: "Docker Desktop", Kind: KindInstaller, URL: "https://desktop.docker.com/x.exe", SHA256: sum(body), Source: "desktop.docker.com"}}, body, nil)
	ctx := context.Background()
	if e := s.List(ctx)[0]; e.State != StateMissing {
		t.Fatalf("before: %s", e.State)
	}
	if err := s.FetchOne(ctx, "docker-desktop-windows"); err != nil {
		t.Fatal(err)
	}
	e, _ := s.Get(ctx, "docker-desktop-windows")
	if e.State != StateReady || e.Origin != "fetched" || e.Stored != int64(len(body)) {
		t.Fatalf("after: %+v", e)
	}
}

func TestFetchMismatchKeepsNothing(t *testing.T) {
	s := newStore(t, []Spec{{ID: "a", Name: "A", URL: "https://h/a", SHA256: sum([]byte("good"))}}, []byte("evil"), nil)
	ctx := context.Background()
	if err := s.FetchOne(ctx, "a"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "a")); err == nil {
		t.Fatal("a file that did not match was kept")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "a.part")); err == nil {
		t.Fatal("the part file stayed")
	}
	if e, _ := s.Get(ctx, "a"); e.State != StateFailed || e.Error == "" {
		t.Fatalf("state = %+v", e)
	}
}

func TestFetchFailureIsReported(t *testing.T) {
	s := newStore(t, []Spec{{ID: "a", Name: "A", Source: "h", URL: "https://h/a", SHA256: sum([]byte("x"))}}, nil, errors.New("no route to host"))
	err := s.FetchOne(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("err = %v", err)
	}
	if e, _ := s.Get(context.Background(), "a"); e.State != StateFailed {
		t.Fatalf("state = %s", e.State)
	}
}

func TestNotPinnedIsNeverStored(t *testing.T) {
	s := newStore(t, []Spec{{ID: "a", Name: "A", URL: "https://h/a"}}, []byte("x"), nil)
	ctx := context.Background()
	if e, _ := s.Get(ctx, "a"); e.State != StateNotPinned {
		t.Fatalf("state = %s", e.State)
	}
	if err := s.FetchOne(ctx, "a"); !errors.Is(err, ErrNotPinned) {
		t.Fatalf("fetch err = %v", err)
	}
	if err := s.Import(ctx, "a", strings.NewReader("x"), 0); !errors.Is(err, ErrNotPinned) {
		t.Fatalf("import err = %v", err)
	}
}

func TestFetchRefusesNonHTTPS(t *testing.T) {
	s := newStore(t, []Spec{{ID: "a", Name: "A", URL: "http://h/a", SHA256: sum([]byte("x"))}}, []byte("x"), nil)
	if err := s.FetchOne(context.Background(), "a"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}

func TestImportChecksSHA(t *testing.T) {
	body := []byte("carried over")
	s := newStore(t, []Spec{{ID: "a", Name: "A", SHA256: sum(body)}}, nil, nil)
	ctx := context.Background()
	if err := s.Import(ctx, "a", strings.NewReader("other"), 0); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong file: %v", err)
	}
	if e, _ := s.Get(ctx, "a"); e.State != StateMissing {
		t.Fatalf("after refused import: %s", e.State)
	}
	if err := s.Import(ctx, "a", bytes.NewReader(body), 0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "a"); e.State != StateReady || e.Origin != "imported" {
		t.Fatalf("after import: %+v", e)
	}
	if err := s.Import(ctx, "zzz", bytes.NewReader(body), 0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestImportLimit(t *testing.T) {
	body := []byte("0123456789")
	s := newStore(t, []Spec{{ID: "a", Name: "A", SHA256: sum(body)}}, nil, nil)
	if err := s.Import(context.Background(), "a", bytes.NewReader(body), 5); err == nil {
		t.Fatal("a file over the limit was kept")
	}
}

func TestTamperedFileIsDroppedOnRestart(t *testing.T) {
	body := []byte("installer")
	specs := []Spec{{ID: "a", Name: "A", URL: "https://h/a", SHA256: sum(body)}}
	s := newStore(t, specs, body, nil)
	ctx := context.Background()
	if err := s.FetchOne(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "a"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := &Store{Dir: s.Dir, Catalog: s.Catalog}
	e, _ := s2.Get(ctx, "a")
	if e.State == StateReady {
		t.Fatal("a tampered file counted as ready")
	}
	if _, _, err := s2.Open(ctx, "a"); err == nil {
		t.Fatal("a tampered file was opened for serving")
	}
}

func TestHandlerServesOnlyReadyFiles(t *testing.T) {
	body := []byte("served bytes")
	s := newStore(t, []Spec{{ID: "a", Name: "A", URL: "https://h/a", SHA256: sum(body)}}, body, nil)
	h := s.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := get(PathPrefix + "a"); w.Code != http.StatusNotFound {
		t.Fatalf("not stored yet: %d", w.Code)
	}
	if err := s.FetchOne(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	w := get(PathPrefix + "a")
	got, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("served: %d %q", w.Code, got)
	}
	for _, bad := range []string{PathPrefix + "../etc/passwd", PathPrefix + "unknown", PathPrefix + "A"} {
		if w := get(bad); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, PathPrefix+"a", nil))
	if w2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w2.Code)
	}
}

type fakeAgents struct {
	list    []AgentInstaller
	fetched []string
	imports []string
}

func (f *fakeAgents) List(context.Context) ([]AgentInstaller, error) { return f.list, nil }
func (f *fakeAgents) Fetch(_ context.Context, n string) error {
	f.fetched = append(f.fetched, n)
	return nil
}
func (f *fakeAgents) Import(_ context.Context, n string, _ io.Reader) error {
	f.imports = append(f.imports, n)
	return nil
}

func TestServiceListsAgentPackagesToo(t *testing.T) {
	st := newStore(t, []Spec{{ID: "docker-desktop-windows", Name: "Docker Desktop", SHA256: sum([]byte("x"))}}, nil, nil)
	fa := &fakeAgents{list: []AgentInstaller{
		{Name: "CrspAgent-1-win-x64.zip", SHA256: "aa", OS: "windows", Arch: "x64", Cached: true},
		{Name: "CrspAgent-1-linux-arm64.tar.gz", SHA256: "bb", OS: "linux", Arch: "arm64"},
	}}
	svc := &Service{Store: st, Agents: fa}
	ctx := context.Background()
	got := svc.List(ctx)
	if len(got) != 3 || got[1].ID != "agent-linux-arm64" || got[1].State != StateMissing || got[2].ID != "agent-windows-x64" || got[2].State != StateReady {
		t.Fatalf("list = %+v", got)
	}
	if err := svc.Fetch(ctx, "me", "agent-linux-arm64"); err != nil || len(fa.fetched) != 1 || fa.fetched[0] != "CrspAgent-1-linux-arm64.tar.gz" {
		t.Fatalf("fetch: %v %v", err, fa.fetched)
	}
	if err := svc.Import(ctx, "me", "agent-windows-x64", strings.NewReader("z"), 0); err != nil || len(fa.imports) != 1 {
		t.Fatalf("import: %v %v", err, fa.imports)
	}
	if _, err := svc.Get(ctx, "agent-macos-x64"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown agent: %v", err)
	}
}
