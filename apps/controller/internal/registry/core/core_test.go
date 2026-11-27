package core

import (
	"bytes"
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
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/keys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// ---- fakes

type memLeases struct {
	mu sync.Mutex
	m  map[string][]string
}

func (l *memLeases) Lease(_ context.Context, jti, _ string, names []string, _ time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string][]string{}
	}
	l.m[jti] = names
	return nil
}

func (l *memLeases) Live(_ context.Context, jti string, _ time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.m[jti]
	return ok, nil
}

func (l *memLeases) Revoke(_ context.Context, jti string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, jti)
	return nil
}

type memLedger struct {
	mu      sync.Mutex
	tags    map[string]bool
	staged  map[string]registry.Image
	refused map[string]string
	done    map[string]registry.Image
}

func newLedger() *memLedger {
	return &memLedger{tags: map[string]bool{}, staged: map[string]registry.Image{}, refused: map[string]string{}, done: map[string]registry.Image{}}
}

func (l *memLedger) TagExists(_ context.Context, name, tag string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tags[name+":"+tag], nil
}

func (l *memLedger) Stage(_ context.Context, img registry.Image, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.staged[img.ID] = img
	return nil
}

func (l *memLedger) Refuse(_ context.Context, id, why string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refused[id] = why
	return nil
}

func (l *memLedger) Promoted(_ context.Context, img registry.Image) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tags[img.Name+":"+img.Key.String()] = true
	l.done[img.ID] = img
	return nil
}

// ---- harness

type harness struct {
	t      *testing.T
	reg    *Registry
	iss    *Issuer
	ledger *memLedger
	prom   *Promoter
	srv    *httptest.Server
	dir    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	iss := &Issuer{Secret: bytes.Repeat([]byte{7}, 32), Leases: &memLeases{}}
	dir := filepath.Join(t.TempDir(), "registry")
	reg, err := New(context.Background(), Options{Dir: dir, ListenAddr: "127.0.0.1:8460", Tokens: iss})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	mux := http.NewServeMux()
	mux.Handle("/v2/", reg.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	led := newLedger()
	return &harness{t: t, reg: reg, iss: iss, ledger: led, prom: &Promoter{Registry: reg, Ledger: led}, srv: srv, dir: dir}
}

func (h *harness) token(machine string, want ...registry.Scope) string {
	h.t.Helper()
	tok, err := h.iss.Issue(context.Background(), machine, want)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok.Value
}

func pull(names ...string) []registry.Scope {
	var out []registry.Scope
	for _, n := range names {
		out = append(out, registry.Scope{Name: n, Actions: []string{ActionPull}})
	}
	return out
}

func push(names ...string) []registry.Scope {
	var out []registry.Scope
	for _, n := range names {
		out = append(out, registry.Scope{Name: n, Actions: []string{ActionPush, ActionPull}})
	}
	return out
}

// reply is what a test keeps of a response, with its body already closed.
type reply struct {
	StatusCode int
	Header     http.Header
}

func (h *harness) do(token, method, path string, body []byte, hdr ...string) (reply, []byte) {
	h.t.Helper()
	url := path
	if !strings.HasPrefix(path, "http") {
		url = h.srv.URL + path
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// putBlob uploads b to name with one monolithic upload.
func (h *harness) putBlob(token, name string, b []byte) string {
	h.t.Helper()
	resp, body := h.do(token, http.MethodPost, "/v2/"+name+"/blobs/uploads/", nil)
	if resp.StatusCode != http.StatusAccepted {
		h.t.Fatalf("start upload: %d %s", resp.StatusCode, body)
	}
	loc := resp.Header.Get("Location")
	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	d := sum(b)
	resp, body = h.do(token, http.MethodPut, loc+sep+"digest="+d, b, "Content-Type", "application/octet-stream")
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("finish upload: %d %s", resp.StatusCode, body)
	}
	return d
}

func (h *harness) putManifest(token, name, ref string, m []byte) (reply, []byte) {
	h.t.Helper()
	return h.do(token, http.MethodPut, "/v2/"+name+"/manifests/"+ref, m, "Content-Type", "application/vnd.oci.image.manifest.v1+json")
}

// image is a pushed test image.
type image struct {
	manifest []byte
	digest   string
	key      registry.Key
	input    registry.KeyInput
	labels   map[string]string
}

func testInput(profile string) registry.KeyInput {
	return registry.KeyInput{
		Profile: profile, StackHash: strings.Repeat("a", 64), Arch: "amd64",
		BaseDigest: "sha256:" + strings.Repeat("1", 64), Pins: map[string]string{"runner": "2.330.0"},
	}
}

// buildImage makes the config, one layer and the manifest of an image whose
// labels follow the key input, and lets edit change the labels first.
func (h *harness) pushImage(token, staging, source string, in registry.KeyInput, edit func(map[string]string)) image {
	h.t.Helper()
	key, err := registry.ComputeKey(in)
	if err != nil {
		h.t.Fatal(err)
	}
	ref := registry.ProfileRef{ID: in.Profile, BaseName: "ubuntu:24.04"}
	labels := keys.LabelsFor(key, in, ref, source, "c0ffee")
	if edit != nil {
		edit(labels)
	}
	layer := []byte("layer of " + staging + string(key))
	layerD := h.putBlob(token, staging, layer)
	cfg, _ := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux", "config": map[string]any{"Labels": labels},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{layerD}},
	})
	cfgD := h.putBlob(token, staging, cfg)
	m, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": cfgD, "size": len(cfg)},
		"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": layerD, "size": len(layer)}},
	})
	resp, body := h.putManifest(token, staging, key.String(), m)
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("push manifest: %d %s", resp.StatusCode, body)
	}
	return image{manifest: m, digest: sum(m), key: key, input: in, labels: labels}
}

