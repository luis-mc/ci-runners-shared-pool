package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// RunnerTools are the common tools the same-label tool check compares
// (doctor R5.63, runners R5.102).
var RunnerTools = []string{"git", "node", "npm", "pnpm", "go", "cargo", "rustc", "python", "docker"}

// ToolResult is one tool seen on a runner's PATH: found or not, and the first
// line of its version output. It never carries an environment variable value.
type ToolResult struct {
	Name    string
	Found   bool
	Version string
}

// ToolsReport is what the agent saw of one runner. Err is set when the probe
// could not run (for example the runner's PATH could not be read).
type ToolsReport struct {
	Err   string
	Tools []ToolResult
}

// ToolsEnv is every OS call the tool probe makes, so tests feed fixed answers.
type ToolsEnv struct {
	GOOS string
	// ReadFile reads a file of the runner folder.
	ReadFile func(path string) ([]byte, error)
	// Exists says whether a file exists.
	Exists func(path string) bool
	// Version runs `path --version` with a short timeout and returns its output.
	Version func(ctx context.Context, path string) (string, error)
}

// ProbeRunnerTools looks for each of RunnerTools on the PATH the runner
// service sees: the `.path` file in the runner folder, which the runner writes
// when it is configured and reads at every start. It reads only; nothing in the
// report is an environment variable value other than the tool's own version line.
func ProbeRunnerTools(ctx context.Context, runnerDir string, env ToolsEnv) ToolsReport {
	raw, err := env.ReadFile(filepath.Join(runnerDir, ".path"))
	if err != nil {
		return ToolsReport{Err: "the runner's PATH could not be read: " + err.Error()}
	}
	sep, exts := ":", []string{""}
	if env.GOOS == "windows" {
		sep, exts = ";", []string{".exe", ".cmd", ".bat"}
	}
	var dirs []string
	for _, d := range strings.Split(strings.TrimSpace(string(raw)), sep) {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	rep := ToolsReport{}
	for _, name := range RunnerTools {
		res := ToolResult{Name: name}
		if path, ok := findTool(env, dirs, name, exts); ok {
			res.Found = true
			if out, err := env.Version(ctx, path); err == nil {
				res.Version, _, _ = strings.Cut(strings.TrimSpace(out), "\n")
				res.Version = strings.TrimSpace(res.Version)
			}
		}
		rep.Tools = append(rep.Tools, res)
	}
	return rep
}

func findTool(env ToolsEnv, dirs []string, name string, exts []string) (string, bool) {
	names := []string{name}
	if name == "python" {
		names = append(names, "python3")
	}
	for _, d := range dirs {
		for _, n := range names {
			for _, e := range exts {
				p := filepath.Join(d, n+e)
				if env.Exists(p) {
					return p, true
				}
			}
		}
	}
	return "", false
}
