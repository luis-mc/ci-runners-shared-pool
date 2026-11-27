package engineconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paths(t *testing.T) Paths {
	d := t.TempDir()
	return Paths{DockerDaemon: filepath.Join(d, "docker", "daemon.json"), ContainerdCerts: filepath.Join(d, "certs.d"),
		PodmanRegistries: filepath.Join(d, "registries.conf.d")}
}

func on() Settings {
	return Settings{Host: "ctl.lan:8443", Enabled: true, Upstreams: []string{"docker.io", "ghcr.io"}}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyWritesAllThreeEnginesOnce(t *testing.T) {
	p := paths(t)
	ch, err := Apply(p, on())
	if err != nil || len(ch) != 4 { // daemon.json, two hosts.toml, one drop-in
		t.Fatalf("changed %v err %v", ch, err)
	}
	if d := read(t, p.DockerDaemon); !strings.Contains(d, `"https://ctl.lan:8443/mirror/docker.io"`) || strings.Contains(d, "ghcr.io") {
		t.Fatalf("daemon.json: %s", d)
	}
	h := read(t, filepath.Join(p.ContainerdCerts, "ghcr.io", "hosts.toml"))
	for _, want := range []string{`server = "https://ghcr.io"`, `[host."https://ctl.lan:8443/mirror/ghcr.io/v2"]`, "override_path = true"} {
		if !strings.Contains(h, want) {
			t.Fatalf("hosts.toml lacks %q:\n%s", want, h)
		}
	}
	if h := read(t, filepath.Join(p.ContainerdCerts, "docker.io", "hosts.toml")); !strings.Contains(h, `server = "https://registry-1.docker.io"`) {
		t.Fatal(h)
	}
	if c := read(t, filepath.Join(p.PodmanRegistries, "50-crsp-mirror.conf")); !strings.Contains(c, `location = "ctl.lan:8443/mirror/ghcr.io"`) {
		t.Fatal(c)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	p := paths(t)
	if _, err := Apply(p, on()); err != nil {
		t.Fatal(err)
	}
	before := read(t, p.DockerDaemon)
	ch, err := Apply(p, on())
	if err != nil || len(ch) != 0 {
		t.Fatalf("second apply changed %v err %v", ch, err)
	}
	if read(t, p.DockerDaemon) != before {
		t.Fatal("daemon.json changed")
	}
}

func TestDockerKeepsOtherSettingsAndMirrors(t *testing.T) {
	p := paths(t)
	if err := os.MkdirAll(filepath.Dir(p.DockerDaemon), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DockerDaemon, []byte(`{"log-driver":"json-file","registry-mirrors":["https://other.example"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p, on()); err != nil {
		t.Fatal(err)
	}
	d := read(t, p.DockerDaemon)
	if !strings.Contains(d, "json-file") || !strings.Contains(d, "https://other.example") || !strings.Contains(d, "/mirror/docker.io") {
		t.Fatal(d)
	}
	// Off removes ours and nothing else.
	off := on()
	off.Enabled = false
	if _, err := Apply(p, off); err != nil {
		t.Fatal(err)
	}
	d = read(t, p.DockerDaemon)
	if strings.Contains(d, "/mirror/") || !strings.Contains(d, "https://other.example") || !strings.Contains(d, "json-file") {
		t.Fatal(d)
	}
	if _, err := os.Stat(filepath.Join(p.ContainerdCerts, "ghcr.io", "hosts.toml")); !os.IsNotExist(err) {
		t.Fatal("hosts.toml stays after off")
	}
	if ch, _ := Apply(p, off); len(ch) != 0 {
		t.Fatalf("off twice changed %v", ch)
	}
}

func TestRemovedUpstreamLosesItsFileButAForeignFileStays(t *testing.T) {
	p := paths(t)
	if _, err := Apply(p, on()); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(p.ContainerdCerts, "quay.io", "hosts.toml")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("server = \"https://quay.io\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := on()
	s.Upstreams = []string{"docker.io"}
	if _, err := Apply(p, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.ContainerdCerts, "ghcr.io", "hosts.toml")); !os.IsNotExist(err) {
		t.Fatal("ghcr.io file stays")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("foreign file removed")
	}
}

func TestForeignHostsTomlIsNotOverwritten(t *testing.T) {
	p := paths(t)
	f := filepath.Join(p.ContainerdCerts, "ghcr.io", "hosts.toml")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("server = \"https://ghcr.io\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p, on()); err == nil {
		t.Fatal("a file crsp does not own was overwritten")
	}
	if read(t, f) != "server = \"https://ghcr.io\"\n" {
		t.Fatal("foreign file changed")
	}
}

func TestParseArgs(t *testing.T) {
	s, err := ParseArgs([]string{"ctl.lan:8443", "on", "docker.io, GHCR.io,"})
	if err != nil || !s.Enabled || len(s.Upstreams) != 2 || s.Upstreams[1] != "ghcr.io" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range [][]string{{"h"}, {"h", "maybe", ""}, {"h/x", "on", ""}, {"h", "on", "a/b"}} {
		if _, err := ParseArgs(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