func (h *harness) staged(machine, target, source string, im image) registry.Staged {
	return registry.Staged{
		MachineID: machine, Name: registry.IncomingName(machine, target), Target: target, Key: im.key,
		Manifest: im.digest, KeyInput: im.input, Source: source, Commit: "c0ffee",
	}
}

// ---- tests

// R5.2: never a public or wildcard address, never a public peer.
func TestNeverPublic(t *testing.T) {
	iss := &Issuer{Secret: bytes.Repeat([]byte{1}, 32)}
	for _, addr := range []string{"0.0.0.0:8460", ":8460", "[::]:8460", "8.8.8.8:8460", "example.com:8460", "8460"} {
		if _, err := New(context.Background(), Options{Dir: t.TempDir(), ListenAddr: addr, Tokens: iss}); err == nil {
			t.Errorf("registry mounted on %q", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8460", "192.168.1.20:8460", "10.0.0.5:8460", "[fe80::1]:8460"} {
		if err := CheckListen(addr); err != nil {
			t.Errorf("%q refused: %v", addr, err)
		}
	}
	for remote, want := range map[string]bool{"127.0.0.1:5": true, "192.168.0.9:5": true, "8.8.8.8:5": false, "[2001:db8::1]:5": false, "0.0.0.0:5": false, "junk": false} {
		if PeerAllowed(remote) != want {
			t.Errorf("PeerAllowed(%q) != %v", remote, want)
		}
	}
	h := newHarness(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v2/", nil)
	req.RemoteAddr = "8.8.8.8:4000"
	req.Header.Set("Authorization", "Bearer "+h.token("m1", pull("crsp/std/*")...))
	rec := httptest.NewRecorder()
	h.reg.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a public peer got %d", rec.Code)
	}
}

// R5.3: no token, no answer; a forged, expired or revoked token neither.
func TestAnonymousRefused(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/v2/", "/v2/crsp/std/base/tags/list", "/v2/crsp/std/base/manifests/abc123abc123", "/v2/_catalog"} {
		resp, _ := h.do("", http.MethodGet, p, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d", p, resp.StatusCode)
		}
		if len(resp.Header.Values("WWW-Authenticate")) == 0 {
			t.Errorf("no challenge on %s", p)
		}
	}
	good := h.token("m1", pull("crsp/std/*")...)
	if resp, _ := h.do(good, http.MethodGet, "/v2/", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a good token got %d", resp.StatusCode)
	}
	forged := good[:len(good)-3] + "AAA"
	if resp, _ := h.do(forged, http.MethodGet, "/v2/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged token got %d", resp.StatusCode)
	}
	if resp, _ := h.do("junk", http.MethodGet, "/v2/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("junk token got %d", resp.StatusCode)
	}
	if err := h.iss.Revoke(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.do(good, http.MethodGet, "/v2/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked token got %d", resp.StatusCode)
	}
	// the token also works as the password of a Basic login
	b := h.token("m1", pull("crsp/std/*")...)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.srv.URL+"/v2/", nil)
	req.SetBasicAuth("machine", b)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("basic login got %d", resp.StatusCode)
	}
}

// R5.10: ten minutes, HMAC, and only the id, machine and names are recorded.
func TestTokenLifeAndRecord(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	leases := &memLeases{}
	iss := &Issuer{Secret: bytes.Repeat([]byte{9}, 32), Leases: leases, Now: func() time.Time { return now }}
	tok, err := iss.Issue(context.Background(), "m1", pull("crsp/std/base"))
	if err != nil {
		t.Fatal(err)
	}
	if !tok.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expires %v", tok.ExpiresAt)
	}
	if len(leases.m) != 1 {
		t.Fatalf("leases = %v", leases.m)
	}
	for _, names := range leases.m {
		if len(names) != 1 || names[0] != "crsp/std/base" {
			t.Fatalf("names = %v", names)
		}
		for _, n := range names {
			if strings.Contains(n, tok.Value) {
				t.Fatal("the token value was recorded")
			}
		}
	}
	m, sc, err := iss.Verify(context.Background(), tok.Value)
	if err != nil || m != "m1" || !Allows(sc, "crsp/std/base", ActionPull) {
		t.Fatalf("verify = %v %v %v", m, sc, err)
	}
	now = now.Add(10*time.Minute + time.Second)
	if _, _, err := iss.Verify(context.Background(), tok.Value); !errors.Is(err, ErrBadToken) {
		t.Fatalf("an expired token verified: %v", err)
	}
	other := &Issuer{Secret: bytes.Repeat([]byte{8}, 32), Now: func() time.Time { return now.Add(-time.Hour) }}
	if _, _, err := other.Verify(context.Background(), tok.Value); !errors.Is(err, ErrBadToken) {
		t.Fatalf("a token of another key verified: %v", err)
	}
}

// R5.11: pull std and custom, push only to the own staging.
func TestTokenScopes(t *testing.T) {
	iss := &Issuer{Secret: bytes.Repeat([]byte{3}, 32)}
	ctx := context.Background()
	ok := [][]registry.Scope{
		pull("crsp/std/node-web"), pull("crsp/custom/o-r/node-web"), pull("crsp/std/*", "crsp/custom/*"),
		push("crsp/incoming/m1/std/node-web"), push("crsp/incoming/m1/*"), pull("crsp/incoming/m1/std/node-web"),
	}
	for _, w := range ok {
		if _, err := iss.Issue(ctx, "m1", w); err != nil {
			t.Errorf("%v refused: %v", w, err)
		}
	}
	bad := map[string][]registry.Scope{
		"push to another machine":     push("crsp/incoming/m2/std/node-web"),
		"push to the whole incoming":  push("crsp/incoming/*"),
		"push to a prefix of another": push("crsp/incoming/m1x/std/a"),
		"push to std":                 push("crsp/std/node-web"),
		"push to custom":              push("crsp/custom/o-r/node-web"),
		"push to the root":            push("crsp/*"),
		"pull another staging":        pull("crsp/incoming/m2/std/node-web"),
		"pull outside crsp":           pull("library/ubuntu"),
		"pull everything":             pull("*"),
		"delete":                      {{Name: "crsp/std/node-web", Actions: []string{"delete"}}},
		"no action":                   {{Name: "crsp/std/node-web"}},
		"old staging name":            push("crsp/_incoming/m1/std/node-web"),
		"nothing":                     nil,
	}
	for what, w := range bad {
		if _, err := iss.Issue(ctx, "m1", w); err == nil {
			t.Errorf("%s was granted", what)
		}
	}
	for _, id := range []string{"", "a/b", "..", "m1*"} {
		if _, err := iss.Issue(ctx, id, pull("crsp/std/x")); err == nil {
			t.Errorf("machine id %q accepted", id)
		}
	}
	if _, err := (&Issuer{Secret: []byte("short")}).Issue(ctx, "m1", pull("crsp/std/x")); err == nil {
		t.Error("a short key signed a token")
	}
}

