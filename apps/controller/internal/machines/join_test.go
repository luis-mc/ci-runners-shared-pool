package machines_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

func joinWith(e *env, code string) (*agentv1.JoinResponse, error) {
	resp, err := e.client().Join(context.Background(), connect.NewRequest(&agentv1.JoinRequest{
		Credential: &agentv1.JoinRequest_JoinCode{JoinCode: code}, Facts: testFacts("box"),
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// R5.6: the controller checks the code, uses it up and issues a key for this
// machine only; the key is shown once and the pin comes back.
func TestJoinIssuesKeyAndUsesCodeUp(t *testing.T) {
	e := newEnv(t)
	r, code := e.joined("box-1")
	if r.GetMachineId() == "" || !strings.HasPrefix(r.GetMachineCredential(), "crsp_mk_") || r.GetCertSha256() != e.CA.Fingerprint() {
		t.Fatalf("response = %+v", r)
	}
	if r.GetSettings().GetBudgetMemoryPct() != 85 || r.GetSettings().GetBudgetCpuPct() != 60 {
		t.Fatalf("default settings = %+v", r.GetSettings())
	}
	_, err := joinWith(e, code)
	if err == nil || !strings.Contains(err.Error(), "this code was already used") {
		t.Fatalf("second join err = %v, want 'this code was already used'", err)
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	vs, _ := e.Svc.List(context.Background())
	if len(vs) != 1 || vs[0].Machine.Name != "box-1" || vs[0].Machine.Backends == nil {
		t.Fatalf("machines = %+v", vs)
	}
}

func TestJoinRefusalsNameTheReason(t *testing.T) {
	e := newEnv(t)
	if _, err := joinWith(e, "not-a-real-code"); err == nil || !strings.Contains(err.Error(), "this code is not known") {
		t.Fatalf("unknown: %v", err)
	}
	c, _ := e.Svc.CreateJoinCode(context.Background(), "owner", time.Minute)
	e.Clk.Add(2 * time.Minute)
	want := "this code expired at " + c.JoinCode.ExpiresAt.Local().Format("15:04")
	if _, err := joinWith(e, c.Code); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expired: %v, want %q", err, want)
	}
	c2, _ := e.Svc.CreateJoinCode(context.Background(), "owner", 0)
	if err := e.Svc.RevokeJoinCode(context.Background(), "owner", c2.JoinCode.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := joinWith(e, c2.Code); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("withdrawn: %v", err)
	}
	// A local credential is not a join code and the other way round (R5.1).
	cred, _ := e.Svc.LocalCredential(context.Background())
	if _, err := joinWith(e, cred); err == nil {
		t.Fatal("a local credential joined as a code")
	}
}

func TestJoinNeedsFacts(t *testing.T) {
	e := newEnv(t)
	c, _ := e.Svc.CreateJoinCode(context.Background(), "owner", 0)
	_, err := e.Svc.Join(context.Background(), machines.JoinInput{Code: c.Code})
	if !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// R5.43: a removed machine joins again only with a new code and gets a new key.
func TestRemovedMachineJoinsAgainWithNewCodeAndNewKey(t *testing.T) {
	e := newEnv(t)
	first, _ := e.joined("box-1")
	if _, err := e.Svc.Remove(context.Background(), "owner", first.GetMachineId(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Authenticate(context.Background(), first.GetMachineCredential(), ""); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("old key still works: %v", err)
	}
	second, _ := e.joined("box-1")
	if second.GetMachineId() == first.GetMachineId() || second.GetMachineCredential() == first.GetMachineCredential() {
		t.Fatal("expected a new machine id and a new key")
	}
	if _, err := e.Svc.Authenticate(context.Background(), first.GetMachineCredential(), ""); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatal("old key must stay revoked")
	}
}

// Two machines with one host name get distinct names. The first is online, so the
// second join is not its rejoin (OPS59).
func TestJoinMakesNamesUnique(t *testing.T) {
	e := newEnv(t)
	first, _ := e.joined("box")
	m, _ := e.Repo.GetMachine(context.Background(), first.GetMachineId())
	m.Online = true
	_ = e.Repo.UpdateMachine(context.Background(), m)
	e.joined("box")
	vs, _ := e.Svc.List(context.Background())
	if len(vs) != 2 || vs[0].Machine.Name == vs[1].Machine.Name {
		t.Fatalf("names = %v, %v", vs[0].Machine.Name, vs[1].Machine.Name)
	}
}

// R5.3 (M1 by owner decision): one install command per OS carries the address,
// port 8461, the certificate fingerprint and the code.
func TestInstallCommandsCarryAddressPortFingerprintAndCode(t *testing.T) {
	e := newEnv(t)
	cmds := e.Svc.InstallCommands("CODE123")
	if len(cmds) != 3 {
		t.Fatalf("commands = %v", cmds)
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[c.OS] = true
		for _, want := range []string{"pool.lan:8461", e.CA.Fingerprint(), "CODE123"} {
			if !strings.Contains(c.Command, want) {
				t.Errorf("%s command %q lacks %q", c.OS, c.Command, want)
			}
		}
	}
	if !seen["windows"] || !seen["linux"] || !seen["macos"] {
		t.Fatalf("OS set = %v", seen)
	}
	if without := e.Svc.InstallCommands(""); strings.Contains(without[0].Command, "CODE123") || !strings.Contains(without[0].Command, "<join code>") {
		t.Fatalf("no code: %q", without[0].Command)
	}
	_ = base64.RawURLEncoding
}
