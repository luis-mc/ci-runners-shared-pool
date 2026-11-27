package lancache_test

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
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

type auth struct{}

func (auth) Authenticate(_ context.Context, cred, id string) error {
	if id == "m1" && cred == "good" {
		return nil
	}
	return errors.New("unknown machine")
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func setup(t *testing.T, capBytes int64) (*lancache.Store, *httptest.Server, *lancache.Client) {
	t.Helper()
	lastDir = t.TempDir()
	st, err := lancache.Open(lastDir, capBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(nil)
	t.Cleanup(ts.Close)
	srv, err := lancache.NewServer(st, auth{}, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv.Handler()
	return st, ts, lancache.NewClient(ts.URL, "m1", "good", ts.Client(), nil)
}

func TestToolCacheHitAndMiss(t *testing.T) {
	_, _, c := setup(t, 1<<20)
	ctx := context.Background()
	data := []byte("tool bytes")
	if _, err := c.Fetch(ctx, lancache.NSTools, "pool", "just/1.0/just.tgz", digest(data)); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("want miss, got %v", err)
	}
	if err := c.Store(ctx, lancache.NSTools, "pool", "just/1.0/just.tgz", data); err != nil {
		t.Fatal(err)
	}
	got, err := c.Fetch(ctx, lancache.NSTools, "pool", "just/1.0/just.tgz", digest(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("hit failed: %v %q", err, got)
	}
}

func TestDigestMismatchRefused(t *testing.T) {
	st, ts, c := setup(t, 1<<20)
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, ts.URL+"/lan/v1/tools/pool/a/1/f", strings.NewReader("abc"))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set(lancache.HeaderMachineID, "m1")
	req.Header.Set(lancache.HeaderDigest, digest([]byte("other")))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if st.Size() != 0 {
		t.Fatal("refused bytes were stored")
	}
	// A holder that holds bytes the pin does not match is a miss for the reader.
	if err := c.Store(ctx, lancache.NSTools, "pool", "a/1/f", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, lancache.NSTools, "pool", "a/1/f", digest([]byte("pinned"))); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("want miss on wrong pin, got %v", err)
	}
}

func TestChangedBlobBecomesMiss(t *testing.T) {
	st, _, c := setup(t, 1<<20)
	ctx := context.Background()
	data := []byte("good bytes")
	_ = st
	if err := c.Store(ctx, lancache.NSPackages, "pool", "npm/x", data); err != nil {
		t.Fatal(err)
	}
	blobs, _ := filepath.Glob(filepath.Join(lastDir, "blobs", digest(data)))
	if len(blobs) != 1 {
		t.Fatal("blob not found")
	}
	if err := os.WriteFile(blobs[0], []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, lancache.NSPackages, "pool", "npm/x", digest(data)); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("want miss, got %v", err)
	}
	if _, err := os.Stat(blobs[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tampered blob was kept")
	}
}

var lastDir string

func TestEvictionAtCap(t *testing.T) {
	clock := time.Unix(1000, 0)
	now := func() time.Time { clock = clock.Add(time.Second); return clock }
	dir := t.TempDir()
	st, err := lancache.Open(dir, 25, now)
	if err != nil {
		t.Fatal(err)
	}
	put := func(n, body string) {
		if _, err := st.Put(n, "", strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	put("packages/p/a", strings.Repeat("a", 10))
	put("packages/p/b", strings.Repeat("b", 10))
	f, _, err := st.Get("packages/p/a") // a is now newer than b
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	put("packages/p/c", strings.Repeat("c", 10))
	if _, err := st.Lookup("packages/p/b"); !errors.Is(err, lancache.ErrMiss) {
		t.Fatal("least recently used entry b was kept")
	}
	for _, n := range []string{"packages/p/a", "packages/p/c"} {
		if _, err := st.Lookup(n); err != nil {
			t.Fatalf("%s lost: %v", n, err)
		}
	}
	if st.Size() > 25 {
		t.Fatalf("over cap: %d", st.Size())
	}
	if _, err := st.Put("packages/p/big", "", strings.NewReader(strings.Repeat("z", 26))); !errors.Is(err, lancache.ErrTooLarge) {
		t.Fatalf("want too large, got %v", err)
	}
}

func TestUnknownMachineRefused(t *testing.T) {
	st, ts, _ := setup(t, 1<<20)
	ctx := context.Background()
	for _, c := range []*lancache.Client{
		lancache.NewClient(ts.URL, "m1", "bad", ts.Client(), nil),
		lancache.NewClient(ts.URL, "stranger", "good", ts.Client(), nil),
	} {
		if err := c.Store(ctx, lancache.NSTools, "pool", "a/1/f", []byte("x")); err == nil {
			t.Fatal("unknown machine could write")
		}
	}
	if st.Size() != 0 {
		t.Fatal("bytes stored for an unknown machine")
	}
	good := lancache.NewClient(ts.URL, "m1", "good", ts.Client(), nil)
	if err := good.Store(ctx, lancache.NSTools, "pool", "a/1/f", []byte("x")); err != nil {
		t.Fatal(err)
	}
	bad := lancache.NewClient(ts.URL, "m1", "bad", ts.Client(), nil)
	if _, err := bad.Fetch(ctx, lancache.NSTools, "pool", "a/1/f", ""); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("unknown machine could read: %v", err)
	}
}

func TestFallbackWhenDown(t *testing.T) {
	_, ts, _ := setup(t, 1<<20)
	c := lancache.NewClient(ts.URL, "m1", "good", ts.Client(), nil)
	ts.Close()
	calls := 0
	fb := func(context.Context) ([]byte, error) { calls++; return []byte("fresh"), nil }
	got, lan, err := lancache.FetchOrFallback(context.Background(), c, lancache.NSTools, "pool", "a/1/f", "", fb)
	if err != nil || lan || string(got) != "fresh" || calls != 1 {
		t.Fatalf("fallback: %v %v %q %d", err, lan, got, calls)
	}
	// A nil client (feature off) takes the same path.
	got, lan, err = lancache.FetchOrFallback(context.Background(), nil, lancache.NSTools, "pool", "a/1/f", "", fb)
	if err != nil || lan || string(got) != "fresh" {
		t.Fatalf("off: %v %v %q", err, lan, got)
	}
}

func TestKeysNeverCrossRepositories(t *testing.T) {
	_, ts, c := setup(t, 1<<20)
	ctx := context.Background()
	if err := c.Store(ctx, lancache.NSActions, "repo1", "k/v", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, lancache.NSActions, "repo2", "k/v", ""); !errors.Is(err, lancache.ErrMiss) {
		t.Fatalf("repo2 read repo1: %v", err)
	}
	for _, bad := range []string{"../x", "a/../b", ""} {
		if _, err := lancache.Name(lancache.NSTools, "s", bad); err == nil {
			t.Fatalf("name %q accepted", bad)
		}
	}
	if _, err := lancache.Name(lancache.NSTools, "..", "x"); err == nil {
		t.Fatal("scope .. accepted")
	}
	_ = ts
}

func twirp(t *testing.T, ts *httptest.Server, method string, body map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/"+method, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set(lancache.HeaderMachineID, "m1")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestActionsCacheTwirpRoundTrip(t *testing.T) {
	_, ts, _ := setup(t, 1<<20)
	meta := map[string]any{"repository_id": "42"}
	create := map[string]any{"key": "npm-abc", "version": "v1", "metadata": meta}
	out := twirp(t, ts, "CreateCacheEntry", create)
	up, _ := out["signed_upload_url"].(string)
	if out["ok"] != true || up == "" {
		t.Fatalf("create: %v", out)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, up, strings.NewReader("archive"))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	if out := twirp(t, ts, "FinalizeCacheEntryUpload", map[string]any{"key": "npm-abc", "version": "v1", "size_bytes": 7, "metadata": meta}); out["ok"] != true {
		t.Fatalf("finalize: %v", out)
	}
	if out := twirp(t, ts, "CreateCacheEntry", create); out["ok"] != false {
		t.Fatalf("second create must refuse: %v", out)
	}
	out = twirp(t, ts, "GetCacheEntryDownloadURL", map[string]any{"key": "npm-zzz", "version": "v1", "restore_keys": []string{"npm-"}, "metadata": meta})
	dl, _ := out["signed_download_url"].(string)
	if out["ok"] != true || out["matched_key"] != "npm-abc" || dl == "" {
		t.Fatalf("restore: %v", out)
	}
	r2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, dl, nil)
	resp, err = ts.Client().Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "archive" {
		t.Fatalf("download %q", body)
	}
	other := twirp(t, ts, "GetCacheEntryDownloadURL", map[string]any{"key": "npm-abc", "version": "v1", "metadata": map[string]any{"repository_id": "43"}})
	if other["ok"] != false {
		t.Fatalf("other repository got the entry: %v", other)
	}
}

func TestListenRefusesWildcardAndPublic(t *testing.T) {
	for _, a := range []string{"0.0.0.0:8470", "[::]:8470", ":8470", "8.8.8.8:8470", "example.com:8470"} {
		if err := lancache.CheckLANAddr(a); !errors.Is(err, lancache.ErrNotLAN) {
			t.Fatalf("%s accepted: %v", a, err)
		}
	}
	for _, a := range []string{"192.168.1.20:8470", "10.0.0.5:1", "127.0.0.1:0", "[fd00::1]:9"} {
		if err := lancache.CheckLANAddr(a); err != nil {
			t.Fatalf("%s refused: %v", a, err)
		}
	}
}
