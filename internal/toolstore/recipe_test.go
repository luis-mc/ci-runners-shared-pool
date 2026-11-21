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
		{Windows, ToolNode, "nvm-windows", "NVM_HOME"},
		{Darwin, ToolNode, "nvm", "NVM_DIR"},
		{Windows, ToolPython, "pyenv-win", "PYENV_ROOT"},
		{Darwin, ToolPython, "pyenv", "PYENV_ROOT"},
		{Darwin, ToolJava, "sdkman", "SDKMAN_DIR"},
		{Windows, ToolJava, "archive", ""},
		{Windows, ToolGo, "archive", ""},
		{Darwin, ToolGo, "archive", ""},
		{Windows, ToolDotnet, "dotnet-install", ""},
		{Darwin, ToolDotnet, "dotnet-install", ""},
		{Darwin, ToolXcode, "xcodes", ""},
		{Windows, ToolPnpm, "archive", ""},
	}
	for _, c := range cases {
		dest := d
		if c.goos == Windows {
			dest = w
		}
		r, err := RecipeFor(c.goos, "amd64", Tool{Tool: c.tool, Version: "1.2.3"}, dest)
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
	if _, err := RecipeFor(Windows, "amd64", Tool{Tool: ToolXcode, Version: "16.2"}, "x"); !errors.Is(err, ErrNoRecipe) {
		t.Errorf("xcode on windows: %v", err)
	}
	if _, err := RecipeFor(Darwin, "amd64", Tool{Tool: "cobol", Version: "1"}, "x"); !errors.Is(err, ErrNoRecipe) {
		t.Errorf("unknown tool: %v", err)
	}
	if _, err := RecipeFor("linux", "amd64", Tool{Tool: ToolGo, Version: "1.24.0"}, "x"); err == nil {
		t.Error("linux has no store")
	}
	if _, err := RecipeFor(Darwin, "amd64", Tool{Tool: ToolGo, Version: "../x"}, "x"); err == nil {
		t.Error("bad version")
	}
}

// R5.113: Go and the JDK on Windows are official archives checked against the published SHA-256; dotnet-install has none.
func TestRecipeChecksums(t *testing.T) {
	g, _ := RecipeFor(Windows, "amd64", Tool{Tool: ToolGo, Version: "1.24.0"}, `C:\s`)
	if st := g.Steps[0]; st.URL != "https://go.dev/dl/go1.24.0.windows-amd64.zip" || st.ChecksumSuffix != ".sha256" {
		t.Errorf("go: %+v", st)
	}
	j, _ := RecipeFor(Windows, "amd64", Tool{Tool: ToolJava, Version: "21.0.2"}, `C:\s`)
	if j.Steps[0].ChecksumSuffix == "" {
		t.Error("jdk must be checked")
	}
	n, _ := RecipeFor(Windows, "amd64", Tool{Tool: ToolDotnet, Version: "8.0.100"}, `C:\s`)
	if n.Steps[0].ChecksumSuffix != "" {
		t.Error("dotnet-install publishes no checksum")
	}
}

// R5.117: a job gets the tool's folders and variables, never a manager's home.
func TestLayoutFor(t *testing.T) {
	l := LayoutFor(Windows, Tool{Tool: ToolNode, Version: "22.1.0"}, `C:\s\node\22.1.0`)
	if len(l.Path) != 1 || l.Path[0] != `C:\s\node\22.1.0\nvm\v22.1.0` || len(l.Env) != 0 {
		t.Errorf("node windows: %+v", l)
	}
	x := LayoutFor(Darwin, Tool{Tool: ToolXcode, Version: "16.2"}, "/s/xcode/16.2")
	if x.Env["DEVELOPER_DIR"] != "/s/xcode/16.2/Xcode-16.2.app/Contents/Developer" {
		t.Errorf("xcode: %+v", x)
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
