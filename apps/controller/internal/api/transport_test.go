package api

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveWith(h http.HandlerFunc, target string, hdr map[string]string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	transportMW(h).ServeHTTP(rec, req)
	return rec
}

func jsonOf(n int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"v":"`+strings.Repeat("a", n)+`"}`)
	}
}

func TestTransportCompressesBigJSONOnly(t *testing.T) {
	rec := serveWith(jsonOf(5000), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip, br"})
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if len(body) != 5000+8 {
		t.Errorf("body %d bytes", len(body))
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Error("no content length")
	}
	if small := serveWith(jsonOf(100), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip"}); small.Header().Get("Content-Encoding") != "" {
		t.Error("a small answer was compressed")
	}
	if plain := serveWith(jsonOf(5000), "/api/v1/x", nil); plain.Header().Get("Content-Encoding") != "" || plain.Body.Len() != 5008 {
		t.Error("compressed for a caller that did not ask")
	}
	if no := serveWith(jsonOf(5000), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip;q=0"}); no.Header().Get("Content-Encoding") != "" {
		t.Error("compressed against q=0")
	}
}

func TestTransportETagAnswers304(t *testing.T) {
	first := serveWith(jsonOf(2000), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip"})
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no etag")
	}
	again := serveWith(jsonOf(2000), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": tag})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("ETag") != tag {
		t.Errorf("304 expected: %d %q %v", again.Code, again.Body, again.Header())
	}
	other := serveWith(jsonOf(2001), "/api/v1/x", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": tag})
	if other.Code != http.StatusOK {
		t.Errorf("changed answer: %d", other.Code)
	}
	// The identity answer has its own tag, so a cache never mixes the two.
	plain := serveWith(jsonOf(2000), "/api/v1/x", nil)
	if plain.Header().Get("ETag") == tag || plain.Header().Get("ETag") == "" {
		t.Errorf("tags %q %q", plain.Header().Get("ETag"), tag)
	}
	if weak := serveWith(jsonOf(2000), "/api/v1/x", map[string]string{"If-None-Match": `W/` + plain.Header().Get("ETag")}); weak.Code != http.StatusNotModified {
		t.Errorf("weak match: %d", weak.Code)
	}
}

func TestTransportLeavesStreamsAndErrorsAlone(t *testing.T) {
	sse := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", 3000)+"\n\n")
		w.(http.Flusher).Flush()
	}
	rec := serveWith(sse, "/api/v1/events", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("ETag") != "" || !rec.Flushed {
		t.Errorf("stream changed: %v flushed %v", rec.Header(), rec.Flushed)
	}
	export := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="x.json"`)
		_, _ = io.WriteString(w, strings.Repeat("y", 3000))
	}
	if rec = serveWith(export, "/api/v1/exports/jobs", map[string]string{"Accept-Encoding": "gzip"}); rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("ETag") != "" {
		t.Errorf("download changed: %v", rec.Header())
	}
	bad := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, strings.Repeat("z", 3000))
	}
	if rec = serveWith(bad, "/api/v1/x", map[string]string{"Accept-Encoding": "gzip"}); rec.Code != http.StatusNotFound || rec.Header().Get("ETag") != "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("error answer: %d %v", rec.Code, rec.Header())
	}
	if rec = serveWith(jsonOf(5000), "/healthz", map[string]string{"Accept-Encoding": "gzip"}); rec.Header().Get("Content-Encoding") != "" {
		t.Error("a path outside /api/ was compressed")
	}
}
