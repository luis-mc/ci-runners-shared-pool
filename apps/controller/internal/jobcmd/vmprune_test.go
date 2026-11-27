package jobcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seed(t *testing.T, dir, kind, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, kind, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := time.Now().Add(-age)
	if err := os.Chtimes(p, m, m); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVMPruneUsageAndResult(t *testing.T) {
	var o, e strings.Builder
	env := vmEnv(true, &o, &e)
	for _, bad := range [][]string{{"prune", "extra"}, {"prune", "--keep", "0"}, {"prune", "--bogus"}, {"prune", "--cap-gib", "-1"}} {
		if code := VM(bad, env); code != ExitVMUsage {
			t.Errorf("%v: %d", bad, code)
		}
	}
	dir := t.TempDir()
	newest := seed(t, dir, "images", "rocky-9-aaaaaaaaaaaaaaaa.qcow2", time.Hour)
	old := seed(t, dir, "images", "rocky-9-bbbbbbbbbbbbbbbb.qcow2", 48*time.Hour)
	// A dry run names the file and removes nothing.
	o.Reset()
	if code := VM([]string{"prune", "--dry-run", "--cache", dir}, env); code != 0 || !strings.Contains(o.String(), "would remove "+old) {
		t.Fatalf("dry %d %q", code, o.String())
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("dry run removed it")
	}
	o.Reset()
	if code := VM([]string{"prune", "--cache", dir}, env); code != 0 || !strings.Contains(o.String(), "removed "+old) || !strings.Contains(o.String(), "PASS prune") {
		t.Fatalf("prune %d %q", code, o.String())
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatal("the newest went")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("the old one stays")
	}
}

func TestCapComesFromFlagThenEnvThenDefault(t *testing.T) {
	envOf := func(v string) Env { return Env{Getenv: func(string) string { return v }} }
	if n, err := capBytes(0, false, envOf("")); err != nil || n != 60*gib {
		t.Fatalf("default %d %v", n, err)
	}
	if n, err := capBytes(0, false, envOf("7")); err != nil || n != 7*gib {
		t.Fatalf("env %d %v", n, err)
	}
	if n, err := capBytes(3, true, envOf("7")); err != nil || n != 3*gib {
		t.Fatalf("flag %d %v", n, err)
	}
	if _, err := capBytes(0, false, envOf("many")); err == nil {
		t.Fatal("bad env")
	}
}

func TestWarnMemoryPrintsOnlyWhenShort(t *testing.T) {
	var o, e strings.Builder
	env := vmEnv(true, &o, &e)
	warnMemory(env, func() (int64, bool) { return 1500, true }, 0) // default guest 1024 MiB
	if !strings.Contains(e.String(), "warning: the guest needs 1024 MiB") {
		t.Fatalf("%q", e.String())
	}
	e.Reset()
	warnMemory(env, func() (int64, bool) { return 9000, true }, 4096)
	warnMemory(env, func() (int64, bool) { return 0, false }, 4096)
	warnMemory(env, nil, 4096)
	if e.String() != "" {
		t.Fatalf("warned: %q", e.String())
	}
}
