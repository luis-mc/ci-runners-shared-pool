package lancache_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// crate puts a crate's index file and .crate file on the fake upstream.
func (r *rig) crate(name, version string, body []byte, cksum string) {
	if cksum == "" {
		s := sha256.Sum256(body)
		cksum = hex.EncodeToString(s[:])
	}
	line, _ := json.Marshal(map[string]any{"name": name, "vers": version, "cksum": cksum})
	idx := map[string]string{"serde": "se/rd/serde", "ab": "2/ab"}[name]
	r.up.docs["/"+idx] = string(line) + "\n"
	r.up.files["/crates/"+name+"/"+name+"-"+version+".crate"] = body
	r.proxy.CratesIndexUpstream = r.up.srv.URL
	r.proxy.CratesFilesUpstream = r.up.srv.URL + "/crates"
}

func TestCratesConfigPointsDownloadsAtTheHub(t *testing.T) {
	r := newRig(t)
	code, body := get(t, r.url("crates", "config.json"), "")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatal(err)
	}
	if want := r.srv.URL + "/pkg/crates/" + r.repo + "/" + r.tok + "/-/dl"; cfg["dl"] != want {
		t.Fatalf("dl = %q, want %q", cfg["dl"], want)
	}
}

func TestCratesIndexAndFileAreStoredAndServedAgain(t *testing.T) {
	r := newRig(t)
	crate := []byte("crate bytes")
	r.crate("serde", "1.0.0", crate, "")
	if code, body := get(t, r.url("crates", "se/rd/serde"), ""); code != 200 || !strings.Contains(body, `"vers":"1.0.0"`) {
		t.Fatalf("index: %d %q", code, body)
	}
	dl := r.url("crates", "-/dl/serde/1.0.0/download")
	if code, body := get(t, dl, ""); code != 200 || body != string(crate) {
		t.Fatalf("file: %d %q", code, body)
	}
	before := r.up.hits.Load()
	if code, body := get(t, dl, ""); code != 200 || body != string(crate) {
		t.Fatalf("second file: %d %q", code, body)
	}
	if r.up.hits.Load() != before {
		t.Fatal("a stored crate must not be fetched again")
	}
	if r.puts.Load() < 2 {
		t.Fatalf("budget pass runs after each put, got %d", r.puts.Load())
	}
}

func TestCratesFileWithAWrongChecksumIsRefusedAndNotStored(t *testing.T) {
	r := newRig(t)
	r.crate("serde", "1.0.1", []byte("good"), strings.Repeat("0", 64))
	if code, _ := get(t, r.url("crates", "-/dl/serde/1.0.1/download"), ""); code != 502 {
		t.Fatalf("code %d, want 502", code)
	}
	if n := len(r.store.Entries(lancache.NSPackages + "/")); n != 1 {
		t.Fatalf("only the index may be stored, got %d entries", n)
	}
}

func TestCratesBadPathsAreRefused(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"se/rd/other", "../x", "-/dl/serde/1.0.0/other", "-/dl/se%2Frde/1/download"} {
		if code, _ := get(t, r.url("crates", p), ""); code != 400 && code != 404 {
			t.Errorf("%s: code %d", p, code)
		}
	}
}

func TestCratesStaleIndexServedWhenUpstreamIsDown(t *testing.T) {
	r := newRig(t)
	r.crate("ab", "0.1.0", []byte("x"), "")
	now := time.Now()
	r.proxy.Now = func() time.Time { return now }
	if code, _ := get(t, r.url("crates", "2/ab"), ""); code != 200 {
		t.Fatalf("code %d", code)
	}
	now = now.Add(10 * time.Minute)
	r.up.down.Store(true)
	if code, body := get(t, r.url("crates", "2/ab"), ""); code != 200 || !strings.Contains(body, "0.1.0") {
		t.Fatalf("stale: %d %q", code, body)
	}
}

func (r *rig) apt(host string) {
	r.proxy.AptUpstreams = map[string]string{host: r.up.srv.URL + "/apt"}
}

