package toolstore

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

func sf(path, body string) registry.StackFile {
	return registry.StackFile{Path: path, Content: []byte(body)}
}

func planStr(p Plan) string {
	var s []string
	for _, t := range p {
		s = append(s, t.Tool+" "+t.Version)
	}
	return strings.Join(s, ",")
}

// R5.110: the plan comes from the stacks (profile defaults), then version files, then .crsp.yml tools.
func TestBuildPlan(t *testing.T) {
	cases := []struct {
		name  string
		files []registry.StackFile
		yml   string
		want  string
	}{
		{"no stack", nil, "", ""},
		{"rust default", []registry.StackFile{sf("Cargo.toml", "")}, "", "rust 1.84.0"},
		{"rust toolchain file", []registry.StackFile{sf("Cargo.lock", ""), sf("rust-toolchain.toml", "[toolchain]\nchannel = \"1.85\"\n")}, "", "rust 1.85"},
		{"node nvmrc", []registry.StackFile{sf(".nvmrc", "v20.11.1\n")}, "", "node 20.11.1,pnpm 10.34.5"},
		{"node default and pnpm", []registry.StackFile{sf("package.json", `{"packageManager":"pnpm@9.1.0"}`)}, "", "node 22.11.0,pnpm 9.1.0"},
		{"go mod toolchain wins", []registry.StackFile{sf("go.mod", "module x\n\ngo 1.23\n\ntoolchain go1.23.4\n")}, "", "go 1.23.4"},
		{"go mod language line", []registry.StackFile{sf("go.mod", "module x\n\ngo 1.24\n")}, "", "go 1.24.0"},
		{"python version file", []registry.StackFile{sf(".python-version", "3.12.1")}, "", "python 3.12.1"},
		{"tool-versions", []registry.StackFile{sf(".tool-versions", "nodejs 18.19.0 # old\njava 21.0.2\nruby 3.3\n")}, "", "java 21.0.2,node 18.19.0"},
		{"dotnet global.json", []registry.StackFile{sf("global.json", `{"sdk":{"version":"8.0.100"}}`)}, "", "dotnet 8.0.100"},
		{"crsp tools override", []registry.StackFile{sf("Cargo.toml", "")}, "tools:\n  - rust 1.90.0\n  - java@17.0.9\n", "java 17.0.9,rust 1.90.0"},
		{"unsafe version file ignored", []registry.StackFile{sf(".nvmrc", "../../etc"), sf("package.json", "{}")}, "", "node 22.11.0,pnpm 10.34.5"},
	}
	for _, c := range cases {
		got, err := BuildPlan(c.files, []byte(c.yml))
		if err != nil || planStr(got) != c.want {
			t.Errorf("%s: plan %q err %v, want %q", c.name, planStr(got), err, c.want)
		}
	}
}

func TestParseToolsErrors(t *testing.T) {
	good, err := ParseTools([]byte("tools:\n  - {tool: golang, version: v1.24.2}\n  - nodejs 22.1.0\n"))
	if err != nil || planStr(good) != "go 1.24.2,node 22.1.0" {
		t.Fatalf("good = %q %v", planStr(good), err)
	}
	for _, bad := range []string{"tools:\n  - rust\n", "tools:\n  - cobol 1.0\n", "tools:\n  - rust ../x\n", "tools: [\n"} {
		if _, err := ParseTools([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	if p, err := ParseTools([]byte("sizes: {}\n")); err != nil || len(p) != 0 {
		t.Errorf("no tools: %v %v", p, err)
	}
}
