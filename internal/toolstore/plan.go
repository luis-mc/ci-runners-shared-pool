package toolstore

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry"
)

// Tool names of a plan. They are the names the Linux profiles use.
const (
	ToolNode   = "node"
	ToolPnpm   = "pnpm"
	ToolRust   = "rust"
	ToolGo     = "go"
	ToolPython = "python"
	ToolJava   = "java"
	ToolDotnet = "dotnet"
	ToolXcode  = "xcode"
)

// Plan is the tool plan of a job: tool and version, sorted, one version of a tool (the newest word of the
// strongest source). A tool can have several versions in the plan only when .crsp.yml lists them.
type Plan []registry.Toolchain

// versionShape keeps a version usable as a folder name: no separators, no "..", no spaces.
var versionShape = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

// ValidVersion reports whether v can be a version folder of the store.
func ValidVersion(v string) bool { return versionShape.MatchString(v) && !strings.Contains(v, "..") }

var toolAlias = map[string]string{
	"nodejs": ToolNode, "node": ToolNode, "golang": ToolGo, "go": ToolGo, "python": ToolPython, "python3": ToolPython,
	"rust": ToolRust, "rustc": ToolRust, "rustup": ToolRust, "java": ToolJava, "jdk": ToolJava,
	"dotnet": ToolDotnet, "dotnet-core": ToolDotnet, "pnpm": ToolPnpm, "xcode": ToolXcode,
}

// CanonTool maps the names other tools use (nodejs, golang) to a plan tool name; "" when unknown.
func CanonTool(name string) string { return toolAlias[strings.ToLower(strings.TrimSpace(name))] }