func TestAptPoolFileIsStoredAndNotFetchedAgain(t *testing.T) {
	r := newRig(t)
	r.apt("archive.ubuntu.com")
	deb := []byte("deb bytes")
	r.up.files["/apt/ubuntu/pool/main/c/curl/curl_8.5.0-2ubuntu10_amd64.deb"] = deb
	u := r.url("apt", "archive.ubuntu.com/ubuntu/pool/main/c/curl/curl_8.5.0-2ubuntu10_amd64.deb")
	if code, body := get(t, u, ""); code != 200 || body != string(deb) {
		t.Fatalf("%d %q", code, body)
	}
	before := r.up.hits.Load()
	if code, body := get(t, u, ""); code != 200 || body != string(deb) {
		t.Fatalf("again: %d %q", code, body)
	}
	if r.up.hits.Load() != before {
		t.Fatal("a stored .deb must not be fetched again")
	}
}

func TestAptIndexIsFreshFor5MinutesThenStaleServed(t *testing.T) {
	r := newRig(t)
	r.apt("deb.debian.org")
	r.up.docs["/apt/debian/dists/bookworm/InRelease"] = "release v1"
	now := time.Now()
	r.proxy.Now = func() time.Time { return now }
	u := r.url("apt", "deb.debian.org/debian/dists/bookworm/InRelease")
	if _, b := get(t, u, ""); b != "release v1" {
		t.Fatalf("body %q", b)
	}
	r.up.docs["/apt/debian/dists/bookworm/InRelease"] = "release v2"
	if _, b := get(t, u, ""); b != "release v1" {
		t.Fatalf("a fresh index must come from the store, got %q", b)
	}
	now = now.Add(6 * time.Minute)
	if _, b := get(t, u, ""); b != "release v2" {
		t.Fatalf("an old index must be fetched again, got %q", b)
	}
	now = now.Add(6 * time.Minute)
	r.up.down.Store(true)
	if code, b := get(t, u, ""); code != 200 || b != "release v2" {
		t.Fatalf("stale: %d %q", code, b)
	}
}

func TestAptRefusesOtherHostsAndBadPaths(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"evil.example.com/x", "archive.ubuntu.com", "archive.ubuntu.com/ubuntu/../x", "archive.ubuntu.com/a b"} {
		if code, _ := get(t, r.url("apt", p), ""); code != 404 && code != 400 {
			t.Errorf("%s: code %d", p, code)
		}
	}
}

func TestAptSourcesPointsAllowedHostsAtTheHubAndIsIdempotent(t *testing.T) {
	mirror := "https://10.0.0.5:8460/pkg/apt/o__r/abc123"
	in := "deb http://archive.ubuntu.com/ubuntu noble main\n" +
		"URIs: http://security.ubuntu.com/ubuntu\n" +
		"deb https://download.docker.com/linux/ubuntu noble stable\n"
	out := lancache.AptSources(in, "10.0.0.5:8460", mirror)
	want := "deb " + mirror + "/archive.ubuntu.com/ubuntu noble main\n" +
		"URIs: " + mirror + "/security.ubuntu.com/ubuntu\n" +
		"deb https://download.docker.com/linux/ubuntu noble stable\n"
	if out != want {
		t.Fatalf("got\n%s", out)
	}
	if again := lancache.AptSources(out, "10.0.0.5:8460", mirror); again != out {
		t.Fatalf("not idempotent:\n%s", again)
	}
	other := lancache.AptSources(out, "10.0.0.5:8460", "https://10.0.0.5:8460/pkg/apt/o__r/def456")
	if !strings.Contains(other, "/def456/archive.ubuntu.com/") || strings.Contains(other, "abc123") {
		t.Fatalf("an earlier token must be replaced:\n%s", other)
	}
}

func TestCargoConfigReplacesCratesIoWithTheHub(t *testing.T) {
	c := lancache.CargoConfig("https://h:1/pkg/crates/o__r/t/", "/b.pem")
	for _, w := range []string{lancache.CargoConfigMarker, `replace-with = "crsp-hub"`, `registry = "sparse+https://h:1/pkg/crates/o__r/t/"`, `cainfo = "/b.pem"`} {
		if !strings.Contains(c, w) {
			t.Errorf("missing %q in\n%s", w, c)
		}
	}
}

func TestProxyKeysNeverCrossRepositoriesForCratesAndApt(t *testing.T) {
	r := newRig(t)
	r.crate("serde", "1.0.0", []byte("a"), "")
	get(t, r.url("crates", "se/rd/serde"), "")
	for n := range r.store.Entries(lancache.NSPackages + "/") {
		if !strings.HasPrefix(n, lancache.NSPackages+"/"+r.repo+"/") {
			t.Fatalf("entry %q is not scoped by the repository", n)
		}
	}
}
