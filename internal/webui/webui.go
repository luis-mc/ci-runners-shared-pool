// Package webui serves the web console, a React single-page app built to static
// files. The build copies apps/web/dist into ./dist (task web:embed). Only
// dist/placeholder.html is committed, so `go build` works without Node.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves the embedded web app from the embedded dist directory.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // dist is part of the embed; its absence is a build error.
	}
	return handlerFor(sub)
}

// hashedAsset matches a build output the bundler named by its content (assets/index-E8YF35Z9.js): the
// name changes whenever the bytes do, so the browser may keep it for good (design-system P-5).
var hashedAsset = regexp.MustCompile(`^assets/[^/]+-[A-Za-z0-9_-]{8}\.[a-z0-9]+$`)

const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
)

// precompressed lists the encodings the build writes next to an asset, best first (web-console R6.14).
var precompressed = []struct{ token, ext string }{{"br", ".br"}, {"gzip", ".gz"}}

// acceptsEncoding reports whether an Accept-Encoding header lists token with a non-zero quality.
func acceptsEncoding(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), token) {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

func handlerFor(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() && !isCompressedCopy(root, name) {
				serveFile(w, r, root, name)
				return
			}
			// A path with a file extension that does not exist is a missing asset.
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		// Anything else is a client-side route: serve the app shell.
		page := "index.html"
		if _, err := fs.Stat(root, page); err != nil {
			page = "placeholder.html"
		}
		serveFile(w, r, root, page)
	})
}

// isCompressedCopy is true for app.js.br and app.js.gz when app.js exists: they are served only as an
// encoding of app.js, never by their own name.
func isCompressedCopy(root fs.FS, name string) bool {
	for _, e := range precompressed {
		if base, ok := strings.CutSuffix(name, e.ext); ok {
			if _, err := fs.Stat(root, base); err == nil {
				return true
			}
		}
	}
	return false
}

// serveFile answers with name, or with its precompressed copy when the browser accepts one (Content-Encoding,
// Vary), a strong ETag (If-None-Match gets 304) and a Cache-Control: immutable for a hashed asset, a
// revalidation for everything else, so the shell and theme-init.js are never stale.
func serveFile(w http.ResponseWriter, r *http.Request, root fs.FS, name string) {
	source, encoding := name, ""
	if name != "index.html" && name != "placeholder.html" {
		accept := r.Header.Get("Accept-Encoding")
		for _, e := range precompressed {
			if _, err := fs.Stat(root, name+e.ext); err == nil && acceptsEncoding(accept, e.token) {
				source, encoding = name+e.ext, e.token
				break
			}
		}
		w.Header().Add("Vary", "Accept-Encoding")
	}
	data, err := fs.ReadFile(root, source)
	if err != nil {
		if name == "placeholder.html" {
			http.Error(w, "web console is not built into this binary", http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256(data)
	h := w.Header()
	h.Set("ETag", `"`+hex.EncodeToString(sum[:12])+`"`)
	if encoding != "" {
		h.Set("Content-Encoding", encoding)
	}
	if hashedAsset.MatchString(name) {
		h.Set("Cache-Control", cacheImmutable)
	} else {
		h.Set("Cache-Control", cacheRevalidate)
	}
	if path.Ext(name) == ".html" {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	// name, not source, picks the Content-Type; the zero time sends no Last-Modified.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
