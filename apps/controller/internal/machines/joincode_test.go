package machines_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.4: a code works once, lives 15 minutes by default (ceiling 24 hours) and a
// new code makes the old unused one invalid.
func TestJoinCodeLifetimeAndReplacement(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, err := e.Svc.CreateJoinCode(ctx, "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.JoinCode.ExpiresAt.Sub(a.JoinCode.CreatedAt); got != 15*time.Minute {
		t.Fatalf("default lifetime = %v, want 15m", got)
	}
	if _, err := e.Svc.CreateJoinCode(ctx, "owner", 25*time.Hour); err == nil {
		t.Fatal("a lifetime above 24 hours must be refused")
	}
	if c, err := e.Svc.CreateJoinCode(ctx, "owner", 24*time.Hour); err != nil || c.JoinCode.ExpiresAt.Sub(c.JoinCode.CreatedAt) != 24*time.Hour {
		t.Fatalf("24 hours: %v", err)
	}
	// The 24-hour code replaced a; a is now withdrawn.
	if _, st, _ := e.Svc.GetJoinCode(ctx, a.JoinCode.ID); st != machines.CodeRevoked {
		t.Fatalf("old code state = %v, want revoked", st)
	}
	if _, err := joinWith(e, a.Code); err == nil {
		t.Fatal("the replaced code still joined")
	}
	// Expiry is judged on the controller clock.
	b, _ := e.Svc.CreateJoinCode(ctx, "owner", time.Minute)
	if _, st, _ := e.Svc.GetJoinCode(ctx, b.JoinCode.ID); st != machines.CodeWaiting {
		t.Fatalf("state = %v", st)
	}
	e.Clk.Add(time.Minute)
	if _, st, _ := e.Svc.GetJoinCode(ctx, b.JoinCode.ID); st != machines.CodeExpired {
		t.Fatalf("state = %v, want expired", st)
	}
	// Used: joined.
	c, _ := e.Svc.CreateJoinCode(ctx, "owner", 0)
	if _, err := joinWith(e, c.Code); err != nil {
		t.Fatal(err)
	}
	if jc, st, _ := e.Svc.GetJoinCode(ctx, c.JoinCode.ID); st != machines.CodeJoined || jc.MachineID == "" {
		t.Fatalf("state = %v machine=%q", st, jc.MachineID)
	}
}

// R6.3: a code has at least 128 bits of entropy.
func TestJoinCodeHas128BitsOfEntropy(t *testing.T) {
	e := newEnv(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, _ := e.Svc.CreateJoinCode(context.Background(), "owner", 0)
		raw, err := base64.RawURLEncoding.DecodeString(c.Code)
		if err != nil || len(raw) < 16 {
			t.Fatalf("code %q decodes to %d bytes (%v); want at least 16", c.Code, len(raw), err)
		}
		if seen[c.Code] {
			t.Fatal("a code repeated")
		}
		seen[c.Code] = true
	}
}

// R6.3: after 10 failed join attempts from one address in one minute, that
// address is refused for 5 minutes.
func TestJoinRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := func(code string) machines.JoinInput {
		return machines.JoinInput{Code: code, Facts: machines.Facts{OS: "linux", Arch: "x64", HostName: "x"}, RemoteAddr: "10.0.0.9:5555"}
	}
	for i := 0; i < 10; i++ {
		if _, err := e.Svc.Join(ctx, in(fmt.Sprintf("bad-%d", i))); err == nil {
			t.Fatal("a bad code joined")
		}
	}
	good, _ := e.Svc.CreateJoinCode(ctx, "owner", 0)
	_, err := e.Svc.Join(ctx, in(good.Code))
	if je, ok := err.(*machines.JoinError); !ok || !strings.Contains(je.Reason, "too many") {
		t.Fatalf("err = %v, want a refusal for too many attempts", err)
	}
	// Another address is not affected.
	other := in(good.Code)
	other.RemoteAddr = "10.0.0.10:1"
	if _, err := e.Svc.Join(ctx, other); err != nil {
		t.Fatalf("other address: %v", err)
	}
	// After 5 minutes the address may try again.
	e.Clk.Add(5*time.Minute + time.Second)
	again, _ := e.Svc.CreateJoinCode(ctx, "owner", 0)
	if _, err := e.Svc.Join(ctx, in(again.Code)); err != nil {
		t.Fatalf("after the block: %v", err)
	}
	var _ = agentv1.ProtocolVersion_PROTOCOL_VERSION_1
	var _ = connect.CodeUnknown
}
