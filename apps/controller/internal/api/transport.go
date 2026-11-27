package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// The transport of the API answers (CRSP290, audit F5): every GET answer gets `Cache-Control: no-cache` (a browser
// may keep a copy but asks again), a JSON answer over gzipMin bytes is compressed when the caller accepts gzip, and
// a GET JSON answer carries an ETag, so a caller that sends If-None-Match gets a 304 with no body when nothing
// changed. Streams (the event stream, exports, file downloads) are not JSON bodies and pass through untouched.

const (
	gzipMin = 1024     // answers of this size or smaller go out as they are
	bufMax  = 16 << 20 // an answer larger than this is no longer held for an ETag and goes out as it is
)

var gzipPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return w }}

// transportMW wraps the generated routes. Only GET and HEAD under /api/ are changed.
func transportMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		tw := &transportWriter{ResponseWriter: w, r: r}
		defer tw.finish()
		next.ServeHTTP(tw, r)
	})
}

type writeMode int

const (
	modeUndecided writeMode = iota
	modeBuffer              // a JSON answer held back to be compressed and tagged
	modePass                // anything else goes straight through
)

type transportWriter struct {
	http.ResponseWriter
	r      *http.Request
	mode   writeMode
	status int
	buf    bytes.Buffer
}

// Unwrap lets http.ResponseController reach the real writer (deadlines, flushing).
func (t *transportWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func (t *transportWriter) decide(status int) {
	h := t.Header()
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-cache")
	}
	t.status = status
	if status == http.StatusOK && h.Get("Content-Encoding") == "" && h.Get("Content-Range") == "" && h.Get("Content-Disposition") == "" &&
		strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.mode = modeBuffer
		return
	}
	t.mode = modePass
	t.ResponseWriter.WriteHeader(status)
}

func (t *transportWriter) WriteHeader(status int) {
	if t.mode == modeUndecided {
		t.decide(status)
	}
}

func (t *transportWriter) Write(p []byte) (int, error) {
	if t.mode == modeUndecided {
		t.decide(http.StatusOK)
	}
	if t.mode == modePass {
		return t.ResponseWriter.Write(p)
	}
	if t.buf.Len()+len(p) > bufMax {
		t.release()
		return t.ResponseWriter.Write(p)
	}
	return t.buf.Write(p)
}

// Flush sends what is held as it is, then flushes: a caller that flushes is streaming.
func (t *transportWriter) Flush() {
	if t.mode == modeUndecided {
		t.decide(http.StatusOK)
	}
	if t.mode == modeBuffer {
		t.release()
	}
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// release stops holding the answer: the head and what was held go out unchanged.
func (t *transportWriter) release() {
	t.mode = modePass
	t.ResponseWriter.WriteHeader(t.status)
	_, _ = t.ResponseWriter.Write(t.buf.Bytes())
	t.buf.Reset()
}

// finish sends a held answer: 304 when the caller has it, else the body, compressed when it is worth it.
func (t *transportWriter) finish() {
	if t.mode == modeUndecided {
		t.decide(http.StatusOK)
	}
	if t.mode != modeBuffer {
		return
	}
	h := t.Header()
	body := t.buf.Bytes()
	sum := sha256.Sum256(body)
	tag := hex.EncodeToString(sum[:12])
	gz := len(body) > gzipMin && acceptsGzip(t.r)
	etag := `"` + tag + `"`
	if gz {
		etag = `"` + tag + `-gzip"`
	}
	h.Add("Vary", "Accept-Encoding")
	h.Set("ETag", etag)
	if noneMatch(t.r.Header.Get("If-None-Match"), etag) {
		h.Del("Content-Length")
		h.Del("Content-Type")
		t.ResponseWriter.WriteHeader(http.StatusNotModified)
		return
	}
	if gz {
		var out bytes.Buffer
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(&out)
		_, _ = zw.Write(body)
		_ = zw.Close()
		gzipPool.Put(zw)
		body = out.Bytes()
		h.Set("Content-Encoding", "gzip")
	}
	h.Del("Content-Length")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	t.ResponseWriter.WriteHeader(t.status)
	_, _ = t.ResponseWriter.Write(body)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "q=")) != "0"
		}
	}
	return false
}

// noneMatch is the If-None-Match test: any listed tag equal to etag (weak or strong), or "*".
func noneMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimPrefix(strings.TrimSpace(c), "W/")
		if c == "*" || c == etag {
			return true
		}
	}
	return false
}
