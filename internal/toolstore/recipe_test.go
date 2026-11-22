package toolstore

import (
	"errors"
	"strings"
	"testing"
)

// R5.112: each tool uses its own manager, every manager home is inside the version folder.
func TestRecipeFor(t *testing.T) {
	const w, d = `C:\store\.staging\x`, "/store/.staging/x"
	cases := []struct {
		goos, tool, manager, inEnv string
	}{
		{Windows, ToolRust, "rustup", "RUSTUP_HOME"},
		{Darwin, ToolRust, "rustup", "CARGO_HOME"},
		{Windows, ToolNode, "archive", ""},
		{Darwin, ToolNode, "archive", ""},
		{Windows, ToolPython, "pyenv-win", "PYENV_ROOT"},
		{Darwin, ToolPython, "pyenv", "PYENV_ROOT"},
		{Darwin, ToolJava, "sdkman", "SDKMAN_DIR"},
		{Windows, ToolJava, "archive", ""},
		{Windows, ToolGo, "archive", ""},
		{Darwin, ToolGo, "archive", ""},
		{Windows, ToolDotnet, "dotnet-install", ""},
		{Darwin, ToolDotnet, "dotnet-install", ""},
		{Windows, ToolPnpm, "archive", ""},
	}
	for _, c := range cases {
		dest := d
		if c.goos == Windows {
			dest = w
		}
		r, err := RecipeFor(c.goos, "amd64", DefaultSources(), Tool{Tool: c.tool, Version: "1.2.3"}, dest)
		if err != nil || r.Manager != c.manager || len(r.Steps) == 0 {
			t.Errorf("%s %s: %+v %v", c.goos, c.tool, r, err)
			continue
		}
		for _, st := range r.Steps {
			if c.inEnv != "" && st.Kind == StepRun {
				if v := st.Env[c.inEnv]; !strings.HasPrefix(v, dest) {
					t.Errorf("%s %s: %s = %q is not inside %s", c.goos, c.tool, c.inEnv, v, dest)
				}
			}
		}
	}
}

func TestRecipeForRefuses(t *testing.T) {
	for _, goos := range []string{Windows, Darwin} { // the owner installs Xcode: no recipe, so no xcodes path
		if _, err := RecipeFor(goos, "amd64", DefaultSources(), Tool{Tool: ToolXcode, Version: "16.2"}, "x"); !errors.Is(err, ErrNoRecipe) {
			t.Errorf("xcode on %s: %v", goos, err)
		}
	}
	if _, err := RecipeFor(Darwin, "amd64", DefaultSources(), Tool{Tool: "cobol", Version: "1"}, "x"); !errors.Is(err, ErrNoRecipe) {
		t.Errorf("unknown tool: %v", err)
	}
	if _, err := RecipeFor("linux", "amd64", DefaultSources(), Tool{Tool: ToolGo, Version: "1.24.0"}, "x"); err == nil {
		t.Error("linux has no store")
	}
	if _, err := RecipeFor(Darwin, "amd64", DefaultSources(), Tool{Tool: ToolGo, Version: "../x"}, "x"); err == nil {
		t.Error("bad version")
	}
}

// R5.113: Go and the JDK on Windows are official archives checked against the published SHA-256; dotnet-install has none.
func TestRecipeChecksums(t *testing.T) {
	g, _ := RecipeFor(Windows, "amd64", DefaultSources(), Tool{Tool: ToolGo, Version: "1.24.0"}, `C:\s`)
	if st := g.Steps[0]; st.URL != "https://go.dev/dl/go1.24.0.windows-amd64.zip" || st.ChecksumSuffix != ".sha256" {
		t.Errorf("go: %+v", st)
	}
	j, _ := RecipeFor(Windows, "amd64", DefaultSources(), Tool{Tool: ToolJava, Version: "21.0.2"}, `C:\s`)
	if j.Steps[0].ChecksumSuffix == "" {
		t.Error("jdk must be checked")
	}
	n, _ := RecipeFor(Windows, "amd64", DefaultSources(), Tool{Tool: ToolDotnet, Version: "8.0.100"}, `C:\s`)
	if n.Steps[0].ChecksumSuffix != "" {
		t.Error("dotnet-install publishes no checksum")
	}
}

// R5.117: a job gets the tool's folders and variables, never a manager's home.
func TestLayoutFor(t *testing.T) {
	l := LayoutFor(Windows, Tool{Tool: ToolNode, Version: "22.1.0"}, `C:\s\node\22.1.0`)
	if len(l.Path) != 1 || l.Path[0] != `C:\s\node\22.1.0` || len(l.Env) != 0 {
		t.Errorf("node windows: %+v", l)
	}
	if l := LayoutFor(Darwin, Tool{Tool: ToolNode, Version: "22.1.0"}, "/s/node/22.1.0"); len(l.Path) != 1 || l.Path[0] != "/s/node/22.1.0/bin" {
		t.Errorf("node darwin: %+v", l)
	}
	for _, tool := range []string{ToolRust, ToolPython, ToolJava, ToolGo, ToolDotnet} {
		l := LayoutFor(Darwin, Tool{Tool: tool, Version: "1.0.0"}, "/s/"+tool)
		for k := range l.Env {
			if managerVar(k) {
				t.Errorf("%s exports manager variable %s", tool, k)
			}
		}
	}
}

// R5.112 and R5.113 (owner decision 22 Nov): Node is the official archive of the version for the OS and CPU,
// checked against that version's SHASUMS256.txt, from the base in Sources; no version manager.
func TestNodeRecipe(t *testing.T) {
	cases := []struct{ goos, arch, file string }{
		{Windows, "amd64", "node-v22.1.0-win-x64.zip"},
		{Windows, "arm64", "node-v22.1.0-win-arm64.zip"},
		{Darwin, "amd64", "node-v22.1.0-darwin-x64.tar.gz"},
		{Darwin, "arm64", "node-v22.1.0-darwin-arm64.tar.gz"},
	}
	for _, c := range cases {
		r, err := RecipeFor(c.goos, c.arch, DefaultSources(), Tool{Tool: ToolNode, Version: "22.1.0"}, "x")
		if err != nil || r.Manager != "archive" || len(r.Steps) != 2 {
			t.Fatalf("%s %s: %+v %v", c.goos, c.arch, r, err)
		}
		d := r.Steps[0]
		if d.URL != "https://nodejs.org/dist/v22.1.0/"+c.file || d.SumsURL != "https://nodejs.org/dist/v22.1.0/SHASUMS256.txt" || d.SumsName != c.file || d.To != c.file {
			t.Errorf("%s %s: %+v", c.goos, c.arch, d)
		}
	}
	src := DefaultSources()
	src.NodeDist = "https://mirror.example/node"
	r, _ := RecipeFor(Darwin, "arm64", src, Tool{Tool: ToolNode, Version: "20.0.0"}, "x")
	if !strings.HasPrefix(r.Steps[0].URL, "https://mirror.example/node/v20.0.0/") {
		t.Errorf("mirror not used: %+v", r.Steps[0])
	}
}
