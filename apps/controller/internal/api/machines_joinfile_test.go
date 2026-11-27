package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/packages"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

type relSource struct{ rel update.Release }

func (r relSource) Latest(context.Context) (update.Release, error) { return r.rel, nil }

func hex256(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }

// joinFileMux serves a controller whose release holds a deb, an msi and a pkg.
func joinFileMux(t *testing.T, role auth.Role, withPackages bool) (*http.ServeMux, *machines.Service, *machines.MemRepo) {
	t.Helper()
	assets := map[string]string{"crsp-linux-amd64.deb": "DEB-BYTES", "crsp-windows-amd64.msi": "MSI-BYTES", "crsp-darwin-arm64.pkg": "PKG-BYTES"}
	sums := ""
	urls := map[string]string{"SHA256SUMS": "https://x.test/sums"}
	body := map[string]string{}
	for n, b := range assets {
		sums += hex256(b) + "  " + n + "\n"
		urls[n] = "https://x.test/" + n
		body["https://x.test/"+n] = b
	}
	body["https://x.test/sums"] = sums
	store := &packages.Store{
		Source: relSource{update.Release{Version: "1.2.3", Assets: urls}}, Dir: t.TempDir(), AllowURL: func(string) bool { return true },
		Fetch: func(_ context.Context, url, dst string) error { return os.WriteFile(dst, []byte(body[url]), 0o600) },
	}
	repo := machines.NewMemRepo()
	o := machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: strings.Repeat("ab", 32)}
	opts := Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CA: fakeCA{}}
	if withPackages {
		o.Packages, o.ConsoleURL = store, "https://pool.lan:8460"
		opts.Packages = store
	}
	svc := machines.New(o)
	opts.Machines = svc
	if role != "" {
		opts.Authn = asRole(role)
	}
	mux := http.NewServeMux()
	Register(mux, opts)
	return mux, svc, repo
}

var fileCode = regexp.MustCompile(`JOIN_CODE='([^']+)'|\$JoinCode = '([^']+)'`)

func codeIn(t *testing.T, body string) string {
	t.Helper()
	m := fileCode.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no join code in the file")
	}
	return m[1] + m[2]
}

func TestJoinFileNeedsAdmin(t *testing.T) { // R5.65
	mux, _, _ := joinFileMux(t, auth.RoleViewer, true)
	if rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "linux"}); rec.Code != 403 {
		t.Fatalf("viewer: %d, want 403", rec.Code)
	}
	mux, _, _ = joinFileMux(t, "", true)
	if rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "linux"}); rec.Code != 401 {
		t.Fatalf("no session: %d, want 401", rec.Code)
	}
}

func TestJoinFilePerOS(t *testing.T) { // R5.65
	cases := []struct{ os, ext, eol string }{{"windows", "cmd", "\r\n"}, {"linux", "sh", "\n"}, {"macos", "command", "\n"}}
	for _, c := range cases {
		mux, _, _ := joinFileMux(t, auth.RoleAdmin, true)
		rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": c.os})
		if rec.Code != 201 {
			t.Fatalf("%s: %d %s", c.os, rec.Code, rec.Body)
		}
		cd := rec.Header().Get("Content-Disposition")
		if !regexp.MustCompile(`^attachment; filename="crsp-join-` + c.os + `-until-\d{8}T\d{4}Z\.` + c.ext + `"$`).MatchString(cd) {
			t.Errorf("%s: Content-Disposition %q", c.os, cd)
		}
		if !strings.HasPrefix(rec.Header().Get("X-Join-Code-Id"), "jc_") {
			t.Errorf("%s: X-Join-Code-Id %q", c.os, rec.Header().Get("X-Join-Code-Id"))
		}
		body := rec.Body.String()
		for _, want := range []string{"pool.lan:8461", "https://pool.lan:8460", strings.Repeat("ab", 32)} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: file lacks %q", c.os, want)
			}
		}
		if c.eol == "\r\n" && strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
			t.Errorf("%s: a line does not end in CRLF", c.os)
		}
		if c.eol == "\n" && strings.Contains(body, "\r") {
			t.Errorf("%s: CR in a shell file", c.os)
		}
	}
}

