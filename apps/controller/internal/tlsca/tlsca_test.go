package tlsca_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

func newAuth(t *testing.T, sec secrets.Store, dir string, hosts ...string) *tlsca.Authority {
	t.Helper()
	a, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: dir, Secrets: sec, Hosts: hosts})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// R6.1: the server speaks TLS 1.3 only; a pinned client connects, TLS 1.2 is refused.
func TestTLS13OnlyAndPinnedClientConnects(t *testing.T) {
	a := newAuth(t, secretstest.New(), t.TempDir(), "pool.lan")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = a.ServerTLS()
	srv.StartTLS()
	defer srv.Close()

	c := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS(a.Fingerprint())}}
	resp, err := get(c, srv.URL)
	if err != nil {
		t.Fatalf("pinned client: %v", err)
	}
	_ = resp.Body.Close()
	if resp.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("version = %x, want TLS 1.3", resp.TLS.Version)
	}

	cfg := tlsca.ClientTLS(a.Fingerprint())
	cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	old := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: cfg}}
	if r, err := get(old, srv.URL); err == nil {
		_ = r.Body.Close()
		t.Fatal("a TLS 1.2 client was accepted")
	}
}

// R5.5, R6.1: a wrong fingerprint is refused, and so is an unknown certificate.
func TestWrongPinRefused(t *testing.T) {
	a := newAuth(t, secretstest.New(), t.TempDir())
	other := newAuth(t, secretstest.New(), t.TempDir())
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = a.ServerTLS()
	srv.StartTLS()
	defer srv.Close()
	c := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS(other.Fingerprint())}}
	if r, err := get(c, srv.URL); err == nil {
		_ = r.Body.Close()
		t.Fatal("connected with the wrong pin")
	}
	for _, pin := range []string{"", "abc", other.Fingerprint()} {
		err := tlsca.VerifyPin(pin, [][]byte{a.ServerCertificate().Raw}, time.Now())
		if !errors.Is(err, tlsca.ErrPinMismatch) {
			t.Fatalf("pin %q: err = %v, want ErrPinMismatch", pin, err)
		}
	}
}

// The pin follows the CA: a server certificate made again keeps the pin valid.
func TestServerCertificateRenewedKeepsPin(t *testing.T) {
	sec, dir := secretstest.New(), t.TempDir()
	a1 := newAuth(t, sec, dir, "a.lan")
	a2 := newAuth(t, sec, dir, "a.lan", "b.lan") // new host name: server certificate made again
	if a1.Fingerprint() != a2.Fingerprint() {
		t.Fatal("the CA changed")
	}
	if string(a1.ServerCertificate().Raw) == string(a2.ServerCertificate().Raw) {
		t.Fatal("server certificate was not made again for a new host")
	}
	if err := tlsca.VerifyPin(a1.Fingerprint(), [][]byte{a2.ServerCertificate().Raw, a2.ServerCertificate().Raw}, time.Now()); err == nil {
		t.Fatal("chain without the CA must not verify")
	}
	chain := a2.ServerTLS().Certificates[0].Certificate
	if err := tlsca.VerifyPin(a1.Fingerprint(), chain, time.Now()); err != nil {
		t.Fatal(err)
	}
	var pool = x509.NewCertPool()
	pool.AppendCertsFromPEM(a2.CAPEM())
	if _, err := a2.ServerCertificate().Verify(x509.VerifyOptions{Roots: pool, DNSName: "b.lan"}); err != nil {
		t.Fatalf("a browser that trusts the CA: %v", err)
	}
}

// R6.2: private keys only in the secret store; the folder holds public data.
func TestKeysOnlyInSecretStore(t *testing.T) {
	sec, dir := secretstest.New(), t.TempDir()
	newAuth(t, sec, dir)
	got := map[secrets.Key]bool{}
	for _, k := range sec.Keys() {
		got[k] = true
	}
	if !got[tlsca.CAKeySecret] || !got[tlsca.ServerKeySecret] {
		t.Fatalf("keys in store: %v", sec.Keys())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(b) == 0 || string(b[:27]) != "-----BEGIN CERTIFICATE-----" {
			t.Fatalf("%s is not a public certificate", e.Name())
		}
	}
}

// A missing CA key never makes a new CA silently: agents pinned the old one.
func TestCAKeyMissingIsAnError(t *testing.T) {
	sec, dir := secretstest.New(), t.TempDir()
	newAuth(t, sec, dir)
	_ = sec.Delete(context.Background(), tlsca.CAKeySecret)
	if _, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: dir, Secrets: sec}); err == nil {
		t.Fatal("expected an error")
	}
	// An unavailable store fails closed too.
	down := secretstest.New()
	down.Down = true
	if _, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: down}); !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func get(c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// operations R5.90: a listen address chosen in the console is in the server
// certificate (a certificate that lacks it is made again), and the CA, so the
// fingerprint CRSP_CA_FINGERPRINT carries, does not change.
func TestServerCertificateCoversTheChosenListenAddress(t *testing.T) {
	sec, dir := secretstest.New(), t.TempDir()
	a1 := newAuth(t, sec, dir, "pc.lan")
	a2 := newAuth(t, sec, dir, "pc.lan", "192.168.1.20")
	if a1.Fingerprint() != a2.Fingerprint() {
		t.Fatal("the CA fingerprint changed")
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(a2.CAPEM())
	if _, err := a1.ServerCertificate().Verify(x509.VerifyOptions{Roots: pool, DNSName: "192.168.1.20"}); err == nil {
		t.Fatal("the first certificate must not cover the address")
	}
	if _, err := a2.ServerCertificate().Verify(x509.VerifyOptions{Roots: pool, DNSName: "192.168.1.20"}); err != nil {
		t.Fatalf("the address is not covered: %v", err)
	}
	a3 := newAuth(t, sec, dir, "pc.lan", "192.168.1.20")
	if string(a3.ServerCertificate().Raw) != string(a2.ServerCertificate().Raw) {
		t.Fatal("a certificate that already covers the address must be kept")
	}
}
