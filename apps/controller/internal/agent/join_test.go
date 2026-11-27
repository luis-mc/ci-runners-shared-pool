package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// R5.5: at join the agent checks the controller certificate against the
// fingerprint in the command. On a mismatch it stops and says so; it joins
// nothing and keeps nothing.
func TestJoinRefusesAControllerItCannotVerify(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Pin = strings.Repeat("ab", 32) // some other controller's fingerprint
	err := agent.Join(context.Background(), cfg, code.Code, false)
	if !errors.Is(err, agent.ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
	if len(cfg.Secrets.(*secretstest.Mem).Keys()) != 0 {
		t.Fatal("something was stored after a failed join")
	}
	if vs, _ := c.Svc.List(context.Background()); len(vs) != 0 {
		t.Fatal("the controller joined a machine that never trusted it")
	}
	// The code was never shown to the wrong server's holder: it is still unused.
	if _, st, _ := c.Svc.GetJoinCode(context.Background(), code.JoinCode.ID); st != "waiting" {
		t.Fatalf("code state = %v, want waiting", st)
	}
	// Run stops on it instead of retrying.
	stop := runAgent(t, cfg)
	// stop may cancel Run before it reaches the pin check; that is not a retry.
	if err := stop(); err != nil && !errors.Is(err, agent.ErrPinMismatch) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v", err)
	}
}

// Spec section 8: if the OS secret store is unavailable at join, the join fails
// with the reason, and the agent does not ask the controller for a key it cannot
// keep.
func TestJoinFailsWhenSecretStoreIsDown(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Secrets.(*secretstest.Mem).Down = true
	err := agent.Join(context.Background(), cfg, code.Code, false)
	if !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("err = %v, want the store's reason", err)
	}
	if _, st, _ := c.Svc.GetJoinCode(context.Background(), code.JoinCode.ID); st != "waiting" {
		t.Fatalf("the code was used (%v) although the key could not be kept", st)
	}
}

func TestJoinRefusedCodeNamesTheReason(t *testing.T) {
	c := startController(t)
	cfg := fastConfig(t, c, "")
	err := agent.Join(context.Background(), cfg, "wrong", false)
	if err == nil || !strings.Contains(err.Error(), "this code is not known") {
		t.Fatalf("err = %v", err)
	}
}

func TestJoinKeepsKeyIdAndPinInSecretStore(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	if err := agent.Join(context.Background(), cfg, code.Code, false); err != nil {
		t.Fatal(err)
	}
	for _, k := range []secrets.Key{agent.MachineKeySecret, agent.MachineIDSecret, agent.PinnedCertSecret} {
		if v, err := cfg.Secrets.Get(context.Background(), k); err != nil || len(v) == 0 {
			t.Fatalf("%s: %v", k, err)
		}
	}
	pin, _ := cfg.Secrets.Get(context.Background(), agent.PinnedCertSecret)
	if string(pin) != c.CA.Fingerprint() {
		t.Fatal("the pinned certificate was not kept")
	}
}
