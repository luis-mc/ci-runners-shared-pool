package machines_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/recovery"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

func recoveryEnv(t *testing.T) (*env, string, string) {
	t.Helper()
	e := newEnv(t, func(o *machines.Options) { o.MinProtocol = 2 })
	e.Svc.SetHooks(machines.Hooks{ControllerVersion: "1.2.0"})
	r, _ := e.joined("old-box")
	return e, r.GetMachineId(), r.GetMachineCredential()
}

func hello(version string, p agentv1.ProtocolVersion) *agentv1.Hello {
	return &agentv1.Hello{AgentVersion: version, ProtocolVersion: p}
}

// R5.44: an agent below the minimum protocol is refused on normal calls and
// shown as needing recovery; a current agent is not.
func TestOldAgentIsRefusedAndNeedsRecovery(t *testing.T) {
	e, id, key := recoveryEnv(t)
	_, err := e.session(id, key, hello("1.0.0", agentv1.ProtocolVersion_PROTOCOL_VERSION_1))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("old agent session: %v", err)
	}
	v, err := e.Svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	r := v.Recovery
	if r == nil || r.State != "needs_recovery" || r.Reason != machines.ReasonOldProtocol || r.Has != "1.0.0" || r.Needs != "1.2.0" || r.Armed {
		t.Fatalf("recovery = %+v", r)
	}
	// The agent updated: it speaks protocol 2 and the state is gone.
	fa, err := e.session(id, key, hello("1.2.0", agentv1.ProtocolVersion_PROTOCOL_VERSION_2))
	if err != nil {
		t.Fatal(err)
	}
	defer fa.Close()
	if v, _ = e.Svc.Get(context.Background(), id); v.Recovery != nil {
		t.Fatalf("recovery after update = %+v", v.Recovery)
	}
}

// R5.44: an agent asked to update that stays old for an hour needs recovery.
func TestStuckAfterAnHour(t *testing.T) {
	e, id, key := recoveryEnv(t)
	fa, err := e.session(id, key, hello("1.0.0", agentv1.ProtocolVersion_PROTOCOL_VERSION_2))
	if err != nil {
		t.Fatal(err)
	}
	defer fa.Close()
	if err := e.Svc.AskUpdate(id, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	e.Clk.Add(59 * time.Minute)
	if v, _ := e.Svc.Get(context.Background(), id); v.Recovery != nil {
		t.Fatalf("marked after 59 minutes: %+v", v.Recovery)
	}
	e.Clk.Add(2 * time.Minute)
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Recovery == nil || v.Recovery.Reason != machines.ReasonStuck {
		t.Fatalf("not marked after an hour: %+v", v.Recovery)
	}
}

func (e *env) recoveryServer() string {
	e.T.Helper()
	h := &recovery.Handler{
		Authenticate: func(ctx context.Context, key, id string) (string, error) {
			m, err := e.Svc.Authenticate(ctx, key, id)
			return m.ID, err
		},
		Armed:  func(ctx context.Context, id, _ string) bool { return e.Svc.RecoveryArmed(ctx, id) },
		Signer: e.CA,
		Target: func(context.Context, recovery.Request) (recovery.Target, error) {
			return recovery.Target{Version: "1.2.0", Asset: "crsp-linux-x64.tar.gz", SHA256: strings.Repeat("ab", 32)}, nil
		},
		Served: func(ctx context.Context, id string, t recovery.Target) { e.Svc.RecoveryServed(ctx, id, t.Version) },
	}
	e.Svc.SetRecoveryHandler(h)
	ln, err := (&net.ListenConfig{}).Listen(e.T.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		e.T.Fatal(err)
	}
	srv := machines.NewLinkServer(ln.Addr().String(), e.CA, e.Svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, e.CA.ServerTLS())) }()
	e.T.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func (e *env) recoveryClient(addr, id, key, pin string) *recovery.Client {
	return &recovery.Client{
		HTTP:       &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS(e.CA.Fingerprint())}},
		Controller: addr, Pin: pin, MachineID: id, Key: key,
	}
}

// R5.44: the recovery call hands out a target only after the owner pressed the
// action, only to a machine that needs recovery, and only with a signature
// that chains to the agent's pin.
func TestRecoveryCallNeedsActionAndValidSignature(t *testing.T) {
	e, id, key := recoveryEnv(t)
	addr := e.recoveryServer()
	ctx := context.Background()
	if _, err := e.session(id, key, hello("1.0.0", agentv1.ProtocolVersion_PROTOCOL_VERSION_1)); err == nil {
		t.Fatal("old agent was accepted")
	}
	c := e.recoveryClient(addr, id, key, e.CA.Fingerprint())
	// Not armed: nothing is served.
	if _, err := c.Poll(ctx, "1.0.0", "linux", "x64"); !errors.Is(err, recovery.ErrNoUpdate) {
		t.Fatalf("unarmed poll: %v", err)
	}
	if err := e.Svc.ArmRecovery(ctx, "owner", id); err != nil {
		t.Fatal(err)
	}
	tg, err := c.Poll(ctx, "1.0.0", "linux", "x64")
	if err != nil || tg.Version != "1.2.0" || tg.Asset != "crsp-linux-x64.tar.gz" {
		t.Fatalf("armed poll: %+v %v", tg, err)
	}
	// Served once: the action is spent.
	if _, err := c.Poll(ctx, "1.0.0", "linux", "x64"); !errors.Is(err, recovery.ErrNoUpdate) {
		t.Fatalf("second poll: %v", err)
	}
	// A pin that is not the controller's refuses the signature.
	_ = e.Svc.ArmRecovery(ctx, "owner", id)
	bad := e.recoveryClient(addr, id, key, strings.Repeat("0", 64))
	if _, err := bad.Poll(ctx, "1.0.0", "linux", "x64"); !errors.Is(err, recovery.ErrBadTarget) {
		t.Fatalf("wrong pin: %v", err)
	}
	// A wrong key is refused by the call itself.
	wrong := e.recoveryClient(addr, id, "not-the-key", e.CA.Fingerprint())
	if _, err := wrong.Poll(ctx, "1.0.0", "linux", "x64"); !errors.Is(err, recovery.ErrRefused) {
		t.Fatalf("wrong key: %v", err)
	}
}

// R5.44: a machine that does not need recovery cannot be armed.
func TestHealthyMachineCannotBeArmed(t *testing.T) {
	e, id, _ := recoveryEnv(t)
	if err := e.Svc.ArmRecovery(context.Background(), "owner", id); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("arm healthy: %v", err)
	}
	if e.Svc.RecoveryArmed(context.Background(), id) {
		t.Fatal("healthy machine is armed")
	}
}

// R5.19: HelloAck carries protocol version 3 only when the controller answers
// the registry calls; an agent asks only on that.
func TestHelloAckVersionFollowsRegistry(t *testing.T) {
	for _, reg := range []bool{false, true} {
		e := newEnv(t)
		e.Svc.SetHooks(machines.Hooks{ControllerVersion: "1.2.0", Registry: reg})
		r, _ := e.joined("box")
		fa, err := e.session(r.GetMachineId(), r.GetMachineCredential(), hello("1.2.0", agentv1.ProtocolVersion_PROTOCOL_VERSION_3))
		if err != nil {
			t.Fatal(err)
		}
		got := fa.Ack.GetProtocolVersion() >= agentv1.ProtocolVersion_PROTOCOL_VERSION_3
		fa.Close()
		if got != reg {
			t.Fatalf("registry=%v: ack version %v", reg, fa.Ack.GetProtocolVersion())
		}
	}
}
