package logs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONFieldsAndRotation(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	l, c, err := Setup(Options{Dir: dir, Mode: "controller", Version: "1.2.3", Foreground: true, Stderr: &stderr, MaxSize: 400, MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	l = With(l, IDs{Machine: "m1", Job: "j9"})
	l.Info("first line")
	data, err := os.ReadFile(filepath.Join(dir, "controller.log"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &m); err != nil {
		t.Fatalf("not JSON: %s", data)
	}
	for k, want := range map[string]string{"msg": "first line", "component": "controller", "version": "1.2.3", "mode": "controller", "machine": "m1", "job": "j9"} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
	if _, ok := m["time"]; !ok || m["level"] != "INFO" {
		t.Errorf("time or level missing: %v", m)
	}
	if _, ok := m["runner"]; ok {
		t.Error("an id without a value was written")
	}
	if !strings.Contains(stderr.String(), "first line") {
		t.Errorf("foreground stderr: %q", stderr.String())
	}
	for i := 0; i < 20; i++ {
		l.Info("filler line to pass the size", "n", i)
	}
	_ = c.Close()
	for _, name := range []string{"controller.log", "controller.log.1", "controller.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "controller.log.3")); err == nil {
		t.Error("more files kept than MaxFiles")
	}
}

func TestBackgroundHasNoStderrAndBadFolderFallsBack(t *testing.T) {
	var stderr bytes.Buffer
	l, _, err := Setup(Options{Dir: t.TempDir(), Mode: "agent", Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("quiet")
	if stderr.Len() != 0 {
		t.Errorf("background wrote to stderr: %q", stderr.String())
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, _, err = Setup(Options{Dir: filepath.Join(blocker, "logs"), Mode: "agent", Stderr: &stderr})
	if err == nil {
		t.Fatal("want the open error")
	}
	l.Info("still logs")
	if !strings.Contains(stderr.String(), "still logs") {
		t.Errorf("no fallback to stderr: %q", stderr.String())
	}
}