type refuseGate struct{}

func (refuseGate) AllowPush(context.Context, string) error { return registry.ErrOverBudget }

// R5.8: a push token is not given when the disk or the budget has no room; a pull token still is.
func TestPushGate(t *testing.T) {
	iss := &Issuer{Secret: bytes.Repeat([]byte{4}, 32), Gate: refuseGate{}}
	if _, err := iss.Issue(context.Background(), "m1", push("crsp/incoming/m1/std/a")); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("err = %v", err)
	}
	if _, err := iss.Issue(context.Background(), "m1", pull("crsp/std/a")); err != nil {
		t.Fatal(err)
	}
}

// R5.4: staging is crsp/incoming; crsp/_incoming is not a valid OCI name.
func TestNames(t *testing.T) {
	if ValidName("crsp/_incoming/m1/std/a") {
		t.Error("an underscore-led path component is not a valid OCI name")
	}
	if !ValidName(registry.IncomingName("m_7f3a", registry.StdName("node-web"))) {
		t.Error("the staging name is not valid")
	}
	for name, want := range map[string]registry.Kind{
		"crsp/std/node-web": registry.KindStandard, "crsp/custom/o-r/node-web": registry.KindCustom,
	} {
		if k, ok := TargetKind(name); !ok || k != want {
			t.Errorf("TargetKind(%q) = %v %v", name, k, ok)
		}
	}
	for _, name := range []string{"crsp/std/", "crsp/std/a/b", "crsp/custom/o-r", "crsp/custom/a/b/c", "crsp/incoming/m1/std/a", "x/std/a", "crsp/std/A"} {
		if _, ok := TargetKind(name); ok {
			t.Errorf("TargetKind(%q) accepted", name)
		}
	}
	if !strings.HasPrefix(registry.IncomingPrefix, "crsp/incoming/") {
		t.Error(registry.IncomingPrefix)
	}
}

// A machine reaches only what its token names.
func TestDoorScopes(t *testing.T) {
	h := newHarness(t)
	m1 := h.token("m1", push("crsp/incoming/m1/std/base")...)
	m2 := h.token("m2", push("crsp/incoming/m2/std/base")...)
	puller := h.token("m3", pull("crsp/std/*", "crsp/custom/*")...)
	for who, tc := range map[string]struct{ tok, method, path string }{
		"push to another staging":                 {m1, http.MethodPost, "/v2/crsp/incoming/m2/std/base/blobs/uploads/"},
		"push to std":                             {m1, http.MethodPost, "/v2/crsp/std/base/blobs/uploads/"},
		"puller pushes":                           {puller, http.MethodPost, "/v2/crsp/incoming/m3/std/base/blobs/uploads/"},
		"pull the staging of another":             {m1, http.MethodGet, "/v2/crsp/incoming/m2/std/base/tags/list"},
		"delete a manifest":                       {m1, http.MethodDelete, "/v2/crsp/incoming/m1/std/base/manifests/sha256:" + strings.Repeat("a", 64)},
		"delete a blob":                           {m1, http.MethodDelete, "/v2/crsp/incoming/m1/std/base/blobs/sha256:" + strings.Repeat("a", 64)},
		"list the catalogue":                      {m1, http.MethodGet, "/v2/_catalog"},
		"mount from a repository m1 may not read": {m1, http.MethodPost, "/v2/crsp/incoming/m1/std/base/blobs/uploads/?mount=sha256:" + strings.Repeat("a", 64) + "&from=crsp/incoming/m2/std/base"},
	} {
		resp, _ := h.do(tc.tok, tc.method, tc.path, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", who, resp.StatusCode)
		}
	}
	_ = m2
	if resp, _ := h.do(m1, http.MethodPost, "/v2/crsp/incoming/m1/std/base/blobs/uploads/", nil); resp.StatusCode != http.StatusAccepted {
		t.Errorf("own staging: %d", resp.StatusCode)
	}
}

