package lancache_test

import (
	"crypto/sha1" //nolint:gosec // the npm shasum form is tested too
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// upstream is a fake npm registry, PyPI index and file host in one server.
type upstream struct {
	srv   *httptest.Server
	files map[string][]byte // path -> bytes
	docs  map[string]string // path -> body
	down  atomic.Bool
	hits  atomic.Int64
	// lie serves other bytes than the ones the hash names.
	lie atomic.Bool
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{files: map[string][]byte{}, docs: map[string]string{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		if u.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		p := r.URL.EscapedPath()
		if b, ok := u.files[p]; ok {
			if u.lie.Load() {
				b = []byte("not the published bytes")
			}
			_, _ = w.Write(b)
			return
		}
		if d, ok := u.docs[p+"|"+r.Header.Get("Accept")]; ok {
			_, _ = io.WriteString(w, d)
			return
		}
		if d, ok := u.docs[p]; ok {
			_, _ = io.WriteString(w, d)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

type rig struct {
	up    *upstream
	store *lancache.Store
	proxy *lancache.Proxy
	srv   *httptest.Server
	repo  string
	tok   string
	puts  atomic.Int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	up := newUpstream(t)
	dir := t.TempDir()
	st, err := lancache.Open(filepath.Join(dir, "store"), 1<<30, nil)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := lancache.LoadSecret(filepath.Join(dir, "packages.key"))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{up: up, store: st, repo: "luis-mc__site"}
	r.proxy = &lancache.Proxy{Store: st, Secret: secret, NpmUpstream: up.srv.URL, PypiUpstream: up.srv.URL,
		FilesUpstream: up.srv.URL + "/f", AfterPut: func() { r.puts.Add(1) }}
	r.tok = lancache.Token(secret, r.repo)
	r.srv = httptest.NewServer(r.proxy.Handler())
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) url(kind, rest string) string {
	return r.srv.URL + lancache.ProxyPrefix + kind + "/" + r.repo + "/" + r.tok + "/" + rest
}

func get(t *testing.T, u, accept string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func sri(b []byte) string {
	s := sha512.Sum512(b)
	return "sha512-" + base64.StdEncoding.EncodeToString(s[:])
}

// npmPackage puts a package document and its tarball on the upstream.
func (r *rig) npmPackage(name, version string, tgz []byte, integrity string) {
	esc := strings.ReplaceAll(name, "/", "%2F")
	file := name[strings.LastIndex(name, "/")+1:] + "-" + version + ".tgz"
	tarPath := "/" + name + "/-/" + file
	doc := map[string]any{"name": name, "versions": map[string]any{version: map[string]any{
		"dist": map[string]any{"tarball": r.up.srv.URL + tarPath, "integrity": integrity},
	}}}
	b, _ := json.Marshal(doc)
	r.up.docs["/"+esc] = string(b)
	r.up.files["/"+esc+"/-/"+file] = tgz
}

func TestNpmDocumentIsRewrittenToTheHub(t *testing.T) {
	r := newRig(t)
	tgz := []byte("left-pad tarball")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	code, body := get(t, r.url("npm", "left-pad"), "application/vnd.npm.install-v1+json")
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	want := `"tarball":"` + r.srv.URL + lancache.ProxyPrefix + "npm/" + r.repo + "/" + r.tok + "/left-pad/-/left-pad-1.3.0.tgz"
	if !strings.Contains(body, want) {
		t.Fatalf("tarball not rewritten:\n%s\nwant %s", body, want)
	}
	if strings.Contains(body, r.up.srv.URL) {
		t.Fatal("an upstream address is left in the document")
	}
	// A second read within 5 minutes is served from the store.
	before := r.up.hits.Load()
	if code, _ := get(t, r.url("npm", "left-pad"), "application/vnd.npm.install-v1+json"); code != 200 || r.up.hits.Load() != before {
		t.Fatalf("second read went upstream (code %d)", code)
	}
}

func TestNpmTarballIsCheckedAgainstTheUpstreamIntegrity(t *testing.T) {
	r := newRig(t)
	tgz := []byte("the real tarball bytes")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	code, body := get(t, r.url("npm", "left-pad/-/left-pad-1.3.0.tgz"), "")
	if code != 200 || body != string(tgz) {
		t.Fatalf("code %d body %q", code, body)
	}
	if r.puts.Load() == 0 {
		t.Fatal("the budget pass was not asked after a store")
	}
	// Stored: the upstream is not asked again, also when it is down.
	r.up.down.Store(true)
	if code, body := get(t, r.url("npm", "left-pad/-/left-pad-1.3.0.tgz"), ""); code != 200 || body != string(tgz) {
		t.Fatalf("stored tarball not served: %d %q", code, body)
	}
	// The SHA-1 shasum form of old packages is checked too.
	r.up.down.Store(false)
	old := []byte("an old package")
	s := sha1.Sum(old) //nolint:gosec // see import
	doc := fmt.Sprintf(`{"versions":{"0.1.0":{"dist":{"tarball":"%s/old/-/old-0.1.0.tgz","shasum":"%s"}}}}`, r.up.srv.URL, hex.EncodeToString(s[:]))
	r.up.docs["/old"] = doc
	r.up.files["/old/-/old-0.1.0.tgz"] = old
	if code, body := get(t, r.url("npm", "old/-/old-0.1.0.tgz"), ""); code != 200 || body != string(old) {
		t.Fatalf("shasum package: %d %q", code, body)
	}
}

func TestNpmTarballWithAWrongHashIsRefusedAndNotStored(t *testing.T) {
	r := newRig(t)
	tgz := []byte("published bytes")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	r.up.lie.Store(true)
	code, _ := get(t, r.url("npm", "left-pad/-/left-pad-1.3.0.tgz"), "")
	if code != http.StatusBadGateway {
		t.Fatalf("a wrong tarball got %d, want 502", code)
	}
	for n := range r.store.Entries(lancache.NSPackages + "/") {
		if strings.Contains(n, ".tgz") {
			t.Fatalf("a tarball that failed its hash was kept: %s", n)
		}
	}
	// A version with no published hash is never stored or served.
	r.up.lie.Store(false)
	r.up.docs["/nohash"] = fmt.Sprintf(`{"versions":{"1.0.0":{"dist":{"tarball":"%s/nohash/-/nohash-1.0.0.tgz"}}}}`, r.up.srv.URL)
	r.up.files["/nohash/-/nohash-1.0.0.tgz"] = []byte("x")
	if code, _ := get(t, r.url("npm", "nohash/-/nohash-1.0.0.tgz"), ""); code != http.StatusBadGateway {
		t.Fatalf("a tarball with no hash got %d, want 502", code)
	}
}

func TestNpmScopedNames(t *testing.T) {
	r := newRig(t)
	tgz := []byte("scoped tarball")
	r.npmPackage("@types/node", "20.0.0", tgz, sri(tgz))
	for _, doc := range []string{"@types%2fnode", "@types%2Fnode", "@types/node"} {
		code, body := get(t, r.url("npm", doc), "")
		if code != 200 || !strings.Contains(body, r.srv.URL+lancache.ProxyPrefix) {
			t.Fatalf("%s: %d %s", doc, code, body)
		}
	}
	for _, tar := range []string{"@types/node/-/node-20.0.0.tgz", "@types%2fnode/-/node-20.0.0.tgz"} {
		if code, body := get(t, r.url("npm", tar), ""); code != 200 || body != string(tgz) {
			t.Fatalf("%s: %d %q", tar, code, body)
		}
	}
	for _, bad := range []string{"..%2f..%2fetc", "%00", "left-pad/-/..%2fx.tgz", "left-pad/-/x.zip", "-/v1/search"} {
		if code, _ := get(t, r.url("npm", bad), ""); code == 200 {
			t.Fatalf("%s was answered", bad)
		}
	}
}

func TestStaleDocumentServedWhenUpstreamIsDown(t *testing.T) {
	r := newRig(t)
	now := time.Now()
	r.proxy.Now = func() time.Time { return now }
	tgz := []byte("t")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	if code, _ := get(t, r.url("npm", "left-pad"), ""); code != 200 {
		t.Fatal(code)
	}
	now = now.Add(10 * time.Minute) // older than 5 minutes: fetched again
	before := r.up.hits.Load()
	if code, _ := get(t, r.url("npm", "left-pad"), ""); code != 200 || r.up.hits.Load() == before {
		t.Fatalf("an old document was not fetched again (code %d)", code)
	}
	now = now.Add(10 * time.Minute)
	r.up.down.Store(true)
	code, body := get(t, r.url("npm", "left-pad"), "")
	if code != 200 || !strings.Contains(body, "left-pad-1.3.0.tgz") {
		t.Fatalf("stale document not served: %d %s", code, body)
	}
	// Nothing stored and upstream down: a 502, never an invented answer.
	if code, _ := get(t, r.url("npm", "never-seen"), ""); code != http.StatusBadGateway {
		t.Fatalf("unknown package with upstream down got %d", code)
	}
}

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestPypiHTMLIndexRewritesOnlyHashedLinks(t *testing.T) {
	r := newRig(t)
	whl := []byte("wheel bytes")
	sha := shaHex(whl)
	files := r.up.srv.URL + "/f"
	r.up.docs["/simple/requests/"] = `<html><body>` +
		`<a href="` + files + `/packages/ab/cd/requests-2.0-py3-none-any.whl#sha256=` + sha + `">requests-2.0-py3-none-any.whl</a>` +
		`<a href="` + files + `/packages/ef/gh/requests-1.0.tar.gz">requests-1.0.tar.gz</a></body></html>`
	code, body := get(t, r.url("pypi", "simple/Requests/"), "text/html")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	hub := r.srv.URL + lancache.ProxyPrefix + "pypi/" + r.repo + "/" + r.tok + "/files/" + sha + "/packages/ab/cd/requests-2.0-py3-none-any.whl#sha256=" + sha
	if !strings.Contains(body, `href="`+hub+`"`) {
		t.Fatalf("hashed link not rewritten:\n%s", body)
	}
	if !strings.Contains(body, `href="`+files+`/packages/ef/gh/requests-1.0.tar.gz"`) {
		t.Fatalf("a link without a hash must keep its upstream address:\n%s", body)
	}
}

func TestPypiJSONIndexIsRewritten(t *testing.T) {
	r := newRig(t)
	sha := shaHex([]byte("w"))
	files := r.up.srv.URL + "/f"
	doc := map[string]any{"meta": map[string]any{"api-version": "1.1"}, "name": "requests", "files": []any{
		map[string]any{"filename": "requests-2.0.whl", "url": files + "/packages/a/requests-2.0.whl", "hashes": map[string]any{"sha256": sha}},
		map[string]any{"filename": "requests-1.0.tar.gz", "url": files + "/packages/b/requests-1.0.tar.gz", "hashes": map[string]any{}},
	}}
	b, _ := json.Marshal(doc)
	r.up.docs["/simple/requests/"] = string(b)
	code, body := get(t, r.url("pypi", "simple/requests/"), "application/vnd.pypi.simple.v1+json, text/html; q=0.01")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var got struct {
		Files []struct {
			URL string `json:"url"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got.Files) != 2 {
		t.Fatalf("bad JSON: %v %s", err, body)
	}
	if want := r.srv.URL + lancache.ProxyPrefix + "pypi/" + r.repo + "/" + r.tok + "/files/" + sha + "/packages/a/requests-2.0.whl"; got.Files[0].URL != want {
		t.Fatalf("got %s want %s", got.Files[0].URL, want)
	}
	if got.Files[1].URL != files+"/packages/b/requests-1.0.tar.gz" {
		t.Fatalf("unhashed file rewritten: %s", got.Files[1].URL)
	}
}

func TestPypiFileIsCheckedAgainstItsSha256(t *testing.T) {
	r := newRig(t)
	whl := []byte("the wheel")
	sha := shaHex(whl)
	r.up.files["/f/packages/ab/x-1.0.whl"] = whl
	code, body := get(t, r.url("pypi", "files/"+sha+"/packages/ab/x-1.0.whl"), "")
	if code != 200 || body != string(whl) {
		t.Fatalf("%d %q", code, body)
	}
	r.up.lie.Store(true)
	r.up.files["/f/packages/ab/y-1.0.whl"] = whl
	if code, _ := get(t, r.url("pypi", "files/"+sha+"/packages/ab/y-1.0.whl"), ""); code != http.StatusBadGateway {
		t.Fatalf("a lying file host got %d, want 502", code)
	}
	for n := range r.store.Entries(lancache.NSPackages + "/") {
		if strings.Contains(n, "y-1.0.whl") {
			t.Fatal("a file that failed its hash was kept")
		}
	}
	for _, bad := range []string{"files/nothex/packages/x.whl", "files/" + sha + "/../x.whl", "files/" + sha + "/", "simple/..%2f/"} {
		if code, _ := get(t, r.url("pypi", bad), ""); code == 200 {
			t.Fatalf("%s was answered", bad)
		}
	}
}

func TestProxyRefusesABadTokenAPublicAddressAndOff(t *testing.T) {
	r := newRig(t)
	tgz := []byte("t")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	bad := r.srv.URL + lancache.ProxyPrefix + "npm/" + r.repo + "/" + strings.Repeat("0", 32) + "/left-pad"
	if code, _ := get(t, bad, ""); code != http.StatusUnauthorized {
		t.Fatalf("bad token got %d", code)
	}
	if code, _ := get(t, r.srv.URL+lancache.ProxyPrefix+"ping", ""); code != 200 {
		t.Fatalf("ping got %d", code)
	}
	on := false
	r.proxy.On = func() bool { return on }
	if code, _ := get(t, r.url("npm", "left-pad"), ""); code != http.StatusNotFound {
		t.Fatalf("off got %d", code)
	}
	if code, _ := get(t, r.srv.URL+lancache.ProxyPrefix+"ping", ""); code != http.StatusNotFound {
		t.Fatalf("ping while off got %d", code)
	}
	on = true
	r.proxy.Remote = func(string) bool { return false }
	if code, _ := get(t, r.url("npm", "left-pad"), ""); code != http.StatusForbidden {
		t.Fatalf("public address got %d", code)
	}
	for addr, want := range map[string]bool{"10.1.2.3:5": true, "192.168.1.9:80": true, "[::1]:9": true, "169.254.1.1:1": true, "8.8.8.8:53": false, "[2001:4860::1]:1": false, "garbage": false} {
		if got := lancache.PrivateRemote(addr); got != want {
			t.Errorf("PrivateRemote(%s) = %v", addr, got)
		}
	}
}

func TestProxyKeysNeverCrossRepositories(t *testing.T) {
	r := newRig(t)
	tgz := []byte("t")
	r.npmPackage("left-pad", "1.3.0", tgz, sri(tgz))
	if code, _ := get(t, r.url("npm", "left-pad/-/left-pad-1.3.0.tgz"), ""); code != 200 {
		t.Fatal(code)
	}
	// The other repository's token does not open this repository's path.
	other := "luis-mc__other"
	otherTok := lancache.Token(r.proxy.Secret, other)
	if otherTok == r.tok {
		t.Fatal("two repositories got one token")
	}
	mixed := r.srv.URL + lancache.ProxyPrefix + "npm/" + r.repo + "/" + otherTok + "/left-pad"
	if code, _ := get(t, mixed, ""); code != http.StatusUnauthorized {
		t.Fatalf("another repository's token got %d", code)
	}
	// Its own path reads its own scope: a stored file of the first repository is a miss there.
	r.up.down.Store(true)
	u := r.srv.URL + lancache.ProxyPrefix + "npm/" + other + "/" + otherTok + "/left-pad/-/left-pad-1.3.0.tgz"
	if code, _ := get(t, u, ""); code == 200 {
		t.Fatal("one repository read another's stored tarball")
	}
	for n := range r.store.Entries(lancache.NSPackages + "/") {
		if !strings.HasPrefix(n, lancache.NSPackages+"/"+r.repo+"/") {
			t.Fatalf("stored outside the repository scope: %s", n)
		}
	}
	if k, ok := lancache.RepoKey("Luis-MC/Site"); !ok || k != "luis-mc__site" {
		t.Fatalf("RepoKey = %q %v", k, ok)
	}
	for _, bad := range []string{"luis-mc", "a/b/c", "/x", "a/"} {
		if _, ok := lancache.RepoKey(bad); ok {
			t.Errorf("RepoKey(%q) accepted", bad)
		}
	}
}

func TestTokenSecretIsKeptAndPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hub", "packages.key")
	a, err := lancache.LoadSecret(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lancache.LoadSecret(p)
	if err != nil || string(a) != string(b) {
		t.Fatal("the secret changed between two loads")
	}
	if lancache.Token(a, "x") != lancache.Token(b, "x") || len(lancache.Token(a, "x")) != 32 {
		t.Fatal("token is not stable")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("secret mode %v %v", fi.Mode().Perm(), err)
		}
	}
	if err := os.WriteFile(p, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lancache.LoadSecret(p); err == nil {
		t.Fatal("a damaged secret was accepted")
	}
}
