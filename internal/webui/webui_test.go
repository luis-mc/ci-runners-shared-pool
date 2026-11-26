package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

func TestEmbeddedPlaceholderServes(t *testing.T) {
	rec := get(Handler(), "/some/client/route")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CI Runners Shared Pool") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestSPAFallbackAndAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := handlerFor(fsys)

	if rec := get(h, "/runners/3"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("client route: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(h, "/assets/app.js"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("asset: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(h, "/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}

// web-console R6.14: a hashed asset is immutable, comes precompressed (br over gzip) with Vary and an
// ETag, and a repeat request with the ETag gets 304.
func TestHashedAssetIsPrecompressedAndImmutable(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":                  {Data: []byte("<html>app</html>")},
		"theme-init.js":               {Data: []byte("1")},
		"assets/index-E8YF35Z9.js":    {Data: []byte("plain")},
		"assets/index-E8YF35Z9.js.br": {Data: []byte("brotli")},
		"assets/index-E8YF35Z9.js.gz": {Data: []byte("gzip")},
	}
	h := handlerFor(fsys)
	do := func(target, accept, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	const asset = "/assets/index-E8YF35Z9.js"
	cases := []struct{ accept, encoding, body string }{
		{"gzip, deflate, br", "br", "brotli"},
		{"gzip", "gzip", "gzip"},
		{"br;q=0, gzip", "gzip", "gzip"},
		{"", "", "plain"},
		{"identity", "", "plain"},
	}
	for _, c := range cases {
		rec := do(asset, c.accept, "")
		if rec.Code != 200 || rec.Body.String() != c.body || rec.Header().Get("Content-Encoding") != c.encoding {
			t.Errorf("accept %q: %d %q encoding %q", c.accept, rec.Code, rec.Body.String(), rec.Header().Get("Content-Encoding"))
		}
		if got := rec.Header().Get("Cache-Control"); got != cacheImmutable {
			t.Errorf("accept %q: Cache-Control %q", c.accept, got)
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("ETag") == "" {
			t.Errorf("accept %q: Vary %q ETag %q", c.accept, rec.Header().Get("Vary"), rec.Header().Get("ETag"))
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("accept %q: Content-Type %q", c.accept, ct)
		}
	}
	first := do(asset, "br", "")
	if again := do(asset, "br", first.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Errorf("same ETag: %d", again.Code)
	}
	if first.Header().Get("ETag") == do(asset, "gzip", "").Header().Get("ETag") {
		t.Error("two encodings share one ETag")
	}
	// The copies are not addressable by their own name.
	if rec := do(asset+".br", "br", ""); rec.Code != http.StatusNotFound {
		t.Errorf("direct .br: %d", rec.Code)
	}
	// The shell and an unhashed file are revalidated, never immutable.
	for _, target := range []string{"/", "/runners/3", "/theme-init.js"} {
		rec := do(target, "br", "")
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != cacheRevalidate || rec.Header().Get("ETag") == "" {
			t.Errorf("%s: %d cache %q etag %q", target, rec.Code, rec.Header().Get("Cache-Control"), rec.Header().Get("ETag"))
		}
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: unexpected encoding", target)
		}
	}
}
