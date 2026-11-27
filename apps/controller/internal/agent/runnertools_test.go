package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func fakeTools(path string, files map[string]string) ToolsEnv {
	return ToolsEnv{
		GOOS: "linux",
		ReadFile: func(p string) ([]byte, error) {
			if path == "" {
				return nil, errors.New("no such file")
			}
			return []byte(path), nil
		},
		Exists: func(p string) bool { _, ok := files[p]; return ok },
		Version: func(_ context.Context, p string) (string, error) {
			return files[p], nil
		},
	}
}

func TestProbeRunnerToolsFindsOnRunnerPath(t *testing.T) {
	files := map[string]string{
		filepath.Join("/usr/bin", "git"):      "git version 2.45.0\nextra",
		filepath.Join("/opt/cargo", "cargo"):  "cargo 1.80.0",
		filepath.Join("/usr/bin", "python3"):  "Python 3.12.1",
		filepath.Join("/elsewhere", "docker"): "Docker version 27",
	}
	rep := ProbeRunnerTools(context.Background(), "/r/a", fakeTools("/usr/bin:/opt/cargo", files))
	if rep.Err != "" {
		t.Fatal(rep.Err)
	}
	want := map[string]ToolResult{
		"git":    {"git", true, "git version 2.45.0"},
		"cargo":  {"cargo", true, "cargo 1.80.0"},
		"python": {"python", true, "Python 3.12.1"},
		"docker": {"docker", false, ""},
		"node":   {"node", false, ""},
	}
	got := map[string]ToolResult{}
	for _, r := range rep.Tools {
		got[r.Name] = r
	}
	if len(rep.Tools) != len(RunnerTools) {
		t.Fatalf("%d tools, want %d", len(rep.Tools), len(RunnerTools))
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s = %+v, want %+v", n, got[n], w)
		}
	}
}

func TestProbeRunnerToolsWindowsExtensionsAndMissingPathFile(t *testing.T) {
	files := map[string]string{filepath.Join(`C:\bin`, "cargo.exe"): "cargo 1.80.0"}
	env := fakeTools(`C:\bin;;C:\other`, files)
	env.GOOS = "windows"
	rep := ProbeRunnerTools(context.Background(), "/r/a", env)
	for _, r := range rep.Tools {
		if r.Name == "cargo" && !r.Found {
			t.Error("cargo.exe not found")
		}
	}
	if rep := ProbeRunnerTools(context.Background(), "/r/a", fakeTools("", nil)); rep.Err == "" || len(rep.Tools) != 0 {
		t.Errorf("unreadable PATH gave %+v, want Err and no tools", rep)
	}
}
