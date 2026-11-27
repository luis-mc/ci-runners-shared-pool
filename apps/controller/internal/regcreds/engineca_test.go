package regcreds

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

type memHost struct {
	files map[string][]byte
	dirs  map[string]bool
	deny  string // a path prefix that refuses writes
}

func newMemHost(dirs ...string) *memHost {
	h := &memHost{files: map[string][]byte{}, dirs: map[string]bool{}}
	for _, d := range dirs {
		h.dirs[d] = true
	}
	return h
}

func (h *memHost) MkdirAll(p string, _ fs.FileMode) error {
	if h.deny != "" && len(p) >= len(h.deny) && p[:len(h.deny)] == h.deny {
		return fs.ErrPermission
	}
	h.dirs[p] = true
	return nil
}

func (h *memHost) WriteFile(p string, d []byte, _ fs.FileMode) error {
	if h.deny != "" && len(p) >= len(h.deny) && p[:len(h.deny)] == h.deny {
		return fs.ErrPermission
	}
	h.files[p] = append([]byte(nil), d...)
	return nil
}

func (h *memHost) ReadFile(p string) ([]byte, error) {
	if b, ok := h.files[p]; ok {
		return b, nil
	}
	return nil, fs.ErrNotExist
}

func (h *memHost) Exists(p string) bool { _, f := h.files[p]; return f || h.dirs[p] }

// R5.12: the CA goes where Docker and Podman read it, for the host:port.
func TestEngineCALinux(t *testing.T) {
	ca := caPEM(t)
	h := newMemHost("/etc/docker", "/etc/containers")
	res, err := InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "ctl.lan:8460", CA: ca})
	if err != nil || len(res.Written) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, p := range []string{"/etc/docker/certs.d/ctl.lan:8460/ca.crt", "/etc/containers/certs.d/ctl.lan:8460/ca.crt"} {
		if string(h.files[p]) != string(ca) {
			t.Errorf("%s not written", p)
		}
	}
	// again: nothing is written
	res, err = InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "ctl.lan:8460", CA: ca})
	if err != nil || len(res.Written) != 0 || len(res.Unchanged) != 2 {
		t.Fatalf("second run %+v %v", res, err)
	}
	// a new CA replaces the old file
	ca2 := caPEM(t)
	res, _ = InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "ctl.lan:8460", CA: ca2})
	if len(res.Written) != 2 || string(h.files["/etc/docker/certs.d/ctl.lan:8460/ca.crt"]) != string(ca2) {
		t.Fatalf("replace %+v", res)
	}
}

func TestEngineCAEngineFolders(t *testing.T) {
	ca := caPEM(t)
	// Podman only: Docker's folder is not made.
	h := newMemHost("/etc/containers")
	if _, err := InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "10.0.0.5:8460", CA: ca}); err != nil {
		t.Fatal(err)
	}
	if len(h.files) != 1 || h.files["/etc/containers/certs.d/10.0.0.5:8460/ca.crt"] == nil {
		t.Fatalf("files = %v", h.files)
	}
	// Neither: Docker's.
	h = newMemHost()
	_, _ = InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "10.0.0.5:8460", CA: ca})
	if len(h.files) != 1 || h.files["/etc/docker/certs.d/10.0.0.5:8460/ca.crt"] == nil {
		t.Fatalf("files = %v", h.files)
	}
}

// Docker Desktop reads the folder of the user's home; Windows has no colon in a folder name.
func TestEngineCADockerDesktop(t *testing.T) {
	ca := caPEM(t)
	for _, c := range []struct{ goos, home, dir string }{
		{"darwin", "/Users/me", "10.0.0.5:8460"},
		{"windows", `C:\Users\me`, "10.0.0.5_8460"},
	} {
		h := newMemHost()
		if _, err := InstallEngineCA(h, EngineCAOptions{GOOS: c.goos, Home: c.home, Host: "10.0.0.5:8460", CA: ca}); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(c.home, ".docker", "certs.d", c.dir, "ca.crt")
		if h.files[want] == nil {
			t.Errorf("%s: files = %v, want %s", c.goos, h.files, want)
		}
	}
	if _, err := InstallEngineCA(newMemHost(), EngineCAOptions{GOOS: "windows", Host: "10.0.0.5:8460", CA: ca}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no home: %v", err)
	}
}

// The step takes only a certificate, only a good host, and says why a write is refused.
func TestEngineCARefusals(t *testing.T) {
	ca := caPEM(t)
	h := newMemHost()
	for name, o := range map[string]EngineCAOptions{
		"no CA":      {GOOS: "linux", Host: "ctl.lan:8460"},
		"a key":      {GOOS: "linux", Host: "ctl.lan:8460", CA: []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")},
		"bad host":   {GOOS: "linux", Host: "ctl lan/../x", CA: ca},
		"path host":  {GOOS: "linux", Host: "../etc", CA: ca},
		"unknown OS": {GOOS: "plan9", Host: "ctl.lan:8460", CA: ca},
	} {
		if _, err := InstallEngineCA(h, o); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(h.files) != 0 {
		t.Fatalf("files = %v", h.files)
	}
	h.deny = "/etc/docker"
	if _, err := InstallEngineCA(h, EngineCAOptions{GOOS: "linux", Host: "ctl.lan:8460", CA: ca}); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("a refused write: %v", err)
	}
}
