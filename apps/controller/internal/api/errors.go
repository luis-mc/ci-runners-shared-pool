package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
)

var scrubError atomic.Pointer[func(string) string]

// SetErrorRedactor puts the process redactor in front of every error body
// (observability R6.5). Nil removes it.
func SetErrorRedactor(f func(string) string) {
	if f == nil {
		scrubError.Store(nil)
		return
	}
	scrubError.Store(&f)
}

// Stable error codes of the envelope {"error":{"code":"...","message":"..."}}.
// The list is the one in packages/contract/openapi.yaml (info.description).
const (
	CodeBadRequest        = "bad_request"
	CodeUnauthenticated   = "unauthenticated"
	CodeSessionExpired    = "session_expired"
	CodeForbidden         = "forbidden"
	CodeNotFound          = "not_found"
	CodeConflict          = "conflict"
	CodeRateLimited       = "rate_limited"
	CodeNeedsConfirmation = "needs_confirmation"
	CodeGitHubUnavailable = "github_unavailable"
	CodeNotImplemented    = "not_implemented"
	CodeInternal          = "internal"
	CodeUnavailable       = "unavailable"
)

// ErrorEnvelope is the one JSON error shape every API failure uses.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the inside of the envelope.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError writes the error envelope. message must be safe to show a client:
// never a secret, a path or a raw internal error.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	if f := scrubError.Load(); f != nil {
		message = (*f)(message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(ErrorEnvelope{Error: ErrorBody{Code: code, Message: message}}); err != nil {
		slog.Default().Warn("write error envelope", "err", err)
	}
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	WriteError(w, http.StatusNotFound, CodeNotFound, "no such API route")
}
