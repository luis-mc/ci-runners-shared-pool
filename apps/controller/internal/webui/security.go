package webui

import "net/http"

// ContentSecurityPolicy is sent with every answer of the controller (web-console
// R6.3). The console loads nothing from outside: no CDN, font host, analytics
// or avatar image. default-src 'self' covers script, style, image, font, media
// and frames; no inline script and no eval; the page cannot be framed.
//
// connect-src 'self' keeps fetch and the event stream on the controller. A form
// may still post to GitHub (the App manifest flow), so form-action is left to
// default, which does not cover it.
const ContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// SecurityHeaders are set on every answer: the CSP of R6.3 and the usual
// hardening headers. The map is read-only; Secure copies it per request.
var SecurityHeaders = map[string]string{
	"Content-Security-Policy":      ContentSecurityPolicy,
	"Strict-Transport-Security":    "max-age=15552000",
	"X-Content-Type-Options":       "nosniff",
	"X-Frame-Options":              "DENY",
	"Referrer-Policy":              "no-referrer",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-origin",
	"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=()",
}

// Secure sends the SecurityHeaders on every answer of next: the console, the
// API and the errors between them.
func Secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for k, v := range SecurityHeaders {
			h.Set(k, v)
		}
		next.ServeHTTP(w, r)
	})
}
