package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depstore"
)

func depStoreMux(t *testing.T, role auth.Role, body []byte, sha string) *http.ServeMux {
	t.Helper()
	st := &depstore.Store{
		Dir: filepath.Join(t.TempDir(), "dependencies"),
		Catalog: func(context.Context) []depstore.Spec {
			return []depstore.Spec{
				{ID: "docker-desktop-windows", Name: "Docker Desktop", Kind: depstore.KindInstaller, OS: "windows", Source: "desktop.docker.com", URL: "https://desktop.docker.com/x.exe", SHA256: sha},
				{ID: "unpinned-windows", Name: "Unpinned", Kind: depstore.KindInstaller, OS: "windows", Source: "h"},
			}
		},
		Fetch: func(_ context.Context, _, dst string) error { return os.WriteFile(dst, body, 0o600) },
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DepStore: &depstore.Service{Store: st}, Authn: asRole(role)})
	return mux
}

func depDo(mux *http.ServeMux, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func shaOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type storeList struct {
	Items []struct {
		ID, State  string
		Pinned     bool
		CanFetch   bool   `json:"can_fetch"`
		Origin     string `json:"origin"`
		StoredByte int64  `json:"stored_bytes"`
	}
}

func TestDependencyStoreListFetchImport(t *testing.T) {
	body := []byte("installer")
	mux := depStoreMux(t, auth.RoleAdmin, body, shaOf(body))
	var l storeList
	w := depDo(mux, http.MethodGet, "/api/v1/dependencies/store", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &l) != nil || len(l.Items) != 2 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if l.Items[0].ID != "docker-desktop-windows" || l.Items[0].State != "missing" || !l.Items[0].CanFetch || l.Items[1].State != "not_pinned" || l.Items[1].Pinned {
		t.Fatalf("list = %+v", l.Items)
	}
	if w := depDo(mux, http.MethodPost, "/api/v1/dependencies/store/docker-desktop-windows/fetch", nil); w.Code != 200 {
		t.Fatalf("fetch: %d %s", w.Code, w.Body.String())
	}
	if w := depDo(mux, http.MethodPost, "/api/v1/dependencies/store/unpinned-windows/fetch", nil); w.Code != http.StatusConflict {
		t.Fatalf("fetch unpinned: %d", w.Code)
	}
	if w := depDo(mux, http.MethodPost, "/api/v1/dependencies/store/nope/fetch", nil); w.Code != http.StatusNotFound {
		t.Fatalf("fetch unknown: %d", w.Code)
	}
}

func TestDependencyStoreImportChecksTheFile(t *testing.T) {
	body := []byte("carried over")
	mux := depStoreMux(t, auth.RoleAdmin, nil, shaOf(body))
	if w := depDo(mux, http.MethodPut, "/api/v1/dependencies/store/docker-desktop-windows/file", []byte("wrong")); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong file: %d %s", w.Code, w.Body.String())
	}
	w := depDo(mux, http.MethodPut, "/api/v1/dependencies/store/docker-desktop-windows/file", body)
	if w.Code != 200 {
		t.Fatalf("right file: %d %s", w.Code, w.Body.String())
	}
	var e struct{ State, Origin string }
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.State != "ready" || e.Origin != "imported" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestDependencyStoreWritesNeedAdmin(t *testing.T) {
	body := []byte("x")
	mux := depStoreMux(t, auth.RoleViewer, body, shaOf(body))
	if w := depDo(mux, http.MethodPost, "/api/v1/dependencies/store/docker-desktop-windows/fetch", nil); w.Code != http.StatusForbidden {
		t.Fatalf("fetch as viewer: %d", w.Code)
	}
	if w := depDo(mux, http.MethodPut, "/api/v1/dependencies/store/docker-desktop-windows/file", body); w.Code != http.StatusForbidden {
		t.Fatalf("import as viewer: %d", w.Code)
	}
}