// R5.1, R5.5, R5.9: push to staging, promote, then pull by digest from std; the
// blobs are linked, not copied.
func TestPushPromotePull(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staging := registry.IncomingName("m1", registry.StdName("base"))
	pushTok := h.token("m1", push(staging)...)
	im := h.pushImage(pushTok, staging, "", testInput("base"), nil)

	// Not offered before the promotion.
	pullTok := h.token("m2", pull("crsp/std/*", "crsp/custom/*")...)
	if resp, _ := h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+im.digest, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a staged image was offered: %d", resp.StatusCode)
	}
	before := blobFiles(t, h.dir)

	var hooked registry.Image
	h.prom.OnPromoted(func(_ context.Context, img registry.Image) error { hooked = img; return nil })
	img, err := h.prom.Promote(ctx, h.staged("m1", registry.StdName("base"), "", im))
	if err != nil {
		t.Fatal(err)
	}
	if img.State != registry.StateCurrent || img.Name != "crsp/std/base" || img.Manifest != im.digest || img.Key != im.key {
		t.Fatalf("image = %+v", img)
	}
	if hooked.ID != img.ID {
		t.Fatal("the promoted hook was not called")
	}
	if after := blobFiles(t, h.dir); after != before {
		t.Fatalf("promotion moved data: %d blob files before, %d after", before, after)
	}
	if !h.ledger.tags["crsp/std/base:"+im.key.String()] {
		t.Fatal("no tag row")
	}

	// Pull by tag and by digest, then the blobs.
	resp, body := h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+im.key.String(), nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, im.manifest) || resp.Header.Get("Docker-Content-Digest") != im.digest {
		t.Fatalf("pull by tag: %d", resp.StatusCode)
	}
	resp, body = h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+im.digest, nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, im.manifest) {
		t.Fatalf("pull by digest: %d", resp.StatusCode)
	}
	var m struct {
		Config struct{ Digest string }
		Layers []struct{ Digest string }
	}
	_ = json.Unmarshal(body, &m)
	for _, d := range []string{m.Config.Digest, m.Layers[0].Digest} {
		resp, b := h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/blobs/"+d, nil)
		if resp.StatusCode != http.StatusOK || sum(b) != d {
			t.Fatalf("blob %s: %d", d, resp.StatusCode)
		}
	}
	// A machine cannot push over the promoted tag.
	if resp, _ := h.putManifest(pullTok, "crsp/std/base", im.key.String(), im.manifest); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("push to std: %d", resp.StatusCode)
	}
	// Events carry the machine of the token.
	var pulls int
	for done := false; !done; {
		select {
		case ev := <-h.reg.Events():
			if ev.Action == "pull" && ev.MachineID == "m2" && ev.Name == "crsp/std/base" && strings.HasPrefix(ev.Digest, "sha256:") {
				pulls++
			}
			if ev.Action == "push" && ev.MachineID != "m1" {
				t.Errorf("push event %+v", ev)
			}
		default:
			done = true
		}
	}
	if pulls != 2 {
		t.Fatalf("pull events = %d", pulls)
	}
}

func blobFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(dir, "docker", "registry", "v2", "blobs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "data" {
			n++
		}
		return nil
	})
	return n
}

// R5.5: a custom image promotes to crsp/custom/<owner>-<repo>/<profile>, a branch run is a variant.
func TestPromoteCustomAndBranch(t *testing.T) {
	h := newHarness(t)
	target := registry.CustomName("Acme", "Web", "node-web")
	staging := registry.IncomingName("m1", target)
	tok := h.token("m1", push(staging)...)
	im := h.pushImage(tok, staging, "Acme/Web", testInput("node-web"), nil)
	s := h.staged("m1", target, "Acme/Web", im)
	s.Branch = "feature"
	img, err := h.prom.Promote(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if img.Kind != registry.KindCustom || img.State != registry.StateVariant || img.Name != "crsp/custom/acme-web/node-web" {
		t.Fatalf("image = %+v", img)
	}
}

func (h *harness) wantRefused(s registry.Staged, why string) registry.Image {
	h.t.Helper()
	img, err := h.prom.Promote(context.Background(), s)
	if !errors.Is(err, registry.ErrPromoteRefuse) {
		h.t.Fatalf("err = %v, want a refusal (%s)", err, why)
	}
	if why != "" && !strings.Contains(err.Error(), why) {
		h.t.Fatalf("err = %v, want it to say %q", err, why)
	}
	return img
}

// R5.5: a digest that differs from the manifest in staging is refused and recorded.
func TestPromotionRefusesABadDigest(t *testing.T) {
	h := newHarness(t)
	staging := registry.IncomingName("m1", registry.StdName("base"))
	im := h.pushImage(h.token("m1", push(staging)...), staging, "", testInput("base"), nil)
	s := h.staged("m1", registry.StdName("base"), "", im)
	s.Manifest = sum([]byte("something else"))
	img := h.wantRefused(s, "the machine reported")
	if h.ledger.refused[img.ID] == "" {
		t.Fatal("the refusal was not recorded")
	}
	if len(h.ledger.done) != 0 || h.ledger.tags["crsp/std/base:"+im.key.String()] {
		t.Fatal("a refused image was promoted")
	}
	s.Manifest = "not a digest"
	h.wantRefused(s, "not a digest")
	// And nothing is offered under the target.
	pullTok := h.token("m2", pull("crsp/std/*")...)
	if resp, _ := h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+im.key.String(), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a refused image was offered: %d", resp.StatusCode)
	}
}

// R5.6: tags are immutable; a second promotion to the same (name, tag) is refused.
func TestPromotionRefusesAReusedTag(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	in := testInput("base")
	s1 := registry.IncomingName("m1", target)
	im1 := h.pushImage(h.token("m1", push(s1)...), s1, "", in, nil)
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", target, "", im1)); err != nil {
		t.Fatal(err)
	}
	// Another machine built the same key: its bytes differ, the tag is the same.
	s2 := registry.IncomingName("m2", target)
	im2 := h.pushImage(h.token("m2", push(s2)...), s2, "", in, nil)
	if im2.digest == im1.digest {
		t.Fatal("the two images are the same")
	}
	_, err := h.prom.Promote(context.Background(), h.staged("m2", target, "", im2))
	if !errors.Is(err, registry.ErrTagExists) || !errors.Is(err, registry.ErrPromoteRefuse) {
		t.Fatalf("err = %v", err)
	}
	// The registry itself also holds the line when the store has no row.
	h.ledger.tags = map[string]bool{}
	_, err = h.prom.Promote(context.Background(), h.staged("m2", target, "", im2))
	if !errors.Is(err, registry.ErrTagExists) {
		t.Fatalf("err = %v", err)
	}
	// The first image is untouched.
	pullTok := h.token("m3", pull("crsp/std/*")...)
	_, body := h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+im1.key.String(), nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if !bytes.Equal(body, im1.manifest) {
		t.Fatal("the promoted tag changed")
	}
}

