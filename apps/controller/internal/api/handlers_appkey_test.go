package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

func rsaPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func TestReplaceAppPrivateKey(t *testing.T) { // github-integration R5.7
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddUser(2, "bob")
	if res, _ := h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, hdr); res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	admin, acsrf := h.signIn(2, cookies{})
	keyName := github.SecretKey(h.fake.AppID, github.SecretPrivateKey)
	stored := func() []byte {
		b, err := h.secrets.Get(context.Background(), keyName)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	old := stored()
	k2, pem2 := rsaPEM(t)
	_, other := rsaPEM(t)
	h.fake.AppPublicKey = &k2.PublicKey

	r := redact.New()
	r.Add(pem2)
	r.Add(other)
	seenBody := func(b []byte) {
		t.Helper()
		if r.Redact(string(b)) != string(b) {
			t.Fatalf("a key came back in an answer: %s", b)
		}
	}

	// A non-owner is refused and nothing changes.
	res, b := h.do("PUT", "/api/v1/app/private-key", map[string]string{"pem": pem2}, admin, map[string]string{"X-CSRF-Token": acsrf})
	if res.StatusCode != 403 || !bytes.Equal(stored(), old) {
		t.Fatalf("admin: %d %s", res.StatusCode, b)
	}
	seenBody(b)
	// Not signed in.
	if res, _ := h.do("PUT", "/api/v1/app/private-key", map[string]string{"pem": pem2}, cookies{}, nil); res.StatusCode != 401 && res.StatusCode != 403 {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
	// Bad PEM and a key of another App: 400, old key kept.
	for name, in := range map[string]string{"bad pem": "nope", "other app": other} {
		res, b := h.do("PUT", "/api/v1/app/private-key", map[string]string{"pem": in}, owner, hdr)
		if res.StatusCode != 400 || errCode(b) != CodeBadRequest || !strings.Contains(string(b), "old key was kept") || !bytes.Equal(stored(), old) {
			t.Fatalf("%s: %d %s", name, res.StatusCode, b)
		}
		seenBody(b)
	}
	// The right key: 204 with no body, stored, audited without the key.
	res, b = h.do("PUT", "/api/v1/app/private-key", map[string]string{"pem": pem2}, owner, hdr)
	if res.StatusCode != 204 || len(b) != 0 || string(stored()) != pem2 {
		t.Fatalf("owner: %d %s", res.StatusCode, b)
	}
	var rotated bool
	for _, a := range h.astore.AuditEvents() {
		if a.Action == "app.key_rotated" {
			rotated = true
		}
		for _, v := range []string{a.Actor, a.Subject, a.Detail, a.Action, a.Result} {
			if r.Redact(v) != v {
				t.Fatalf("a key reached the audit: %q", v)
			}
		}
	}
	if !rotated {
		t.Fatal("rotation not audited")
	}
}
