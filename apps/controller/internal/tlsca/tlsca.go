// Package tlsca is the controller's own certificate authority (decision 0004;
// machines R5.5, R5.8, R6.1, R6.2).
//
// At first run the controller makes a CA and a server certificate signed by it.
// Private keys live only in the OS secret store (internal/secrets); the two
// certificates are public and sit in a folder. The console (:8460) and the
// agent link (:8461) both serve the server certificate over TLS 1.3 only.
//
// Pin: the fingerprint in the install command is the SHA-256 of the CA
// certificate. The server sends the chain (server certificate, then CA), so
// the server certificate can be made again (new host names, expiry) without
// breaking agents that already pinned the CA. An agent checks that the chain
// holds a certificate with the pinned fingerprint and that the server
// certificate is signed by it. It never checks a host name: on a local network
// the pin is the identity.
package tlsca

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Secret store keys of the two private keys (never a file, never the database).
const (
	CAKeySecret     secrets.Key = "crsp/controller/ca-key"
	ServerKeySecret secrets.Key = "crsp/controller/server-key"
)

const (
	caFile     = "ca.pem"
	serverFile = "server.pem"
	caLife     = 10 * 365 * 24 * time.Hour
	serverLife = 365 * 24 * time.Hour
	// renewBefore: a server certificate this close to its end is made again.
	renewBefore = 30 * 24 * time.Hour
)

// ErrPinMismatch is returned when the controller's certificate is not the one
// pinned (machines R5.5). The agent stops and says so.
var ErrPinMismatch = errors.New("the controller certificate does not match the pinned fingerprint")

// Authority is the controller's CA and server certificate.
type Authority struct {
	caCert     *x509.Certificate
	caPEM      []byte
	server     tls.Certificate
	serverCert *x509.Certificate
	fp         string
}

