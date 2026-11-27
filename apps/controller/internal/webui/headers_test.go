package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// directives splits a CSP into name -> sources.
func directives(csp string) map[string][]string {
	out := map[string][]string{}
	for _, d := range strings.Split(csp, ";") {
		f := strings.Fields(d)
		if len(f) > 0 {
			out[f[0]] = f[1:]
		}
	}
	return out
}

// R6.3: the policy is default-src 'self', frame-ancestors 'none', and has no
// way to run an inline script, evaluate a string, or reach another host.
func TestContentSecurityPolicyShape(t *testing.T) {
	d := directives(ContentSecurityPolicy)
	if got := d["default-src"]; len(got) != 1 || got[0] != "'self'" {
		t.Fatalf("default-src = %v", got)
	}
	if got := d["frame-ancestors"]; len(got) != 1 || got[0] != "'none'" {
		t.Fatalf("frame-ancestors = %v", got)
	}
	for name, sources := range d {
		for _, s := range sources {
			switch {
			case s == "'unsafe-inline'", s == "'unsafe-eval'", s == "*", s == "https:", s == "http:", s == "blob:",
				strings.Contains(s, "://"), strings.HasPrefix(s, "'nonce-"), strings.HasPrefix(s, "'sha"):
				t.Errorf("%s allows %s", name, s)
			}
		}
	}
	for _, name := range []string{"script-src", "style-src", "img-src", "font-src", "connect-src"} {
		if len(d[name]) == 0 || d[name][0] != "'self'" {
			t.Errorf("%s must start with 'self': %v", name, d[name])
		}
	}
	if got := d["img-src"]; len(got) != 2 || got[1] != "data:" {
		t.Errorf("img-src should allow only 'self' and data: (no avatar host): %v", got)
	}
}

// R6.3: every answer carries the headers: the app, a deep link, an asset, a
// missing asset, a refused method, and an API error.
func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	root := fstest.MapFS{
		"index.html":    {Data: []byte("<html><script src=\"/assets/app.js\"></script></html>")},
		"assets/app.js": {Data: []byte("1")},
	}
	api := http.NewServeMux()
	api.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusNotFound) })
	api.Handle("/", handlerFor(root))
	h := Secure(api)
	cases := []struct{ method, path string }{
		{"GET", "/"}, {"GET", "/machines/m1"}, {"GET", "/assets/app.js"}, {"GET", "/assets/missing.js"},
		{"POST", "/"}, {"GET", "/api/v1/nothing"}, {"HEAD", "/"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), c.method, c.path, nil))
		for name, want := range SecurityHeaders {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s %s: %s = %q, want %q", c.method, c.path, name, got, want)
			}
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") ||
			!strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s %s: CSP %q", c.method, c.path, rec.Header().Get("Content-Security-Policy"))
		}
	}
}

var (
	externalURL = regexp.MustCompile(`(?i)(src|href|action|content|url\()\s*=?\s*["']?(https?:)?//[^"')\s]+`)
	inlineHook  = regexp.MustCompile(`(?i)\son[a-z]+\s*=|\sstyle\s*=|javascript:`)
	scriptTag   = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
)

// R6.3: the page the controller serves loads nothing from outside and holds no
// inline script, no event attribute and no style attribute. The embedded page
// is checked here (the placeholder in a plain checkout, the built console in a
// release build).
func TestServedPageLoadsNothingFromOutside(t *testing.T) {
	rec := httptest.NewRecorder()
	Secure(Handler()).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	page := rec.Body.String()
	if rec.Code != 200 || page == "" {
		t.Fatalf("status %d", rec.Code)
	}
	if m := externalURL.FindString(page); m != "" {
		t.Errorf("the page reaches another host: %q", m)
	}
	if m := inlineHook.FindString(page); m != "" {
		t.Errorf("the page has an inline hook: %q", m)
	}
	for _, m := range scriptTag.FindAllStringSubmatch(page, -1) {
		if !strings.Contains(m[1], "src=") || strings.TrimSpace(m[2]) != "" {
			t.Errorf("inline script: %q", m[0])
		}
	}
}