// R5.11: a push outside the own staging is refused at the door and at promotion.
func TestPromotionRefusesAPushOutsideOwnStaging(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	other := registry.IncomingName("m2", target)
	im := h.pushImage(h.token("m2", push(other)...), other, "", testInput("base"), nil)
	// m1 reports m2's staging name as its own push.
	s := h.staged("m1", target, "", im)
	s.Name = other
	h.wantRefused(s, "staging root of machine m1")
	// A staging name that is not below crsp/incoming at all.
	s.Name = "crsp/std/base"
	h.wantRefused(s, "staging root")
	// A target that is not a promoted name.
	s = h.staged("m2", "crsp/incoming/m2/x", "", im)
	h.wantRefused(s, "image name")
	s = h.staged("m2", "library/ubuntu", "", im)
	h.wantRefused(s, "image name")
	if len(h.ledger.done) != 0 {
		t.Fatal("something was promoted")
	}
	// The wrong profile for the target, and a custom image without its repository.
	s = h.staged("m2", registry.StdName("other"), "", im)
	h.wantRefused(s, "not the standard image")
	s = h.staged("m2", registry.CustomName("o", "r", "base"), "", im)
	h.wantRefused(s, "")
}

// R5.5, R5.7: missing or disagreeing labels, and a key that is not the key of the input.
func TestPromotionChecksLabelsAndKey(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	staging := registry.IncomingName("m1", target)
	tok := h.token("m1", push(staging)...)
	in := testInput("base")
	in.Pins = map[string]string{"runner": "1"}
	im := h.pushImage(tok, staging, "", in, func(l map[string]string) { delete(l, keys.LabelStackHash) })
	h.wantRefused(h.staged("m1", target, "", im), keys.LabelStackHash)

	in2 := testInput("base")
	in2.Pins = map[string]string{"runner": "2"}
	im = h.pushImage(tok, staging, "", in2, func(l map[string]string) { l[keys.LabelBaseDigest] = "sha256:" + strings.Repeat("2", 64) })
	h.wantRefused(h.staged("m1", target, "", im), keys.LabelBaseDigest)

	in3 := testInput("base")
	in3.Pins = map[string]string{"runner": "3"}
	im = h.pushImage(tok, staging, "", in3, func(l map[string]string) { l[keys.LabelLineage] = "o/r|base|amd64" })
	h.wantRefused(h.staged("m1", target, "", im), keys.LabelLineage)

	in4 := testInput("base")
	in4.Pins = map[string]string{"runner": "4"}
	im = h.pushImage(tok, staging, "", in4, nil)
	s := h.staged("m1", target, "", im)
	s.KeyInput.StackHash = strings.Repeat("b", 64) // the key is not the key of this input
	h.wantRefused(s, "not the key of the key input")
	s = h.staged("m1", target, "", im)
	s.Key = "zzzzzzzzzzzz"
	h.wantRefused(s, "12 hex")
	if len(h.ledger.done) != 0 {
		t.Fatal("something was promoted")
	}
	// The good one still promotes.
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", target, "", im)); err != nil {
		t.Fatal(err)
	}
}

// R5.48: nothing is promoted and nothing is pushed during the read-only window; pulls go on.
func TestReadOnly(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	staging := registry.IncomingName("m1", target)
	tok := h.token("m1", push(staging)...)
	im := h.pushImage(tok, staging, "", testInput("base"), nil)
	h.reg.SetReadOnly(true)
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", target, "", im)); !errors.Is(err, registry.ErrReadOnly) {
		t.Fatalf("err = %v", err)
	}
	if resp, _ := h.do(tok, http.MethodPost, "/v2/"+staging+"/blobs/uploads/", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("push in the window: %d", resp.StatusCode)
	}
	if resp, _ := h.do(tok, http.MethodGet, "/v2/"+staging+"/manifests/"+im.key.String(), nil, "Accept", "application/vnd.oci.image.manifest.v1+json"); resp.StatusCode != http.StatusOK {
		t.Fatalf("pull in the window: %d", resp.StatusCode)
	}
	h.reg.SetReadOnly(false)
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", target, "", im)); err != nil {
		t.Fatal(err)
	}
}

