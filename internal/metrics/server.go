package metrics

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// NewToken makes a random token of 32 hex characters.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Handler serves /metrics. A token of "" means no token is asked for.
func Handler(src Source, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="crsp metrics"`)
				http.Error(w, "a token is needed", http.StatusUnauthorized)
				return
			}
		}
		snap, err := src.Snapshot(r.Context())
		if err != nil {
			http.Error(w, "the pool numbers are not available", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		WritePrometheus(w, snap)
	})
	return mux
}

// Server runs the /metrics listener.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Start listens on addr and serves in the background. It returns the address in use.
func Start(addr string, h http.Handler) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for metrics on %s: %w", addr, err)
	}
	s := &Server{ln: ln, srv: &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Addr is the address in use.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops the listener.
func (s *Server) Close(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
