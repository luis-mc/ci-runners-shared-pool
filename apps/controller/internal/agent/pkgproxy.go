package agent

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches/toolcache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// The package proxy at runner start (caches R5.76): the agent asks the hub,
// and when it answers points the runner's npm and pip at it. A hub that does
// not answer leaves the runner on the real registries.

// hubAccess reaches the hub: Up asks GET /pkg/ping (with the 30 s skip of a
// failed hub), Trust writes the controller CA and the combined bundle where
// runner users can read them and returns their paths (bundle "" when the host
// has no public bundle file).
type hubAccess interface {
	Up(ctx context.Context, base string) bool
	Trust(ctx context.Context) (caFile, bundleFile string, err error)
}

// packageEnv is the runner's package proxy environment, or nil.
func (l *link) packageEnv(ctx context.Context, spec backend.RunnerSpec) map[string]string {
	if !spec.PackageProxy.On() || spec.Container || l.cfg.Controller == "" {
		return nil
	}
	h := l.hub
	if h == nil {
		h = realHubFor(l.cfg.Controller, l.pin)
	}
	base := "https://" + l.cfg.Controller
	if !h.Up(ctx, base) {
		return nil
	}
	ca, bundle, err := h.Trust(ctx)
	if err != nil {
		return nil
	}
	return lancache.RunnerEnv(base, spec.PackageProxy, ca, bundle)
}

var (
	hubsMu sync.Mutex
	hubs   = map[string]*realHub{}
)

// realHubFor keeps one probe per controller and pin, so the 30 s skip holds
// across runner adds.
func realHubFor(controller, pin string) *realHub {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	k := controller + "|" + pin
	if h := hubs[k]; h != nil {
		return h
	}
	h := &realHub{controller: controller, pin: pin, dir: hubTrustDir(runtime.GOOS),
		probe: &lancache.Probe{HTTP: pinnedClient(pin)}}
	hubs[k] = h
	return h
}

type realHub struct {
	controller, pin, dir string
	probe                *lancache.Probe
}

func (h *realHub) Up(ctx context.Context, base string) bool { return h.probe.Up(ctx, base) }

func (h *realHub) Trust(ctx context.Context) (string, string, error) {
	pemCA, err := controllerCA(ctx, h.controller, h.pin)
	if err != nil {
		return "", "", err
	}
	return writeHubTrust(h.dir, pemCA, systemBundles(runtime.GOOS))
}

// hubTrustDir is a folder every runner user can read: next to the shared tool
// cache (caches R5.65), never inside the agent's private data folder.
func hubTrustDir(goos string) string {
	return filepath.Join(filepath.Dir(toolcache.DefaultRoot(goos)), "hub-ca")
}

// systemBundles are the public CA bundle files of a host, first found wins.
// Windows keeps its roots in the system store, so it has none.
func systemBundles(goos string) []string {
	switch goos {
	case "linux":
		return []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem", "/etc/ssl/cert.pem"}
	case "darwin":
		return []string{"/etc/ssl/cert.pem"}
	}
	return nil
}

// controllerCA reads the pinned CA certificate from the controller's TLS chain.
func controllerCA(ctx context.Context, controller, pin string) ([]byte, error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: tlsca.ClientTLS(pin)}
	c, err := d.DialContext(ctx, "tcp", controller)
	if err != nil {
		return nil, fmt.Errorf("reach the controller: %w", err)
	}
	defer func() { _ = c.Close() }()
	tc, ok := c.(*tls.Conn)
	if !ok {
		return nil, errors.New("not a TLS connection")
	}
	for _, cert := range tc.ConnectionState().PeerCertificates {
		if tlsca.Fingerprint(cert.Raw) == pin {
			return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), nil
		}
	}
	return nil, errors.New("the controller did not present its CA")
}

// writeHubTrust writes controller-ca.pem and, when a public bundle exists,
// combined.pem (the bundle, then the CA) into dir, readable by every user.
// The CA is public data.
func writeHubTrust(dir string, pemCA []byte, bundles []string) (string, string, error) {
	if len(pemCA) == 0 {
		return "", "", errors.New("no controller CA")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // runner users read the public CA here
		return "", "", fmt.Errorf("make the hub CA folder: %w", err)
	}
	ca := filepath.Join(dir, "controller-ca.pem")
	if err := writeReadable(ca, pemCA); err != nil {
		return "", "", err
	}
	for _, b := range bundles {
		pub, err := os.ReadFile(b)
		if err != nil || len(pub) == 0 {
			continue
		}
		all := append(append(append([]byte(nil), pub...), '\n'), pemCA...)
		combined := filepath.Join(dir, "combined.pem")
		if err := writeReadable(combined, all); err != nil {
			return "", "", err
		}
		return ca, combined, nil
	}
	return ca, "", nil
}

func writeReadable(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // public CA, read by runner users
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
