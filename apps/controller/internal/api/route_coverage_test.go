package api

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPIPath is relative to this package's directory.
var openAPIPath = filepath.Join("..", "..", "..", "..", "packages", "contract", "openapi.yaml")

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// coverageProblems lists every operation in an OpenAPI document that does not
// declare both x-auth and x-rate-limit, and the number of operations seen.
func coverageProblems(doc []byte) (problems []string, operations int, err error) {
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &spec); err != nil {
		return nil, 0, err
	}
	for path, item := range spec.Paths {
		for method, node := range item {
			if !httpMethods[method] {
				continue
			}
			operations++
			var op map[string]any
			if err := node.Decode(&op); err != nil {
				return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
			}
			for _, ext := range []string{"x-auth", "x-rate-limit"} {
				if v, ok := op[ext]; !ok || v == nil || v == "" {
					problems = append(problems, fmt.Sprintf("%s %s: missing %s", method, path, ext))
				}
			}
		}
	}
	sort.Strings(problems)
	return problems, operations, nil
}

// TestRouteCoverage is the STACKSPEC 11.1 route-coverage test: a route in
// openapi.yaml without an explicit authorization and rate-limit declaration
// fails the build. A route that is public declares `x-auth: public`.
func TestRouteCoverage(t *testing.T) {
	doc, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	problems, operations, err := coverageProblems(doc)
	if err != nil {
		t.Fatal(err)
	}
	if operations == 0 {
		t.Fatal("openapi.yaml declares no operations; an empty check would pass for the wrong reason")
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestRouteCoverageDetectsMissingDeclarations(t *testing.T) {
	doc := []byte(`
openapi: 3.1.0
paths:
  /a:
    get:
      x-auth: public
      x-rate-limit: 1/second
  /b:
    get:
      x-auth: public
  /c:
    post:
      x-rate-limit: 1/second
    parameters: []
`)
	problems, operations, err := coverageProblems(doc)
	if err != nil {
		t.Fatal(err)
	}
	if operations != 3 {
		t.Fatalf("operations = %d, want 3", operations)
	}
	want := []string{"get /b: missing x-rate-limit", "post /c: missing x-auth"}
	if len(problems) != len(want) || problems[0] != want[0] || problems[1] != want[1] {
		t.Fatalf("problems = %v, want %v", problems, want)
	}
}