// R5.1: push, pull, delete and referrers through the mount, then GC frees the blobs.
func TestOCIRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	name := registry.IncomingName("m1", registry.StdName("base"))
	tok := h.token("m1", push(name)...)
	im := h.pushImage(tok, name, "", testInput("base"), nil)

	// pull (tags list too)
	resp, body := h.do(tok, http.MethodGet, "/v2/"+name+"/tags/list", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), im.key.String()) {
		t.Fatalf("tags: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(tok, http.MethodGet, "/v2/"+name+"/manifests/"+im.digest, nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, im.manifest) {
		t.Fatalf("pull: %d", resp.StatusCode)
	}

	// referrers: a signature-like manifest whose subject is the image
	artifact := []byte(`{"schemaVersion":2}`)
	artD := h.putBlob(tok, name, artifact)
	ref, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"artifactType": "application/spdx+json",
		"config":       map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "digest": artD, "size": len(artifact)},
		"layers":       []any{map[string]any{"mediaType": "application/spdx+json", "digest": artD, "size": len(artifact)}},
		"subject":      map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": im.digest, "size": len(im.manifest)},
		"annotations":  map[string]string{"org.opencontainers.image.created": "2026-11-19T10:00:00Z"},
	})
	resp, body = h.putManifest(tok, name, "sbom", ref)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("push referrer: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("OCI-Subject") != im.digest {
		t.Fatalf("OCI-Subject = %q", resp.Header.Get("OCI-Subject"))
	}
	resp, body = h.do(tok, http.MethodGet, "/v2/"+name+"/referrers/"+im.digest, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/vnd.oci.image.index.v1+json" {
		t.Fatalf("referrers: %d %s", resp.StatusCode, body)
	}
	var idx struct {
		SchemaVersion int
		Manifests     []struct {
			Digest       string
			Size         int64
			ArtifactType string
			Annotations  map[string]string
		}
	}
	if err := json.Unmarshal(body, &idx); err != nil || idx.SchemaVersion != 2 || len(idx.Manifests) != 1 {
		t.Fatalf("index = %s (%v)", body, err)
	}
	if g := idx.Manifests[0]; g.Digest != sum(ref) || g.Size != int64(len(ref)) || g.ArtifactType != "application/spdx+json" || g.Annotations["org.opencontainers.image.created"] == "" {
		t.Fatalf("descriptor = %+v", g)
	}
	// filter by artifact type, and a subject nobody refers to
	resp, body = h.do(tok, http.MethodGet, "/v2/"+name+"/referrers/"+im.digest+"?artifactType=application/vnd.other", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("OCI-Filters-Applied") != "artifactType" || !strings.Contains(string(body), `"manifests":[]`) {
		t.Fatalf("filtered referrers: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(tok, http.MethodGet, "/v2/"+name+"/referrers/"+sum([]byte("none")), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"manifests":[]`) {
		t.Fatalf("referrers of nothing: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(tok, http.MethodGet, "/v2/"+name+"/referrers/nonsense", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad digest: %d", resp.StatusCode)
	}
	// no one reads the referrers of a repository the token does not name
	if resp, _ := h.do(h.token("m2", pull("crsp/std/*")...), http.MethodGet, "/v2/"+name+"/referrers/"+im.digest, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("referrers across scopes: %d", resp.StatusCode)
	}

	// delete: the machine cannot, the controller can; blobs wait for GC
	if err := h.reg.DeleteManifest(ctx, name, sum(ref)); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.DeleteManifest(ctx, name, im.digest); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.DeleteManifest(ctx, name, im.digest); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if resp, _ := h.do(tok, http.MethodGet, "/v2/"+name+"/manifests/"+im.digest, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a deleted manifest answers %d", resp.StatusCode)
	}
	if resp, _ := h.do(tok, http.MethodGet, "/v2/"+name+"/manifests/"+im.key.String(), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a deleted manifest is still tagged: %d", resp.StatusCode)
	}
	if n := blobFiles(t, h.dir); n == 0 {
		t.Fatal("blobs went before GC")
	}
	dry, err := h.reg.GC(ctx, true)
	if err != nil || dry <= 0 {
		t.Fatalf("dry run = %d %v", dry, err)
	}
	if n := blobFiles(t, h.dir); n == 0 {
		t.Fatal("a dry run removed blobs")
	}
	freed, err := h.reg.GC(ctx, false)
	if err != nil || freed != dry {
		t.Fatalf("gc = %d (dry %d) %v", freed, dry, err)
	}
	if n := blobFiles(t, h.dir); n != 0 {
		t.Fatalf("%d blobs left after GC", n)
	}
}

// R5.1: an index (docker push with the containerd image store) promotes with its image and attestation children.
func TestPromoteIndex(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	name := registry.IncomingName("m1", target)
	tok := h.token("m1", push(name)...)
	in := testInput("base")
	key, _ := registry.ComputeKey(in)
	labels := keys.LabelsFor(key, in, registry.ProfileRef{ID: "base", BaseName: "ubuntu:24.04"}, "", "c0ffee")

	child := func(cfg map[string]any, layer []byte) ([]byte, string) {
		ld := h.putBlob(tok, name, layer)
		cb, _ := json.Marshal(cfg)
		cd := h.putBlob(tok, name, cb)
		m, _ := json.Marshal(map[string]any{
			"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
			"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": cd, "size": len(cb)},
			"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": ld, "size": len(layer)}},
		})
		resp, body := h.putManifest(tok, name, sum(m), m)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("child: %d %s", resp.StatusCode, body)
		}
		return m, sum(m)
	}
	img, imgD := child(map[string]any{"architecture": "amd64", "os": "linux", "config": map[string]any{"Labels": labels}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + strings.Repeat("3", 64)}}}, []byte("image layer"))
	att, attD := child(map[string]any{"architecture": "unknown", "os": "unknown", "config": map[string]any{}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + strings.Repeat("4", 64)}}}, []byte("attestation"))
	idx, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []any{
			map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": attD, "size": len(att), "platform": map[string]string{"architecture": "unknown", "os": "unknown"}, "annotations": map[string]string{"vnd.docker.reference.type": "attestation-manifest"}},
			map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": imgD, "size": len(img), "platform": map[string]string{"architecture": "amd64", "os": "linux"}},
		},
	})
	resp, body := h.do(tok, http.MethodPut, "/v2/"+name+"/manifests/"+key.String(), idx, "Content-Type", "application/vnd.oci.image.index.v1+json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("push index: %d %s", resp.StatusCode, body)
	}
	s := registry.Staged{MachineID: "m1", Name: name, Target: target, Key: key, Manifest: sum(idx), KeyInput: in}
	if _, err := h.prom.Promote(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	pullTok := h.token("m2", pull("crsp/std/*")...)
	resp, _ = h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+key.String(), nil, "Accept", "application/vnd.oci.image.index.v1+json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull the index: %d", resp.StatusCode)
	}
	resp, _ = h.do(pullTok, http.MethodGet, "/v2/crsp/std/base/manifests/"+imgD, nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull the child: %d", resp.StatusCode)
	}
}

