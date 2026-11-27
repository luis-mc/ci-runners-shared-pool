package api

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// R6.4: no secret reaches the browser. Two walks: every response schema of
// the contract is searched for a field named like a secret, and the answers of
// a signed-in console are searched for the secret values the controller holds.

// secretName matches a field name that would carry a secret: the App private
// key, the webhook or client secret, a GitHub or access token, a password, an
// agent key.
var secretName = regexp.MustCompile(`(?i)(secret|token|password|passwd|private|api_?key|agent_?key|credential|signing_?key|(^|_)pem($|_))`)

// allowedSecretFields are the response fields that may be named like a secret,
// each with the reason. Add nothing here without the owner's say-so.
var allowedSecretFields = map[string]string{
	// The CLI token goes to the CLI, once, at creation: it is the answer to the
	// CLI's own sign-in, not data of the console (github-integration R5.15).
	"CliTokenCreated.token": "returned once to the CLI that asked for it",
	// A true or false that says a secret is stored; the value itself is never in
	// any answer (notifications R6.2). Merged from CRSP37: needs the owner's say-so.
	"NotificationChannel.secret_set": "a boolean: a signing secret is stored; the value is never returned",
	"SmtpSettings.has_password":      "a boolean: a mail password is stored; the value is never returned",
}

// secretFieldFindings lists the fields of response bodies in an OpenAPI
// document that are named like a secret and not allowed. A $ref is followed.
func secretFieldFindings(doc []byte) ([]string, int, error) {
	var spec struct {
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Schemas   map[string]any `yaml:"schemas"`
			Responses map[string]any `yaml:"responses"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(doc, &spec); err != nil {
		return nil, 0, err
	}
	found := map[string]bool{}
	var walk func(n any, owner string, seen map[string]bool)
	walk = func(n any, owner string, seen map[string]bool) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			name := ref[strings.LastIndex(ref, "/")+1:]
			if seen[name] {
				return
			}
			seen[name] = true
			if strings.Contains(ref, "/responses/") {
				walk(spec.Components.Responses[name], name, seen)
			} else {
				walk(spec.Components.Schemas[name], name, seen)
			}
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for field, sub := range props {
				if secretName.MatchString(field) {
					key := owner + "." + field
					if _, ok := allowedSecretFields[key]; !ok {
						found[key] = true
					}
				}
				walk(sub, owner, seen)
			}
		}
		for _, k := range []string{"items", "additionalProperties"} {
			walk(m[k], owner, seen)
		}
		for _, k := range []string{"allOf", "oneOf", "anyOf"} {
			if list, ok := m[k].([]any); ok {
				for _, x := range list {
					walk(x, owner, seen)
				}
			}
		}
		if content, ok := m["content"].(map[string]any); ok {
			for _, c := range content {
				if cm, ok := c.(map[string]any); ok {
					walk(cm["schema"], owner, seen)
				}
			}
		}
	}
	responses := 0
	for _, item := range spec.Paths {
		for method, node := range item {
			if !httpMethods[method] {
				continue
			}
			var op struct {
				ID        string         `yaml:"operationId"`
				Responses map[string]any `yaml:"responses"`
			}
			if err := node.Decode(&op); err != nil {
				return nil, 0, fmt.Errorf("%s: %w", method, err)
			}
			for _, r := range op.Responses {
				responses++
				walk(r, op.ID, map[string]bool{})
			}
		}
	}
	var out []string
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, responses, nil
}

func TestNoResponseSchemaCarriesASecret(t *testing.T) {
	doc, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	found, responses, err := secretFieldFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if responses < 50 {
		t.Fatalf("only %d responses walked; the walk is broken", responses)
	}
	for _, f := range found {
		t.Errorf("response field %s is named like a secret; remove it or list it in allowedSecretFields with the reason", f)
	}
	for key := range allowedSecretFields {
		if !strings.Contains(string(doc), key[strings.Index(key, ".")+1:]+":") {
			t.Errorf("allowedSecretFields has %s, which the contract no longer has", key)
		}
	}
}

func TestSecretWalkFindsASecretField(t *testing.T) { // the walk is not blind
	doc := []byte(`
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/A'}
components:
  schemas:
    A:
      type: object
      properties:
        name: {type: string}
        inner: {$ref: '#/components/schemas/B'}
        list:
          type: array
          items: {$ref: '#/components/schemas/B'}
    B:
      type: object
      properties:
        private_key: {type: string}
        webhook_secret: {type: string}
        access_token: {type: string}
`)
	got, _, err := secretFieldFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A.access_token", "A.private_key", "A.webhook_secret"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		// owner is the schema whose walk began at the operation response, so a
		// nested field is reported under the schema that holds it.
		want = []string{"B.access_token", "B.private_key", "B.webhook_secret"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v", got)
		}
	}
}

// R6.4: every answer a signed-in owner gets from the console's reads, and
// every header, is free of the secret values the controller keeps in the OS
// store (the App private key, the webhook secret, the client secret) and of
// the GitHub tokens it holds.
func TestAnswersCarryNoStoredSecret(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	var secretValues [][]byte
	for _, k := range h.secrets.Keys() {
		v, err := h.secrets.Get(t.Context(), k)
		if err != nil || len(v) < 8 {
			t.Fatalf("secret %v: %v (%d bytes)", k, err, len(v))
		}
		secretValues = append(secretValues, v)
	}
	if len(secretValues) < 3 {
		t.Fatalf("expected the three App secrets in the store, got %d", len(secretValues))
	}
	for _, op := range loadContractOps(t) {
		if op.Method != "GET" || op.Auth == authPublic && op.ID == "getHealth" || op.ID == "streamEvents" {
			continue
		}
		path := pathParam.ReplaceAllString(op.Path, "1")
		res, b := h.do("GET", path, nil, owner, nil)
		var all strings.Builder
		all.Write(b)
		for k, vs := range res.Header {
			all.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		for _, v := range secretValues {
			if strings.Contains(all.String(), string(v)) {
				t.Errorf("GET %s (%s) answered with a stored secret", op.Path, op.ID)
			}
		}
	}
}