// Options say where the certificates sit and what the server certificate covers.
type Options struct {
	// Dir holds ca.pem and server.pem (public data).
	Dir string
	// Secrets holds the private keys.
	Secrets secrets.Store
	// Hosts are the DNS names and IP addresses the server certificate covers.
	Hosts []string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// LoadOrCreate loads the CA and server certificate, making what is missing.
// The server certificate is made again when it is near its end or when Hosts
// changed. The CA is never replaced silently: a CA certificate whose key is
// missing from the secret store is an error, because every agent pinned it.
func LoadOrCreate(ctx context.Context, o Options) (*Authority, error) {
	if o.Secrets == nil {
		return nil, errors.New("tlsca: a secret store is required")
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil { //nolint:gosec // public certificates only
		return nil, fmt.Errorf("tlsca: %w", err)
	}
	caKey, caCert, caPEM, err := loadOrMakeCA(ctx, o, now())
	if err != nil {
		return nil, err
	}
	a := &Authority{caCert: caCert, caPEM: caPEM, fp: Fingerprint(caCert.Raw)}
	if err := a.loadOrMakeServer(ctx, o, caKey, now()); err != nil {
		return nil, err
	}
	return a, nil
}

func loadOrMakeCA(ctx context.Context, o Options, now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, []byte, error) {
	path := filepath.Join(o.Dir, caFile)
	keyDER, kerr := o.Secrets.Get(ctx, CAKeySecret)
	pemBytes, perr := os.ReadFile(path) //nolint:gosec // path built from the configured dir
	switch {
	case kerr == nil && perr == nil:
		key, err := x509.ParseECPrivateKey(keyDER)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tlsca: CA key is not readable: %w", err)
		}
		cert, err := parseCertPEM(pemBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tlsca: ca.pem: %w", err)
		}
		return key, cert, pemBytes, nil
	case kerr != nil && !errors.Is(kerr, secrets.ErrNotFound):
		return nil, nil, nil, fmt.Errorf("tlsca: %w", kerr)
	case kerr == nil && perr != nil, kerr != nil && perr == nil:
		// One half is gone. Agents pinned the old CA; never replace it silently.
		return nil, nil, nil, errors.New("tlsca: the CA certificate and its key do not match up (one is missing); restore it from a backup or remove both and join every agent again")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "CRSP controller CA", Organization: []string{"CI Runners Shared Pool"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLife),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tlsca: make CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := o.Secrets.Put(ctx, CAKeySecret, keyBytes); err != nil {
		return nil, nil, nil, fmt.Errorf("tlsca: keep CA key: %w", err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, p, 0o644); err != nil { //nolint:gosec // public certificate
		return nil, nil, nil, fmt.Errorf("tlsca: %w", err)
	}
	return key, cert, p, nil
}

func (a *Authority) loadOrMakeServer(ctx context.Context, o Options, caKey *ecdsa.PrivateKey, now time.Time) error {
	path := filepath.Join(o.Dir, serverFile)
	hosts := normalizeHosts(o.Hosts)
	if keyDER, err := o.Secrets.Get(ctx, ServerKeySecret); err == nil {
		if pemBytes, rerr := os.ReadFile(path); rerr == nil { //nolint:gosec // configured dir
			if cert, perr := parseCertPEM(pemBytes); perr == nil && cert.CheckSignatureFrom(a.caCert) == nil &&
				now.Add(renewBefore).Before(cert.NotAfter) && sameHosts(cert, hosts) {
				if key, kerr := x509.ParseECPrivateKey(keyDER); kerr == nil {
					return a.setServer(cert, key)
				}
			}
		}
	} else if !errors.Is(err, secrets.ErrNotFound) {
		return fmt.Errorf("tlsca: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "CRSP controller", Organization: []string{"CI Runners Shared Pool"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("tlsca: make server certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := o.Secrets.Put(ctx, ServerKeySecret, keyBytes); err != nil {
		return fmt.Errorf("tlsca: keep server key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil { //nolint:gosec // public
		return fmt.Errorf("tlsca: %w", err)
	}
	return a.setServer(cert, key)
}

func (a *Authority) setServer(cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	a.serverCert = cert
	a.server = tls.Certificate{Certificate: [][]byte{cert.Raw, a.caCert.Raw}, PrivateKey: key, Leaf: cert}
	return nil
}

// Fingerprint returns the SHA-256 of a DER certificate, as lower-case hex.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Fingerprint is what the install command carries and the agent pins.
func (a *Authority) Fingerprint() string { return a.fp }

// CAPEM is the CA certificate for the one-click download (public data).
func (a *Authority) CAPEM() []byte { return append([]byte(nil), a.caPEM...) }

// ServerCertificate is the certificate the console and the agent link serve.
func (a *Authority) ServerCertificate() *x509.Certificate { return a.serverCert }

// ServerTLS is the server config for :8460 and :8461: TLS 1.3 only (R6.1).
func (a *Authority) ServerTLS() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{a.server},
		NextProtos:   []string{"h2", "http/1.1"},
	}
}

// ClientTLS is the agent's config: TLS 1.3 only, no host name check, and the
// chain must hold a certificate with the pinned fingerprint (R5.5, R5.8, R6.1).
func ClientTLS(pinSHA256 string) *tls.Config {
	pin := strings.ToLower(strings.TrimSpace(pinSHA256))
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2"},
		// The default check is replaced by VerifyPeerCertificate below: the pin
		// is the identity, not a host name or the system roots.
		InsecureSkipVerify:    true, //nolint:gosec // see VerifyPin
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error { return VerifyPin(pin, raw, time.Now()) },
	}
}

// VerifyPin checks a presented chain (DER, server certificate first) against
// the pinned CA fingerprint: some certificate in the chain has that
// fingerprint, and the server certificate is that certificate or is signed by
// it, and is inside its validity dates.
func VerifyPin(pin string, chain [][]byte, now time.Time) error {
	if len(pin) != sha256.Size*2 || len(chain) == 0 {
		return ErrPinMismatch
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return ErrPinMismatch
	}
	var anchor *x509.Certificate
	for _, der := range chain {
		if Fingerprint(der) == pin {
			if anchor, err = x509.ParseCertificate(der); err != nil {
				return ErrPinMismatch
			}
			break
		}
	}
	if anchor == nil {
		return ErrPinMismatch
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return errors.New("the controller certificate is outside its validity dates")
	}
	if bytes.Equal(leaf.Raw, anchor.Raw) {
		return nil
	}
	if !anchor.IsCA {
		return ErrPinMismatch
	}
	if err := leaf.CheckSignatureFrom(anchor); err != nil {
		return ErrPinMismatch
	}
	return nil
}

func parseCertPEM(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate found")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		panic(err) // the system random source failed; nothing safe can continue
	}
	return n
}

func normalizeHosts(in []string) []string {
	seen := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range in {
		if h = strings.TrimSpace(h); h != "" {
			seen[strings.ToLower(h)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func sameHosts(c *x509.Certificate, hosts []string) bool {
	have := map[string]bool{}
	for _, d := range c.DNSNames {
		have[strings.ToLower(d)] = true
	}
	for _, ip := range c.IPAddresses {
		have[ip.String()] = true
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			h = ip.String()
		}
		if !have[h] {
			return false
		}
	}
	return true
}