func TestParseRoute(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	for path, want := range map[string]route{
		"/v2/crsp/std/base/manifests/abc":                    {"crsp/std/base", "manifests", "abc"},
		"/v2/crsp/std/base/blobs/" + d:                       {"crsp/std/base", "blobs", d},
		"/v2/crsp/std/base/blobs/uploads/":                   {"crsp/std/base", "uploads", ""},
		"/v2/crsp/std/base/blobs/uploads/u1":                 {"crsp/std/base", "uploads", "u1"},
		"/v2/crsp/std/base/tags/list":                        {"crsp/std/base", "tags", ""},
		"/v2/crsp/std/base/referrers/" + d:                   {"crsp/std/base", "referrers", d},
		"/v2/crsp/custom/o-r/blobs/manifests/blobs/uploads/": {"crsp/custom/o-r/blobs/manifests", "uploads", ""},
	} {
		got, ok := parseRoute(path)
		if !ok || got != want {
			t.Errorf("%s = %+v %v, want %+v", path, got, ok, want)
		}
	}
	for _, path := range []string{"/v2/_catalog", "/v2/crsp", "/v2/crsp/std/base", "/v2/crsp/std/base/manifests/", "/v2/crsp/_x/manifests/a", "/v2/manifests/a"} {
		if got, ok := parseRoute(path); ok {
			t.Errorf("%s parsed as %+v", path, got)
		}
	}
}

// The sink answers a token request with the scopes asked, never a token for others.
func TestSinkToken(t *testing.T) {
	iss := &Issuer{Secret: bytes.Repeat([]byte{5}, 32)}
	s := &Sink{Tokens: iss}
	ctx := context.Background()
	ok := s.RegistryToken(ctx, "m1", agentPush("crsp/incoming/m1/std/a"))
	if ok.GetToken() == "" || ok.GetRefused() != "" || ok.GetExpiresAtUnixMs() == 0 {
		t.Fatalf("answer = %+v", ok)
	}
	_, scopes, err := iss.Verify(ctx, ok.GetToken())
	if err != nil || !Allows(scopes, "crsp/incoming/m1/std/a", ActionPush) {
		t.Fatalf("scopes = %v %v", scopes, err)
	}
	no := s.RegistryToken(ctx, "m1", agentPush("crsp/incoming/m2/std/a"))
	if no.GetToken() != "" || no.GetRefused() == "" {
		t.Fatalf("answer = %+v", no)
	}
	if strings.Contains(no.GetRefused(), "m2") {
		t.Fatalf("the refusal leaks a name: %q", no.GetRefused())
	}
}

func agentPush(names ...string) *agentv1.RegistryTokenRequest {
	return &agentv1.RegistryTokenRequest{Action: agentv1.RegistryTokenRequest_ACTION_PUSH, Names: names}
}

type memRecorder struct {
	mu     sync.Mutex
	pushes []registry.PushRecord
	pulls  []registry.PullRecord
}

func (r *memRecorder) RecordPull(_ context.Context, p registry.PullRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pulls = append(r.pulls, p)
	return nil
}

func (r *memRecorder) RecordPush(_ context.Context, p registry.PushRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushes = append(r.pushes, p)
	return nil
}

type memOutcomes struct {
	promoted map[string]bool
	why      map[string]string
}

func (o *memOutcomes) SetPushOutcome(_ context.Context, id string, promoted bool, _ string, why string) error {
	o.promoted[id], o.why[id] = promoted, why
	return nil
}

type fakeLocator struct {
	ans registry.EnsureAnswer
	err error
}

func (l fakeLocator) Ensure(context.Context, string, registry.Key, string) (registry.EnsureAnswer, error) {
	return l.ans, l.err
}

// R5.5, R5.20: a PUSHED report promotes once; a resent one changes nothing; the machine is the one of the link.
func TestSinkPushReportPromotes(t *testing.T) {
	h := newHarness(t)
	target := registry.StdName("base")
	staging := registry.IncomingName("m1", target)
	im := h.pushImage(h.token("m1", push(staging)...), staging, "", testInput("base"), nil)
	rec, out := &memRecorder{}, &memOutcomes{promoted: map[string]bool{}, why: map[string]string{}}
	s := &Sink{Tokens: h.iss, Promoter: h.prom, Recorder: rec, Outcomes: out}
	in, _ := json.Marshal(im.input)
	rep := &agentv1.PushReport{
		PushId: "p1", Key: im.key.String(), Arch: "amd64", StagingName: staging, TargetName: target, ManifestDigest: im.digest,
		State: agentv1.PushReport_STATE_PUSHED, KeyInputJson: string(in), CommitSha: "c0ffee",
	}
	ctx := context.Background()
	s.PushReport(ctx, "m1", rep)
	s.PushReport(ctx, "m1", rep) // resent
	if len(rec.pushes) != 2 || rec.pushes[0].MachineID != "m1" || rec.pushes[0].State != "pushed" {
		t.Fatalf("pushes = %+v", rec.pushes)
	}
	if !out.promoted["p1"] || len(h.ledger.done) != 1 {
		t.Fatalf("outcome = %v, images = %d", out.promoted, len(h.ledger.done))
	}
	// A report of a push still going does not promote.
	rep2 := &agentv1.PushReport{PushId: "p2", State: agentv1.PushReport_STATE_PUSHING}
	s.PushReport(ctx, "m1", rep2)
	if _, ok := out.promoted["p2"]; ok {
		t.Fatal("a push in progress was promoted")
	}
	// m2 reports m1's staging name: refused, with the reason recorded.
	rep3 := &agentv1.PushReport{
		PushId: "p3", Key: im.key.String(), StagingName: staging, TargetName: target, ManifestDigest: im.digest,
		State: agentv1.PushReport_STATE_PUSHED, KeyInputJson: string(in),
	}
	s.PushReport(ctx, "m2", rep3)
	if out.promoted["p3"] || !strings.Contains(out.why["p3"], "staging root of machine m2") {
		t.Fatalf("p3 = %v %q", out.promoted["p3"], out.why["p3"])
	}
}

