package webui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

type running struct {
	addr string
	pool *x509.CertPool
}

// serveWithCA starts the console as the controller does on a non-loopback
// address (the listener reports 10.0.0.5:8460): the certificate of the
// controller's own CA, TLS only.
func serveWithCA(t *testing.T) running {
	t.Helper()
	return serveOn(t, "10.0.0.5:8460")
}

// addrListener reports another address than the one it listens on, so a test
// can serve as if the console were on a non-loopback address.
type addrListener struct {
	net.Listener
	addr net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr }

// serveOn serves as the controller does. fakeAddr "" is the real loopback
// address; any other value makes the server treat the listener as bound there.
func serveOn(t *testing.T, fakeAddr string) running {
	t.Helper()
	ca, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New()})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	real := ln.Addr().String()
	if fakeAddr != "" {
		ta, err := net.ResolveTCPAddr("tcp", fakeAddr)
		if err != nil {
			t.Fatal(err)
		}
		ln = addrListener{Listener: ln, addr: ta}
	}
	mux := http.NewServeMux()
	mux.Handle("/", Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	srv := NewServer(real, ca.ServerTLS(), mux)
	done := make(chan error, 1)
	go func() { done <- Serve(srv, ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve: %v", err)
		}
	})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CAPEM()) {
		t.Fatal("CA PEM")
	}
	return running{addr: real, pool: pool}
}

// fetchURL makes one GET; the caller closes the body when the error is nil.
func fetchURL(c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func client(pool *x509.CertPool, minV, maxV uint16) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: minV, MaxVersion: maxV, ServerName: "localhost"},
	}}
}

// R5.1: the console answers over HTTPS, with the certificate the controller's
// CA made, and the security headers are on the answer.
func TestConsoleIsServedOverTLSWithTheControllerCA(t *testing.T) {
	r := serveWithCA(t)
	resp, err := fetchURL(client(r.pool, tls.VersionTLS13, tls.VersionTLS13), "https://"+r.addr+"/machines")
	if err != nil {
		t.Fatalf("a client that trusts the controller CA must get the console: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "CI Runners Shared Pool") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("TLS state %+v", resp.TLS)
	}
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("headers: %v", resp.Header)
	}
}

// R5.1: a client that does not trust the CA is refused, not served.
func TestConsoleRefusesAClientThatDoesNotTrustTheCA(t *testing.T) {
	r := serveWithCA(t)
	resp, err := fetchURL(client(x509.NewCertPool(), tls.VersionTLS13, tls.VersionTLS13), "https://"+r.addr+"/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the certificate must not verify against an empty trust store")
	}
}

// R5.1: TLS 1.3 only: an older protocol version is refused.
func TestConsoleRefusesOlderTLS(t *testing.T) {
	r := serveWithCA(t)
	resp, err := fetchURL(client(r.pool, tls.VersionTLS12, tls.VersionTLS12), "https://"+r.addr+"/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("TLS 1.2 must be refused")
	}
}

// R5.1: there is no plain-HTTP console. A plain request to the console port
// never gets the console or the API, and Go's TLS server answers it with its
// own refusal.
func TestNoPlainHTTPConsole(t *testing.T) {
	r := serveWithCA(t)
	for _, path := range []string{"/", "/healthz", "/api/v1/me"} {
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(context.Background(), "tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+r.addr+"\r\nConnection: close\r\n\r\n")
		got, _ := io.ReadAll(conn)
		_ = conn.Close()
		text := string(got)
		if strings.Contains(text, "CI Runners Shared Pool") || strings.Contains(text, "\r\n\r\nok") || strings.HasPrefix(text, "HTTP/1.1 200") {
			t.Errorf("plain HTTP %s was served: %q", path, text)
		}
	}
}

// R5.1: on a non-loopback address the server refuses to start without a
// certificate rather than fall back to plain HTTP.
func TestServeNeedsACertificateOffLoopback(t *testing.T) {
	real, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = real.Close() }()
	ta, _ := net.ResolveTCPAddr("tcp", "10.0.0.5:8460")
	ln := addrListener{Listener: real, addr: ta}
	for _, cfg := range []*tls.Config{nil, {MinVersion: tls.VersionTLS13}} {
		if err := Serve(NewServer(real.Addr().String(), cfg, http.NotFoundHandler()), ln); err == nil {
			t.Fatalf("config %+v: Serve must refuse to serve without a certificate", cfg)
		}
	}
}

// R5.1: a loopback listener (127.0.0.1, ::1) serves plain HTTP, with the R6.3
// headers, and a TLS client gets no console from it.
func TestLoopbackServesPlainHTTP(t *testing.T) {
	r := serveOn(t, "")
	resp, err := fetchURL(&http.Client{Timeout: 5 * time.Second}, "http://"+r.addr+"/healthz")
	if err != nil {
		t.Fatalf("plain HTTP on loopback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" || resp.TLS != nil {
		t.Fatalf("%d %q tls=%v", resp.StatusCode, body, resp.TLS)
	}
	for name, want := range SecurityHeaders {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// R5.1: a non-loopback address serves TLS only: a plain request is never
// answered with the console (see TestNoPlainHTTPConsole), a TLS one is.
func TestNonLoopbackServesTLSOnly(t *testing.T) {
	r := serveOn(t, "192.168.1.20:8460")
	if resp, err := fetchURL(&http.Client{Timeout: 3 * time.Second}, "http://"+r.addr+"/healthz"); err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == 200 || string(body) == "ok" {
			t.Fatalf("plain HTTP was served on a non-loopback address: %d %q", resp.StatusCode, body)
		}
	}
	resp, err := fetchURL(client(r.pool, tls.VersionTLS13, tls.VersionTLS13), "https://"+r.addr+"/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.TLS == nil {
		t.Fatalf("%d tls=%v", resp.StatusCode, resp.TLS)
	}
}

func TestIsLoopback(t *testing.T) { // R5.1: only 127.0.0.1 and ::1 are loopback
	for addr, want := range map[string]bool{
		"127.0.0.1:8460": true, "[::1]:8460": true, "127.0.0.1": true, "::1": true,
		"0.0.0.0:8460": false, "[::]:8460": false, ":8460": false, "localhost:8460": false,
		"192.168.1.20:8460": false, "example.com:443": false, "": false,
	} {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
