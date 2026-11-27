package machines_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1/agentv1connect"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.8: every connection after join proves the machine's key; the controller
// refuses an unknown or revoked key with CodeUnauthenticated (the agent stops
// retrying on it, R5.39).
func TestSessionRefusesUnknownRevokedAndMismatchedKeys(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	ok, err := e.session(r.GetMachineId(), r.GetMachineCredential(), nil)
	if err != nil {
		t.Fatalf("good key: %v", err)
	}
	ok.Close()

	for name, tc := range map[string]struct{ id, key string }{
		"unknown key":   {r.GetMachineId(), "crsp_mk_not-a-key"},
		"no key":        {r.GetMachineId(), ""},
		"wrong machine": {"m_other", r.GetMachineCredential()},
	} {
		_, err := e.session(tc.id, tc.key, nil)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v (%v), want unauthenticated", name, connect.CodeOf(err), err)
		}
	}
	if _, err := e.Svc.Remove(context.Background(), "owner", r.GetMachineId(), true); err != nil {
		t.Fatal(err)
	}
	_, err = e.session(r.GetMachineId(), r.GetMachineCredential(), nil)
	if connect.CodeOf(err) != connect.CodeUnauthenticated || !strings.Contains(err.Error(), "removed from the pool") {
		t.Fatalf("revoked key: %v", err)
	}
}

// R6.1: the link is TLS 1.3 only; a client without the pinned certificate or
// below 1.3 is refused before any call.
func TestLinkRefusesTLS12AndUnpinnedClients(t *testing.T) {
	e := newEnv(t)
	call := func(cfg *tls.Config) error {
		hc := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: cfg}, Timeout: 5 * time.Second}
		_, err := agentv1connect.NewAgentServiceClient(hc, "https://"+e.Addr, connect.WithGRPC()).
			Join(context.Background(), connect.NewRequest(&agentv1.JoinRequest{}))
		return err
	}
	// 1.2 only
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, NextProtos: []string{"h2"}} //nolint:gosec // the test checks the server refuses
	if err := call(cfg); err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("TLS 1.2: err = %v, want a protocol version failure", err)
	}
	// system roots only: the controller's CA is not trusted
	if err := call(&tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}}); err == nil {
		t.Fatalf("unpinned: err = %v, want a certificate failure", err)
	}
}

func TestAuthenticateChecksKeyAndMachine(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	m, err := e.Svc.Authenticate(context.Background(), r.GetMachineCredential(), r.GetMachineId())
	if err != nil || m.ID != r.GetMachineId() {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := e.Svc.Authenticate(context.Background(), r.GetMachineCredential(), "m_other"); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("wrong id: %v", err)
	}
}
