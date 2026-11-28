package hooks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hooks"
)

func hubEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyHubConfigWritesCargoAndAptAndIsRepeatable(t *testing.T) {
	apt, cargo := t.TempDir(), t.TempDir()
	write(t, filepath.Join(apt, "sources.list.d", "ubuntu.sources"), "URIs: http://archive.ubuntu.com/ubuntu\n")
	write(t, filepath.Join(apt, "sources.list.d", "docker.list"), "deb https://download.docker.com/linux/ubuntu noble stable\n")
	env := hubEnv(map[string]string{
		"CRSP_HUB_CARGO_INDEX": "https://10.0.0.5:8460/pkg/crates/o__r/t/",
		"CARGO_HTTP_CAINFO":    "/r/combined.pem",
		"CRSP_HUB_APT_MIRROR":  "https://10.0.0.5:8460/pkg/apt/o__r/t",
		"CRSP_HUB_CA":          "/r/ca.pem",
	})
	f := hooks.HubFiles{AptDir: apt, CargoHome: cargo}
	for i := 0; i < 2; i++ {
		if lines := hooks.ApplyHubConfig(env, f); len(lines) != 0 {
			t.Fatalf("lines: %v", lines)
		}
	}
	if c := read(t, filepath.Join(cargo, "config.toml")); !strings.Contains(c, `registry = "sparse+https://10.0.0.5:8460/pkg/crates/o__r/t/"`) {
		t.Fatalf("cargo config:\n%s", c)
	}
	if c := read(t, filepath.Join(apt, "apt.conf.d", "90crsp-hub")); !strings.Contains(c, `Acquire::https::10.0.0.5::CaInfo "/r/ca.pem";`) {
		t.Fatalf("apt conf:\n%s", c)
	}
	if s := read(t, filepath.Join(apt, "sources.list.d", "ubuntu.sources")); s != "URIs: https://10.0.0.5:8460/pkg/apt/o__r/t/archive.ubuntu.com/ubuntu\n" {
		t.Fatalf("sources:\n%s", s)
	}
	if s := read(t, filepath.Join(apt, "sources.list.d", "docker.list")); !strings.Contains(s, "download.docker.com") {
		t.Fatalf("a third-party source must stay:\n%s", s)
	}
}

func TestApplyHubConfigKeepsAUsersOwnCargoConfigAndSaysSo(t *testing.T) {
	cargo := t.TempDir()
	write(t, filepath.Join(cargo, "config.toml"), "[build]\njobs = 2\n")
	lines := hooks.ApplyHubConfig(hubEnv(map[string]string{"CRSP_HUB_CARGO_INDEX": "https://h/pkg/crates/o__r/t/"}),
		hooks.HubFiles{AptDir: "-", CargoHome: cargo})
	if len(lines) != 1 || !strings.Contains(lines[0], "user's own") {
		t.Fatalf("lines: %v", lines)
	}
	if read(t, filepath.Join(cargo, "config.toml")) != "[build]\njobs = 2\n" {
		t.Fatal("the user's config was changed")
	}
}

func TestApplyHubConfigDoesNothingWithoutTheHubValues(t *testing.T) {
	apt := t.TempDir()
	if lines := hooks.ApplyHubConfig(hubEnv(nil), hooks.HubFiles{AptDir: apt, CargoHome: t.TempDir()}); len(lines) != 0 {
		t.Fatalf("lines: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(apt, "apt.conf.d")); err == nil {
		t.Fatal("apt files written without a hub")
	}
}
