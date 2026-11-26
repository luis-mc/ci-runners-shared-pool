package logs

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
)

// secretCase is one kind of secret: text is what a sink is given (the secret
// with its context), secret is what must never come out.
type secretCase struct {
	kind, text, secret string
	value              bool // a value the process holds: the redactor is taught it
}

// pieces keep the test source free of secret-looking literals.
const (
	ghp = "gh" + "p_"
	pat = "github_" + "pat_"
	jwt = "ey" + "JhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlMTIzNDU2"
	pem = "-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIBPLANTEDKEYBODY0001\n-----END RSA PRIV" + "ATE KEY-----"
)

// planted builds a fake secret at run time, so no source line holds a key-shaped
// literal for the history scan to flag.
func planted(kind, n string) string { return kind + "-" + "PLANT" + "ED-" + n }

// secretCases is the one table of secret kinds (R6.6). TestSecretKindTableIsComplete
// fails when redact.ValueKinds or redact.ShapeKinds has a kind this table lacks,
// so a new kind cannot be added without a planted value.
var secretCases = []secretCase{
	{"app_private_key", planted("appkey", "0001"), planted("appkey", "0001"), true},
	{"webhook_secret", planted("whsec", "0002"), planted("whsec", "0002"), true},
	{"installation_token", planted("instok", "0003"), planted("instok", "0003"), true},
	{"oauth_token", planted("oauth", "0004"), planted("oauth", "0004"), true},
	{"refresh_token", planted("refresh", "0005"), planted("refresh", "0005"), true},
	{"registration_token", planted("regtok", "0006"), planted("regtok", "0006"), true},
	{"remove_token", planted("rmtok", "0007"), planted("rmtok", "0007"), true},
	{"jit_config", planted("jitcfg", "0008"), planted("jitcfg", "0008"), true},
	{"join_code", planted("joincode", "0009"), planted("joincode", "0009"), true},
	{"machine_key", planted("machkey", "0010"), planted("machkey", "0010"), true},
	{"session_cookie", planted("sesscookie", "0011"), planted("sesscookie", "0011"), true},

	{"github_token", ghp + strings.Repeat("a", 30), ghp + strings.Repeat("a", 30), false},
	{"github_pat", pat + strings.Repeat("b", 30), pat + strings.Repeat("b", 30), false},
	{"private_key", pem, "MIIBPLANTEDKEYBODY0001", false},
	{"cli_token", "crsp_cli_" + "PLANT" + "EDCLI0012", "crsp_cli_" + "PLANT" + "EDCLI0012", false},
	{"access_token", "crsp_acc_" + "PLANT" + "EDACC0013", "crsp_acc_" + "PLANT" + "EDACC0013", false},
	{"jwt", jwt, jwt, false},
	{"auth_header", "Authorization: Bearer PLANTEDBEARER0014", "PLANTEDBEARER0014", false},
	{"token_flag", "config.sh --token PLANTEDFLAG0015 --url u", "PLANTEDFLAG0015", false},
	{"runner_input", "ACTIONS_RUNNER_INPUT_" + "TOKEN=PLANTEDINPUT0016", "PLANTEDINPUT0016", false},
}

// plantedRedactor knows every value kind of the table.
func plantedRedactor() *redact.Redactor {
	r := redact.New()
	for _, c := range secretCases {
		if c.value {
			r.AddKind(c.kind, c.secret)
		}
	}
	return r
}

// forms are the spellings a sink must not show: the value and its base64.
func (c secretCase) forms() []string {
	return []string{c.secret,
		base64.StdEncoding.EncodeToString([]byte(c.secret)),
		base64.RawStdEncoding.EncodeToString([]byte(c.secret)),
		base64.URLEncoding.EncodeToString([]byte(c.secret))}
}

// planted is what a test writes into a sink: the text and, for a value, its base64 form.
func (c secretCase) planted() string {
	if c.value {
		return c.text + " " + base64.StdEncoding.EncodeToString([]byte(c.secret))
	}
	return c.text
}

func TestKnownValuesAndShapesRedacted(t *testing.T) {
	r := plantedRedactor()
	for _, c := range secretCases {
		got := r.Tagged("before " + c.planted() + " after")
		for _, f := range c.forms() {
			if strings.Contains(got, f) {
				t.Errorf("%s: %q left in %q", c.kind, f, got)
			}
		}
		if !strings.Contains(got, "[redacted:"+c.kind+"]") {
			t.Errorf("%s: no [redacted:%s] in %q", c.kind, c.kind, got)
		}
	}
	if got := r.Tagged("ordinary text, no secret"); got != "ordinary text, no secret" {
		t.Errorf("plain text changed: %q", got)
	}
}

func TestSecretKindTableIsComplete(t *testing.T) {
	want := append(append([]string{}, redact.ValueKinds...), redact.ShapeKinds()...)
	have := map[string]bool{}
	for _, c := range secretCases {
		if have[c.kind] {
			t.Errorf("kind %s twice in the table", c.kind)
		}
		have[c.kind] = true
	}
	for _, k := range want {
		if !have[k] {
			t.Errorf("secret kind %q has no planted value in secretCases (internal/logs/redact_test.go): add one", k)
		}
		delete(have, k)
	}
	for k := range have {
		t.Errorf("secretCases has kind %q that internal/redact does not list", k)
	}
}
