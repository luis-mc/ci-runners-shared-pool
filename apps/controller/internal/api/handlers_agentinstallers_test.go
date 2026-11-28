package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentinstallers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

const (
	aiLinux = "CrspAgent-1.2.3-linux-x64.tar.gz"
	aiDeb   = "CrspAgent-1.2.3-linux-arm64.deb"
	aiWin   = "CrspAgent-1.2.3-win-x64.msi"
)

func agentInstallerMux(t *testing.T, role auth.Role, withStore bool) *http.ServeMux {
	t.Helper()
	opts := Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CA: fakeCA{}}
	if withStore {
		opts.AgentInstallers = &agentinstallers.Store{Version: "1.2.3", Dir: t.TempDir()}
	}
	if role != "" {
		opts.Authn = asRole(role)
	}
	mux := http.NewServeMux()
	Register(mux, opts)
	return mux
}

func putFile(t *testing.T, mux *http.ServeMux, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/agent-installers/"+name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func aiList(t *testing.T, rec *httptest.ResponseRecorder) gen.AgentInstallers {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out gen.AgentInstallers
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// CRSP347, operations R5.132: SHA256SUMS first, then each file, checked against it.
func TestImportAgentInstallersEndToEnd(t *testing.T) {
	mux := agentInstallerMux(t, auth.RoleAdmin, true)
	empty := aiList(t, doM(t, mux, "GET", "/api/v1/agent-installers", nil))
	if empty.HaveSums || len(empty.Installers) != 0 || empty.Version != "1.2.3" {
		t.Fatalf("before = %+v", empty)
	}
	if rec := putFile(t, mux, aiLinux, "lin"); rec.Code != http.StatusConflict {
		t.Fatalf("a file before SHA256SUMS: %d %s", rec.Code, rec.Body)
	}
	sums := hex256("lin") + "  " + aiLinux + "\n" + hex256("deb") + "  " + aiDeb + "\n" + hex256("msi") + "  " + aiWin + "\n"
	if rec := putFile(t, mux, "SHA256SUMS", "nothing useful\n"); rec.Code != http.StatusNotFound {
		t.Fatalf("a SHA256SUMS without agent installers: %d %s", rec.Code, rec.Body)
	}
	after := aiList(t, putFile(t, mux, "SHA256SUMS", sums))
	if !after.HaveSums || len(after.Installers) != 3 || after.FromRelease {
		t.Fatalf("after sums = %+v", after)
	}
	for _, in := range after.Installers {
		if in.Stored {
			t.Fatalf("%s stored before its import", in.Name)
		}
	}
	// A wrong file is refused and kept nowhere; the right archive and the native packages are taken.
	if rec := putFile(t, mux, aiLinux, "tampered"); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong bytes: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{aiLinux: "lin", aiDeb: "deb"} {
		if rec := putFile(t, mux, name, body); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	got := aiList(t, doM(t, mux, "GET", "/api/v1/agent-installers", nil))
	stored := map[string]bool{}
	for _, in := range got.Installers {
		stored[in.Name] = in.Stored
		if in.Format == "" || in.Os == "" || in.Sha256 == "" {
			t.Fatalf("incomplete row %+v", in)
		}
	}
	if !stored[aiLinux] || !stored[aiDeb] || stored[aiWin] {
		t.Fatalf("stored = %v", stored)
	}
	// Not an installer name, not listed, too many bytes.
	if rec := putFile(t, mux, "Crsp-1.2.3-win-x64.msi", "x"); rec.Code != http.StatusNotFound {
		t.Fatalf("the Controller installer was taken: %d", rec.Code)
	}
	if rec := putFile(t, mux, "CrspAgent-1.2.3-macos-x64.tar.gz", "x"); rec.Code != http.StatusNotFound {
		t.Fatalf("an unlisted name was taken: %d", rec.Code)
	}
}

func TestImportAgentInstallersRoles(t *testing.T) {
	viewer := agentInstallerMux(t, auth.RoleViewer, true)
	if rec := doM(t, viewer, "GET", "/api/v1/agent-installers", nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer list: %d", rec.Code)
	}
	if rec := putFile(t, viewer, aiLinux, "lin"); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer import: %d", rec.Code)
	}
	anon := agentInstallerMux(t, "", true)
	if rec := doM(t, anon, "GET", "/api/v1/agent-installers", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session list: %d", rec.Code)
	}
	off := agentInstallerMux(t, auth.RoleAdmin, false)
	if rec := doM(t, off, "GET", "/api/v1/agent-installers", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("no store: %d", rec.Code)
	}
}

func TestLimitedBodyRefusesTooMuch(t *testing.T) {
	l := &limitedBody{r: bytes.NewReader([]byte("abcdef")), left: 4}
	if _, err := io.ReadAll(l); err == nil || !l.over {
		t.Fatalf("err = %v over = %v", err, l.over)
	}
	l = &limitedBody{r: bytes.NewReader([]byte("abcd")), left: 4}
	if b, err := io.ReadAll(l); err != nil || string(b) != "abcd" || l.over {
		t.Fatalf("exact size: %q %v", b, err)
	}
}
