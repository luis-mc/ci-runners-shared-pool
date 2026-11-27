package machines_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// R6.2: no key or join code is stored in clear; only hashes. The key shows once.
func TestNoSecretStoredInClear(t *testing.T) {
	e := newEnv(t)
	code, _ := e.Svc.CreateJoinCode(context.Background(), "owner", 0)
	r, _ := joinWith(e, code.Code)
	key := r.GetMachineCredential()
	for _, h := range e.Repo.KeyHashes() {
		if h == key || strings.Contains(h, key) || len(h) != 64 {
			t.Fatalf("key hash %q is not a SHA-256 hash of the key", h)
		}
	}
	for _, h := range e.Repo.CodeHashes() {
		if h == code.Code || strings.Contains(h, code.Code) || len(h) != 64 {
			t.Fatalf("code hash %q is not a SHA-256 hash of the code", h)
		}
	}
	// Neither the audit log nor the rows the API reads carry them.
	for _, a := range e.Repo.AuditLog() {
		blob := fmt.Sprint(a)
		if strings.Contains(blob, key) || strings.Contains(blob, code.Code) {
			t.Fatalf("audit entry holds a secret: %v", a)
		}
	}
	vs, _ := e.Svc.List(context.Background())
	if strings.Contains(fmt.Sprintf("%+v", vs), key) || strings.Contains(fmt.Sprintf("%+v", vs), code.Code) {
		t.Fatal("machine view holds a secret")
	}
	if jc, _, _ := e.Svc.GetJoinCode(context.Background(), code.JoinCode.ID); strings.Contains(fmt.Sprintf("%+v", jc), code.Code) {
		t.Fatal("join code record holds the code")
	}
}