func TestSinkEnsureImage(t *testing.T) {
	ctx := context.Background()
	in, _ := json.Marshal(testInput("base"))
	key, _ := registry.ComputeKey(testInput("base"))
	req := &agentv1.EnsureImage{Key: key.String(), Arch: "amd64", KeyInputJson: string(in)}
	if (&Sink{}).EnsureImage(ctx, "m1", req) != nil {
		t.Fatal("no locator must leave the default answer")
	}
	s := &Sink{Host: "ctl.lan:8460", Locator: fakeLocator{ans: registry.EnsureAnswer{Found: true, Name: "crsp/std/base", Manifest: "sha256:" + strings.Repeat("a", 64), Size: 9}}}
	a := s.EnsureImage(ctx, "m1", req)
	if a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_FOUND || a.GetName() != "crsp/std/base" || a.GetRegistryHost() != "ctl.lan:8460" {
		t.Fatalf("found = %+v", a)
	}
	for _, l := range []fakeLocator{{ans: registry.EnsureAnswer{Busy: true}}, {err: errors.New("db")}} {
		s.Locator = l
		if a := s.EnsureImage(ctx, "m1", req); a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_BUSY || a.GetRetryAfterMs() == 0 {
			t.Fatalf("busy = %+v", a)
		}
	}
	s.Locator = fakeLocator{}
	if a := s.EnsureImage(ctx, "m1", req); a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND || a.GetStagingName() != "" {
		t.Fatalf("not found without a target = %+v", a)
	}
	s.Target = func(_ context.Context, _, profile string) (string, error) { return registry.StdName(profile), nil }
	a = s.EnsureImage(ctx, "m1", req)
	if a.GetStagingName() != "crsp/incoming/m1/std/base" || a.GetTargetName() != "crsp/std/base" || a.GetRegistryHost() != "ctl.lan:8460" {
		t.Fatalf("not found = %+v", a)
	}
	s.ReadOnly = func() bool { return true }
	if a := s.EnsureImage(ctx, "m1", req); a.GetStagingName() != "" {
		t.Fatalf("a staging name in the read-only window: %+v", a)
	}
}

func TestSinkPullReport(t *testing.T) {
	rec := &memRecorder{}
	s := &Sink{Recorder: rec}
	s.PullReport(context.Background(), "m1", &agentv1.PullReport{PullId: "x", Key: "aaaaaaaaaaaa", Source: agentv1.PullReport_SOURCE_PULLED, State: agentv1.PullReport_STATE_DONE, BytesDone: 5})
	s.PullReport(context.Background(), "m1", &agentv1.PullReport{PullId: "y"}) // no source: dropped
	if len(rec.pulls) != 1 || rec.pulls[0].MachineID != "m1" || rec.pulls[0].Source != registry.SourcePulled || rec.pulls[0].State != "done" {
		t.Fatalf("pulls = %+v", rec.pulls)
	}
}

// R5.8: a push token is refused when the disk is under the free floor or the kind is over budget.
func TestDiskGate(t *testing.T) {
	ctx := context.Background()
	disk := func(free int64) func(string) (runnercache.Disk, error) {
		return func(string) (runnercache.Disk, error) { return runnercache.Disk{Free: free, Total: 100}, nil }
	}
	var refused []registry.Kind
	g := &DiskGate{Disk: disk(50), Refused: func(_ context.Context, k registry.Kind) { refused = append(refused, k) }}
	if err := g.AllowPush(ctx, "crsp/incoming/m1/std/base"); err != nil {
		t.Fatal(err)
	}
	g.Disk = disk(5) // under the 10 percent floor
	err := g.AllowPush(ctx, "crsp/incoming/m1/custom/o-r/base")
	if !errors.Is(err, registry.ErrOverBudget) || len(refused) != 1 || refused[0] != registry.KindCustom {
		t.Fatalf("err = %v, refused = %v", err, refused)
	}
	g.Disk = disk(50)
	g.OverBudget = func(_ context.Context, k registry.Kind) (bool, error) { return k == registry.KindStandard, nil }
	if err := g.AllowPush(ctx, "crsp/incoming/m1/std/base"); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("over budget: %v", err)
	}
	if err := g.AllowPush(ctx, "crsp/incoming/m1/custom/o-r/base"); err != nil {
		t.Fatalf("custom has room: %v", err)
	}
	// the issuer asks the gate for a push and only for a push
	iss := &Issuer{Secret: bytes.Repeat([]byte{6}, 32), Gate: g}
	if _, err := iss.Issue(ctx, "m1", push("crsp/incoming/m1/std/base")); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("issue: %v", err)
	}
	if _, err := iss.Issue(ctx, "m1", pull("crsp/std/base")); err != nil {
		t.Fatalf("a pull needs no room: %v", err)
	}
	if free, err := (OSDisk{}).Free(t.TempDir()); err != nil || free <= 0 {
		t.Fatalf("free = %d %v", free, err)
	}
}

// R5.41, R5.50: a promotion the budget has no room for is refused, nothing is tagged, and the layers reach the check.
func TestPromoteRefusedOverBudget(t *testing.T) {
	h := newHarness(t)
	staging := registry.IncomingName("m1", registry.StdName("base"))
	im := h.pushImage(h.token("m1", push(staging)...), staging, "", testInput("base"), nil)
	var asked registry.Kind
	var seen int
	h.prom.Room = func(_ context.Context, kind registry.Kind, layers []registry.Layer) error {
		asked, seen = kind, len(layers)
		return errors.Join(registry.ErrOverBudget, errors.New("full"))
	}
	img := h.wantRefused(h.staged("m1", registry.StdName("base"), "", im), "full")
	if asked != registry.KindStandard || seen == 0 {
		t.Fatalf("room asked for %q with %d layers", asked, seen)
	}
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", registry.StdName("base"), "", im)); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget inside the refusal", err)
	}
	if h.ledger.refused[img.ID] == "" {
		t.Fatal("the refusal was not recorded")
	}
	h.prom.Room = func(context.Context, registry.Kind, []registry.Layer) error { return nil }
	if _, err := h.prom.Promote(context.Background(), h.staged("m1", registry.StdName("base"), "", im)); err != nil {
		t.Fatalf("with room the promotion failed: %v", err)
	}
}