// R5.65, R5.4: the code of the file works once; the audit never holds it; the
// wizard polls the code by the id in the header.
func TestJoinFileCodeSingleUseAuditAndPolling(t *testing.T) {
	mux, svc, repo := joinFileMux(t, auth.RoleAdmin, true)
	rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "linux", "ttl_minutes": 15})
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	code, id := codeIn(t, rec.Body.String()), rec.Header().Get("X-Join-Code-Id")
	if got := doM(t, mux, "GET", "/api/v1/join-codes/"+id, nil); got.Code != 200 || !strings.Contains(got.Body.String(), `"progress":"waiting"`) || strings.Contains(got.Body.String(), code) {
		t.Fatalf("poll: %d %s", got.Code, got.Body)
	}
	facts := machines.Facts{HostName: "box", OS: "linux", Arch: "x64"}
	if _, err := svc.Join(context.Background(), machines.JoinInput{Code: code, Facts: facts}); err != nil {
		t.Fatalf("the code of the file did not join: %v", err)
	}
	if _, err := svc.Join(context.Background(), machines.JoinInput{Code: code, Facts: facts}); err == nil {
		t.Fatal("the code joined twice")
	}
	if got := doM(t, mux, "GET", "/api/v1/join-codes/"+id, nil); !strings.Contains(got.Body.String(), `"progress":"connected"`) {
		t.Fatalf("poll after join: %s", got.Body)
	}
	seen := false
	for _, a := range repo.AuditLog() {
		if strings.Contains(fmt.Sprintf("%+v", a), code) {
			t.Errorf("the audit log holds the code: %+v", a)
		}
		seen = seen || a.Action == "join_file.create" && a.Actor == "tester"
	}
	if !seen {
		t.Errorf("no join_file.create entry by the caller: %v", repo.AuditLog())
	}
}

func TestJoinFileExpiryAndBadInput(t *testing.T) {
	mux, _, _ := joinFileMux(t, auth.RoleAdmin, true)
	if rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "linux", "ttl_minutes": 1441}); rec.Code != 400 && rec.Code != 422 {
		t.Errorf("a code above 24 hours: %d", rec.Code)
	}
	if rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "plan9"}); rec.Code != 400 && rec.Code != 422 {
		t.Errorf("unknown OS: %d", rec.Code)
	}
	rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "macos", "ttl_minutes": 60})
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	id := rec.Header().Get("X-Join-Code-Id")
	got := doM(t, mux, "GET", "/api/v1/join-codes/"+id, nil).Body.String()
	if !strings.Contains(got, `"state":"waiting"`) {
		t.Errorf("%s", got)
	}
}

func TestJoinFileWithoutPackagesIsNotImplemented(t *testing.T) {
	mux, _, _ := joinFileMux(t, auth.RoleAdmin, false)
	rec := doM(t, mux, "POST", "/api/v1/join-files", map[string]any{"os": "linux"})
	if rec.Code != 501 || !strings.Contains(rec.Body.String(), "copy the command") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// R5.65: the package endpoint serves what SHA256SUMS lists, byte for byte.
func TestPackageEndpointServesWithSha256(t *testing.T) {
	mux, _, _ := joinFileMux(t, "", true)
	rec := doM(t, mux, "GET", "/api/v1/packages/crsp-linux-amd64.deb", nil)
	if rec.Code != 200 || rec.Body.String() != "DEB-BYTES" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	sums := doM(t, mux, "GET", "/api/v1/packages/SHA256SUMS", nil)
	if sums.Code != 200 || !strings.Contains(sums.Body.String(), hex256("DEB-BYTES")+"  crsp-linux-amd64.deb") {
		t.Fatalf("sums: %d %s", sums.Code, sums.Body)
	}
	if got := hex256(rec.Body.String()); !strings.Contains(sums.Body.String(), got) {
		t.Errorf("the served package does not match SHA256SUMS")
	}
	for _, name := range []string{"crsp-linux-arm64.deb", "etc-passwd", "..%2Fsecret"} {
		if r := doM(t, mux, "GET", "/api/v1/packages/"+name, nil); r.Code != 404 {
			t.Errorf("%s: %d, want 404", name, r.Code)
		}
	}
	nomux, _, _ := joinFileMux(t, "", false)
	if r := doM(t, nomux, "GET", "/api/v1/packages/crsp-linux-amd64.deb", nil); r.Code != 501 {
		t.Errorf("no store: %d, want 501", r.Code)
	}
}
