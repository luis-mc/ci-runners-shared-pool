package auth

import (
	"strings"
	"testing"
)

func TestAuditScrubsTokens(t *testing.T) { // R5.16, R6.3
	for _, in := range []string{
		"x ghs_" + "abcdefghijklmnop y", "ghu_" + "abcdefghijklmnop", "ghr_" + "abcdefgh1234", "gho_" + "abcdefgh1234",
		"ghp_" + "abcdefgh1234", "github_pat_" + "11AAAA_bbbbCCCC", CLITokenPrefix + "abcDEF-123_x",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----",
	} {
		out := scrub("detail: " + in + " end")
		if strings.Contains(out, "abcdefgh") || strings.Contains(out, "MIIE") || strings.Contains(out, "abcDEF") || strings.Contains(out, "11AAAA") {
			t.Errorf("token survived: %q -> %q", in, out)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("not redacted: %q", out)
		}
	}
	if scrub("role admin to viewer") != "role admin to viewer" {
		t.Fatal("plain text must pass")
	}
}

func TestAuditEveryChange(t *testing.T) { // R5.16
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	e.signIn(2, "")
	_, _ = e.trySignIn(1, "") // another sign-in
	e.fake.AddUser(9, "stranger")
	_, _ = e.trySignIn(9, "")
	for _, want := range []string{"setup.app_created", "auth.owner_claimed", "auth.signin", "user.added", "auth.signin_failed"} {
		if !hasAudit(e.store, want) {
			t.Errorf("missing audit %q", want)
		}
	}
	for _, ev := range e.store.AuditEvents() {
		if ev.Actor == "" || ev.Action == "" || ev.Result == "" {
			t.Errorf("who, what and result must be set: %+v", ev)
		}
	}
}
