package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

func newPEM(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func storedKey(t *testing.T, e *env, appID int64) []byte {
	t.Helper()
	b, err := e.secrets.Get(context.Background(), github.SecretKey(appID, github.SecretPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRotateAppKeyReplaces(t *testing.T) { // R5.7
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	ctx := context.Background()
	k2, pem2 := newPEM(t)
	e.fake.AppPublicKey = &k2.PublicKey // GitHub now knows only the new key
	if _, err := e.gh.GetApp(ctx); err == nil {
		t.Fatal("old key must no longer work at GitHub in this setup")
	}
	if err := e.svc.RotateAppKey(ctx, owner, pem2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedKey(t, e, e.apps[0].ID), pem2) {
		t.Fatal("the new key was not stored")
	}
	if _, err := e.gh.GetApp(ctx); err != nil {
		t.Fatalf("the client must use the new key at once: %v", err)
	}
	if !hasAudit(e.store, "app.key_rotated") {
		t.Fatal("rotation not audited")
	}
}

func TestRotateAppKeyRefusedKeepsOld(t *testing.T) { // R5.7
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	ctx := context.Background()
	old := storedKey(t, e, e.apps[0].ID)
	_, otherApp := newPEM(t) // a valid key that belongs to another App
	for name, in := range map[string][]byte{
		"wrong app": otherApp, "bad pem": []byte("not a key"), "empty": nil,
		"cert block": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}),
	} {
		err := e.svc.RotateAppKey(ctx, owner, in)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Fatalf("%s: want a refusal the person can fix, got %v", name, err)
		}
		if !bytes.Equal(storedKey(t, e, e.apps[0].ID), old) {
			t.Fatalf("%s: the old key was replaced", name)
		}
		if _, err := e.gh.GetApp(ctx); err != nil {
			t.Fatalf("%s: the old key stopped working: %v", name, err)
		}
	}
	if hasAudit(e.store, "app.key_rotated") || !hasAudit(e.store, "app.key_rotation_failed") {
		t.Fatal("failures must be audited as failed, and no success row written")
	}
}

func TestRotateAppKeyOwnerOnly(t *testing.T) { // R5.7, R5.12
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleAdmin)
	admin := e.signIn(2, "").Principal
	_, pem2 := newPEM(t)
	old := storedKey(t, e, e.apps[0].ID)
	for _, p := range []Principal{admin, {}} {
		if err := e.svc.RotateAppKey(context.Background(), p, pem2); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("role %q: %v", p.Role, err)
		}
	}
	if !bytes.Equal(storedKey(t, e, e.apps[0].ID), old) {
		t.Fatal("a non-owner replaced the key")
	}
}

func TestRotateAppKeyStoreFailureKeepsOld(t *testing.T) { // R5.7
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	old := storedKey(t, e, e.apps[0].ID)
	k2, pem2 := newPEM(t)
	e.fake.AppPublicKey = &k2.PublicKey
	e.svc.cfg.Secrets = failingPut{e.secrets}
	if err := e.svc.RotateAppKey(context.Background(), owner, pem2); err == nil {
		t.Fatal("a store failure must be an error")
	}
	if !bytes.Equal(storedKey(t, e, e.apps[0].ID), old) {
		t.Fatal("old key lost")
	}
}

func TestRotateAppKeyNeverLeaksKey(t *testing.T) { // R5.7, R6.3
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	k2, pem2 := newPEM(t)
	e.fake.AppPublicKey = &k2.PublicKey
	_, other := newPEM(t)
	var errs []string
	for _, in := range [][]byte{other, pem2} {
		if err := e.svc.RotateAppKey(context.Background(), owner, in); err != nil {
			errs = append(errs, err.Error())
		}
	}
	var text []string
	for _, a := range e.store.AuditEvents() {
		text = append(text, a.Actor, a.Action, a.Subject, a.Detail, a.Result)
	}
	body := strings.Join(pem2Lines(pem2), "")
	r := redact.New()
	r.Add(string(pem2))
	r.Add(body)
	for _, v := range append(text, errs...) {
		if r.Redact(v) != v || strings.Contains(v, "PRIVATE KEY") || strings.Contains(v, body[:40]) {
			t.Fatalf("key text reached an error or the audit: %q", v)
		}
	}
}

func pem2Lines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" && !strings.HasPrefix(l, "-----") {
			out = append(out, l)
		}
	}
	return out
}

// failingPut is a store whose Put always fails.
type failingPut struct{ secrets.Store }

func (failingPut) Put(context.Context, secrets.Key, []byte) error { return errors.New("disk full") }
