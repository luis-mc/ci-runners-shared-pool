package webui

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// NewServer is the console's server (web-console R5.1): the security headers
// of R6.3 on every answer. How it listens depends on the address (see Serve):
// plain HTTP on loopback only, TLS on every other address, with the
// certificate of the controller's own CA (internal/tlsca, the same one the
// agent link uses).
//
// tlsConfig must carry the certificate and a TLS 1.3 minimum
// (tlsca.Authority.ServerTLS). It is not used on a loopback listener.
func NewServer(addr string, tlsConfig *tls.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           Secure(handler),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// IsLoopback reports whether addr (host:port or a bare host) is 127.0.0.1 or
// ::1, the only addresses that serve plain HTTP (web-console R5.1, github-
// integration R6.4). A name, a wildcard or an empty host is not loopback.
func IsLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(net.IPv4(127, 0, 0, 1)) || ip.Equal(net.IPv6loopback))
}

// Serve accepts connections on ln. A loopback listener (127.0.0.1, ::1) serves
// plain HTTP; any other listener serves TLS only and refuses to start without
// a certificate. It returns when the server is shut down.
func Serve(srv *http.Server, ln net.Listener) error {
	if IsLoopback(ln.Addr().String()) {
		return srv.Serve(ln)
	}
	if srv.TLSConfig == nil || len(srv.TLSConfig.Certificates) == 0 && srv.TLSConfig.GetCertificate == nil {
		return errors.New("webui: a console address that is not loopback is served over TLS and needs a certificate")
	}
	// The certificate is in TLSConfig, so the file names stay empty.
	return srv.ServeTLS(ln, "", "")
}

// Listen opens the console's TCP listener.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
}

// ServerErrorLog routes the server's own error lines to logger. A browser that
// refuses the self-signed certificate ("TLS handshake error ... unknown
// certificate") is routine and logs at DEBUG; every other line is a WARN (OPS17).
func ServerErrorLog(logger *slog.Logger) *log.Logger {
	return log.New(errorLogWriter{logger}, "", 0)
}

type errorLogWriter struct{ l *slog.Logger }

func (w errorLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if strings.Contains(msg, "TLS handshake error") {
		w.l.Debug("console TLS handshake refused", "detail", msg)
	} else {
		w.l.Warn("console server", "detail", msg)
	}
	return len(p), nil
}
