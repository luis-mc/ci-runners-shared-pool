package container

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

func newCA(t *testing.T, cn string) (pemData []byte, fp string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), hex.EncodeToString(sum[:])
}

// R5.12: before the first pull the engine is made to trust the controller CA for the registry host, once.
func TestEnsureInstallsTheEngineCAOnce(t *testing.T) {
	ca, _ := newCA(t, "controller")
	var asked []string
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil }}
	clk := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	er := &ensureRig{ctl: ctl}
	er.rig = newRig(t, func(c *Config) {
		c.Controller = ctl
		c.Now = func() time.Time { clk = clk.Add(3 * time.Second); return clk }
		c.HostOS = "linux"
		c.ControllerCA = func(_ context.Context, host string) ([]byte, error) { asked = append(asked, host); return ca, nil }
	})
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := er.b.d.FS.ReadFile("/etc/docker/certs.d/ctl.lan:8443/ca.crt")
	if err != nil || string(got) != string(ca) {
		t.Fatalf("the CA was not installed: %v", err)
	}
	if len(asked) != 1 || asked[0] != "ctl.lan:8443" {
		t.Fatalf("asked = %v", asked)
	}
	if !strings.Contains(er.logText(), "now trusts the controller's certificate for ctl.lan:8443") {
		t.Fatalf("no log line:\n%s", er.logText())
	}
	// a second pull does not fetch it again
	er.b.ensureEngineCA(context.Background(), "ctl.lan:8443")
	if len(asked) != 1 {
		t.Fatalf("asked again: %v", asked)
	}
}

// A CA that cannot be got or written is said in plain words and the pull goes on.
func TestEngineCAFailureDoesNotStopThePull(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil }}
	clk := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	er := &ensureRig{ctl: ctl}
	er.rig = newRig(t, func(c *Config) {
		c.Controller = ctl
		c.Now = func() time.Time { clk = clk.Add(3 * time.Second); return clk }
		c.HostOS = "linux"
		c.ControllerCA = func(context.Context, string) ([]byte, error) { return nil, errors.New("connection refused") }
	})
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(er.logText(), "could not be made to trust the controller's certificate for ctl.lan:8443: get the controller CA: connection refused") {
		t.Fatalf("log:\n%s", er.logText())
	}
	if !strings.Contains(er.f.text(), "pull ctl.lan:8443/crsp/std/node-web@") {
		t.Fatal("no pull")
	}
}

// No ControllerCA: nothing is written.
func TestNoControllerCAWritesNothing(t *testing.T) {
	r := newRig(t)
	r.b.ensureEngineCA(context.Background(), "ctl.lan:8443")
	if r.b.d.FS.Exists("/etc/docker") {
		t.Fatal("wrote a folder")
	}
}

// The download keeps only the certificate with the pinned fingerprint.
func TestFetchControllerCAChecksThePin(t *testing.T) {
	ca, fp := newCA(t, "controller")
	other, _ := newCA(t, "someone else")
	serve := func(body []byte, status int) string {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/ca.pem" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}))
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "https://")
	}
	ctx := context.Background()
	got, err := FetchControllerCA(fp, nil)(ctx, serve(append(append([]byte{}, other...), ca...), http.StatusOK))
	if err != nil || string(got) != string(ca) {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := FetchControllerCA(fp, nil)(ctx, serve(other, http.StatusOK)); err == nil {
		t.Fatal("a CA with another fingerprint was accepted")
	}
	if _, err := FetchControllerCA(fp, nil)(ctx, serve(ca, http.StatusInternalServerError)); err == nil {
		t.Fatal("an error page was accepted")
	}
	if _, err := FetchControllerCA("", nil)(ctx, serve(ca, http.StatusOK)); err == nil {
		t.Fatal("no pin was accepted")
	}
	if _, err := FetchControllerCA(strings.ToUpper(fp), nil)(ctx, serve(ca, http.StatusOK)); err != nil {
		t.Fatalf("an upper case pin: %v", err)
	}
}
