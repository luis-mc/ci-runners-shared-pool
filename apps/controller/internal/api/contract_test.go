package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// allowedAuth is the x-auth vocabulary: public, any signed-in user (viewer and
// up), admin (and owner), owner (github-integration R5.12, web-console R6.5).
var allowedAuth = map[string]bool{"public": true, "user": true, "admin": true, "owner": true}

var rateLimitShape = regexp.MustCompile(`^[1-9][0-9]*/(second|minute|hour)/(ip|user)$`)

type contractOp struct {
	Method, Path, ID, Auth, Rate string
	HasBody                      bool
}

func loadContractOps(t *testing.T) []contractOp {
	t.Helper()
	doc, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
			Auth        string `yaml:"x-auth"`
			Rate        string `yaml:"x-rate-limit"`
			RequestBody any    `yaml:"requestBody"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &spec); err != nil {
		t.Fatal(err)
	}
	var ops []contractOp
	for path, item := range spec.Paths {
		for method, op := range item {
			if !httpMethods[method] {
				continue
			}
			ops = append(ops, contractOp{
				Method: strings.ToUpper(method), Path: path, ID: op.OperationID,
				Auth: op.Auth, Rate: op.Rate, HasBody: op.RequestBody != nil,
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
	return ops
}

// TestContractDeclarations checks the shape of each operation's declarations:
// a unique operationId, an x-auth from the fixed vocabulary, and a rate limit
// of the form N/unit/key.
func TestContractDeclarations(t *testing.T) {
	seen := map[string]bool{}
	for _, op := range loadContractOps(t) {
		where := op.Method + " " + op.Path
		if op.ID == "" {
			t.Errorf("%s: no operationId", where)
		} else if seen[op.ID] {
			t.Errorf("%s: duplicate operationId %s", where, op.ID)
		}
		seen[op.ID] = true
		if !allowedAuth[op.Auth] {
			t.Errorf("%s: x-auth %q is not one of public, user, admin, owner", where, op.Auth)
		}
		if !rateLimitShape.MatchString(op.Rate) {
			t.Errorf("%s: x-rate-limit %q is not N/second|minute|hour/ip|user", where, op.Rate)
		}
	}
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// TestUnbuiltOperationsAnswer501 proves every contract route is mounted: each
// operation other than the health check answers 501 with the error envelope
// until its feature replaces the stub in stubs.go.
func TestUnbuiltOperationsAnswer501(t *testing.T) {
	mux := newMux(fakeDB{})
	for _, op := range loadContractOps(t) {
		if op.ID == "getHealth" {
			continue
		}
		target := pathParam.ReplaceAllString(op.Path, "1") + "?code=c&state=s"
		var body *bytes.Reader
		if op.HasBody {
			body = bytes.NewReader([]byte(`{}`))
		} else {
			body = bytes.NewReader(nil)
		}
		req := httptest.NewRequestWithContext(context.Background(), op.Method, target, body)
		req.Header.Set("Content-Type", "application/json")
		for _, h := range []string{"X-GitHub-Event", "X-GitHub-Delivery", "X-Hub-Signature-256"} {
			req.Header.Set(h, "x")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s (%s): status %d, want 501", op.Method, op.Path, op.ID, rec.Code)
			continue
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != CodeNotImplemented {
			t.Errorf("%s: body %q is not the not_implemented envelope", op.ID, rec.Body.String())
		}
	}
}
