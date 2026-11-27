package container

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/regcreds"
)

// The engine CA host step (registry R5.12). Before the first pull or push to a
// registry host the engine is made to trust the controller's CA for it, through
// the regcreds CA mechanism: /etc/docker/certs.d and /etc/containers/certs.d on
// Linux, the Docker Desktop folder of the user on macOS and Windows. The CA is
// public data. The step is done once per host and CA; a machine with no
// ControllerCA keeps the trust it has.

// caFetchBound bounds the download of the CA.
const caFetchBound = 30 * time.Second

// maxCAResponse bounds the CA response body.
const maxCAResponse = 64 << 10

// ErrEngineCAUnsupported is the reason the step does not run on a route that
// reaches the engine in a WSL distro: the trust folder is inside the distro.
var ErrEngineCAUnsupported = errors.New("the engine runs inside WSL2, so its CA folder cannot be written from here; install the controller CA in the distro")

// ensureEngineCA makes the engine trust the controller CA for host. A failure
// is logged in plain words and never stops the pull: the engine's own TLS
// error follows if the trust is still missing.
func (b *Backend) ensureEngineCA(ctx context.Context, host string) {
	if b.cfg.ControllerCA == nil || host == "" {
		return
	}
	b.mu.Lock()
	if b.caDone == nil {
		b.caDone = map[string]bool{}
	}
	done := b.caDone[host]
	b.mu.Unlock()
	if done {
		return
	}
	if err := b.installEngineCA(ctx, host); err != nil {
		b.log("prepare image", "The engine could not be made to trust the controller's certificate for "+host+": "+b.redact(err.Error()))
		return
	}
	b.mu.Lock()
	b.caDone[host] = true
	b.mu.Unlock()
}

func (b *Backend) installEngineCA(ctx context.Context, host string) error {
	if st, err := b.eng.Detect(ctx); err == nil && st.Route == RouteWSL {
		return ErrEngineCAUnsupported
	}
	ca, err := b.cfg.ControllerCA(ctx, host)
	if err != nil {
		return fmt.Errorf("get the controller CA: %w", err)
	}
	goos, home := b.cfg.HostOS, b.cfg.HomeDir
	if goos == "" {
		goos = runtime.GOOS
	}
	if home == "" && goos != "linux" {
		home, _ = os.UserHomeDir()
	}
	res, err := regcreds.InstallEngineCA(b.d.FS, regcreds.EngineCAOptions{GOOS: goos, Home: home, Host: host, CA: ca})
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w (the agent must run as root or an administrator to install it)", err)
		}
		return err
	}
	if len(res.Written) > 0 {
		b.log("prepare image", "The engine now trusts the controller's certificate for "+host)
	}
	return nil
}

// FetchControllerCA returns a Config.ControllerCA that downloads the CA from
// https://<host>/api/v1/ca.pem and keeps only the certificate whose SHA-256
// equals pin, the fingerprint of the controller CA the agent was installed
// with. The download needs no trust in the connection because nothing but a
// certificate with the pinned fingerprint is accepted; a CA of anyone else is
// refused. client may be nil.
func FetchControllerCA(pin string, client *http.Client) func(ctx context.Context, host string) ([]byte, error) {
	pin = strings.ToLower(strings.TrimSpace(pin))
	return func(ctx context.Context, host string) ([]byte, error) {
		if len(pin) != 64 {
			return nil, errors.New("the agent has no controller CA fingerprint to check against")
		}
		c := client
		if c == nil {
			c = &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}, //nolint:gosec // only a certificate with the pinned fingerprint is accepted below
			}}
		}
		ctx, cancel := context.WithTimeout(ctx, caFetchBound)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/api/v1/ca.pem", nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the controller answered %d for its CA", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxCAResponse))
		if err != nil {
			return nil, err
		}
		return pickPinned(body, pin)
	}
}

// pickPinned returns the PEM of the certificate of body whose SHA-256 is pin.
func pickPinned(body []byte, pin string) ([]byte, error) {
	for rest := body; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil, errors.New("the CA the controller gave does not match the pinned fingerprint")
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
			continue
		}
		sum := sha256.Sum256(blk.Bytes)
		if hex.EncodeToString(sum[:]) == pin {
			return pem.EncodeToMemory(blk), nil
		}
	}
}