// BuildPlan makes the tool plan from the stack files the scan read and the .crsp.yml text (nil when the
// repository has none). Order of strength: tools: in .crsp.yml, then a version file or manifest, then the
// default version of the stack's standard profile (profiles.Defaults). A bad tools: entry is an error; the
// plan built so far is still returned so a caller can show it.
func BuildPlan(files []registry.StackFile, crspYML []byte) (Plan, error) {
	got := map[string][]string{}
	set := func(tool, v string) {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if tool != "" && ValidVersion(v) {
			got[tool] = []string{v}
		}
	}
	// Defaults for the stacks that show.
	stacks := map[string]bool{}
	for _, s := range profiles.DetectStacks(files) {
		stacks[s] = true
	}
	for _, p := range profiles.Defaults() {
		for _, s := range p.Stacks {
			if stacks[s] {
				for _, tc := range p.Toolchains {
					set(tc.Tool, tc.Version)
				}
			}
		}
	}
	// Version files and manifests.
	for _, f := range files {
		readVersionFile(f, set)
	}
	var err error
	if len(crspYML) > 0 {
		var extra Plan
		extra, err = ParseTools(crspYML)
		for _, t := range extra {
			got[t.Tool] = appendOnce(got[t.Tool], t.Version, len(got[t.Tool]) > 0 && !listed(extra, t.Tool, got[t.Tool][0]))
		}
	}
	var out Plan
	for tool, vs := range got {
		for _, v := range vs {
			out = append(out, registry.Toolchain{Tool: tool, Version: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tool != out[j].Tool {
			return out[i].Tool < out[j].Tool
		}
		return out[i].Version < out[j].Version
	})
	return out, err
}

func listed(extra Plan, tool, v string) bool {
	for _, t := range extra {
		if t.Tool == tool && t.Version == v {
			return true
		}
	}
	return false
}

// appendOnce adds v; with replace true (the tool had a weaker version) the list restarts at v.
func appendOnce(vs []string, v string, replace bool) []string {
	if replace {
		return []string{v}
	}
	for _, x := range vs {
		if x == v {
			return vs
		}
	}
	return append(vs, v)
}

var (
	reGoToolch  = regexp.MustCompile(`(?m)^\s*toolchain\s+go(\d[0-9.]*)`)
	reGoLine    = regexp.MustCompile(`(?m)^\s*go\s+(\d[0-9.]*)\s*$`)
	reRustChan  = regexp.MustCompile(`(?m)^\s*channel\s*=\s*["']?(\d+\.\d+(?:\.\d+)?)["']?`)
	reSdkmanrc  = regexp.MustCompile(`(?m)^\s*java\s*=\s*([0-9][0-9.]*)`)
	reGlobalSDK = regexp.MustCompile(`"version"\s*:\s*"(\d+\.\d+\.\d+)`)
	rePkgPnpm   = regexp.MustCompile(`"packageManager"\s*:\s*"pnpm@(\d+\.\d+\.\d+)`)
	reDigits    = regexp.MustCompile(`^\d+(\.\d+){0,2}$`)
)

// readVersionFile reads the version out of one file the scan read; a file it cannot read gives nothing.
func readVersionFile(f registry.StackFile, set func(tool, v string)) {
	txt := string(f.Content)
	first := strings.TrimSpace(strings.SplitN(txt, "\n", 2)[0])
	switch path.Base(f.Path) {
	case ".nvmrc", ".node-version":
		if v := strings.TrimPrefix(first, "v"); reDigits.MatchString(v) {
			set(ToolNode, v)
		}
	case ".python-version":
		if reDigits.MatchString(first) {
			set(ToolPython, first)
		}
	case ".java-version":
		if reDigits.MatchString(first) {
			set(ToolJava, first)
		}
	case ".sdkmanrc":
		if m := reSdkmanrc.FindStringSubmatch(txt); m != nil {
			set(ToolJava, m[1])
		}
	case "rust-toolchain.toml":
		if m := reRustChan.FindStringSubmatch(txt); m != nil {
			set(ToolRust, m[1])
		}
	case "rust-toolchain":
		if reDigits.MatchString(first) {
			set(ToolRust, first)
		}
	case "go.mod":
		// A toolchain line wins over the go line; a "go 1.24" line names a language version, and the
		// store installs it as "1.24.0".
		if m := reGoToolch.FindStringSubmatch(txt); m != nil {
			set(ToolGo, m[1])
		} else if m := reGoLine.FindStringSubmatch(txt); m != nil {
			v := m[1]
			if strings.Count(v, ".") == 1 {
				v += ".0"
			}
			set(ToolGo, v)
		}
	case "global.json":
		if m := reGlobalSDK.FindStringSubmatch(txt); m != nil {
			set(ToolDotnet, m[1])
		}
	case "package.json":
		if m := rePkgPnpm.FindStringSubmatch(txt); m != nil {
			set(ToolPnpm, m[1])
		}
	case ".tool-versions":
		for _, ln := range strings.Split(txt, "\n") {
			w := strings.Fields(strings.SplitN(ln, "#", 2)[0])
			if len(w) >= 2 {
				if t := CanonTool(w[0]); t != "" && reDigits.MatchString(strings.TrimPrefix(w[1], "v")) {
					set(t, w[1])
				}
			}
		}
	}
}

// ParseTools reads the tools: list of .crsp.yml. An entry is "rust 1.85", "rust@1.85" or a map with tool
// and version. A name or version that is not valid is an error that names the entry; the valid entries
// before it are returned.
func ParseTools(data []byte) (Plan, error) {
	var doc struct {
		Tools []yaml.Node `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf(".crsp.yml is not valid YAML: %w", err)
	}
	var out Plan
	for i := range doc.Tools {
		n := &doc.Tools[i]
		var tool, ver string
		switch n.Kind {
		case yaml.ScalarNode:
			w := strings.FieldsFunc(n.Value, func(r rune) bool { return r == ' ' || r == '@' || r == '=' })
			if len(w) == 2 {
				tool, ver = w[0], w[1]
			}
		case yaml.MappingNode:
			var m struct{ Tool, Version string }
			if n.Decode(&m) == nil {
				tool, ver = m.Tool, m.Version
			}
		}
		ver = strings.TrimPrefix(ver, "v")
		canon := CanonTool(tool)
		if canon == "" || !ValidVersion(ver) {
			return out, fmt.Errorf(".crsp.yml: tools entry %d is not a known tool and a version (for example \"rust 1.85\")", i+1)
		}
		out = append(out, registry.Toolchain{Tool: canon, Version: ver})
	}
	return out, nil
}
