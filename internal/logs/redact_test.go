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

// secretCases is the one table of secret kinds (R6.6). TestSecretKindTableIsComplete
// fails when redact.ValueKinds or redact.ShapeKinds has a kind this table lacks,
// so a new kind cannot be added without a planted value.
var secretCases = []secretCase{
	{"app_private_key", "appkey-PLANTED-0001", "appkey-PLANTED-0001", true},
	{"webhook_secret", "whsec-PLANTED-0002", "whsec-PLANTED-0002", true},
	{"installation_token", "instok-PLANTED-0003", "instok-PLANTED-0003", true},
	{"oauth_token", "oauth-PLANTED-0004", "oauth-PLANTED-0004", true},
	{"refresh_token", "refresh-PLANTED-0005", "refresh-PLANTED-0005", true},
	{"registration_token", "regtok-PLANTED-0006", "regtok-PLANTED-0006", true},
	{"remove_token", "rmtok-PLANTED-0007", "rmtok-PLANTED-0007", true},
	{"jit_config", "jitcfg-PLANTED-0008", "jitcfg-PLANTED-0008", true},
	{"join_code", "joincode-PLANTED-0009", "joincode-PLANTED-0009", true},
	{"machine_key", "machkey-PLANTED-0010", "machkey-PLANTED-0010", true},
	{"session_cookie", "sesscookie-PLANTED-0011", "sesscookie-PLANTED-0011", true},

	{"github_token", ghp + strings.Repeat("a", 30), ghp + strings.Repeat("a", 30), false},
	{"github_pat", pat + strings.Repeat("b", 30), pat + strings.Repeat("b", 30), false},
	{"private_key", pem, "MIIBPLANTEDKEYBODY0001", false},
	{"cli_token", "crsp_cli_PLANTEDCLI0012", "crsp_cli_PLANTEDCLI0012", false},
	{"access_token", "crsp_acc_PLANTEDACC0013", "crsp_acc_PLANTEDACC0013", false},
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
