package container

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// OPS60, runners R5.100: only a Linux ELF of the image's arch is copied.
func TestCheckLinuxBinary(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		arch string
		bad  string
	}{
		{"linux amd64", elfHeader(elf.EM_X86_64), "amd64", ""},
		{"linux arm64", elfHeader(elf.EM_AARCH64), "arm64", ""},
		{"windows exe", append([]byte("MZ\x90\x00"), make([]byte, 80)...), "amd64", "Windows program"},
		{"macos", append([]byte{0xcf, 0xfa, 0xed, 0xfe}, make([]byte, 80)...), "arm64", "macOS program"},
		{"wrong arch", elfHeader(elf.EM_AARCH64), "amd64", "built for"},
		{"text", []byte("#!/bin/sh\necho hi\n"), "amd64", "not an ELF"},
		{"short", []byte("x"), "amd64", "too short"},
		{"unknown arch", elfHeader(elf.EM_X86_64), "riscv", "not supported"},
	}
	for _, c := range cases {
		err := CheckLinuxBinary(writeFile(t, "crsp", c.data), c.arch)
		if c.bad == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrNoLinuxCrsp) || !strings.Contains(err.Error(), c.bad) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if err := CheckLinuxBinary(filepath.Join(t.TempDir(), "none"), "amd64"); !errors.Is(err, ErrNoLinuxCrsp) {
		t.Errorf("missing file: %v", err)
	}
}

type fakeRelease struct {
	archive []byte
	sums    string
	assets  map[string]string
	fetched []string
}

func newFakeRelease(t *testing.T, version, arch string, binary []byte) *fakeRelease {
	t.Helper()
	var buf strings.Builder
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "crsp", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	name := "crsp-linux-" + arch + "_" + version + ".tar.gz"
	sum := sha256.Sum256([]byte(buf.String()))
	return &fakeRelease{
		archive: []byte(buf.String()),
		sums:    hex.EncodeToString(sum[:]) + "  " + name + "\n",
		assets:  map[string]string{name: "https://github.com/x/" + name, "SHA256SUMS": "https://github.com/x/SHA256SUMS"},
	}
}

func (f *fakeRelease) source(dir, version string) LinuxCrspSource {
	return LinuxCrspSource{
		Version: version, DataDir: dir, GOOS: "windows", GOARCH: "amd64",
		Release: func(context.Context, string) (update.Release, error) {
			return update.Release{Version: version, Assets: f.assets}, nil
		},
		Fetch: func(_ context.Context, url, dst string) error {
			f.fetched = append(f.fetched, url)
			if strings.HasSuffix(url, "SHA256SUMS") {
				return os.WriteFile(dst, []byte(f.sums), 0o600)
			}
			return os.WriteFile(dst, f.archive, 0o600)
		},
	}
}

func TestLinuxCrspFetchedOnceAndKept(t *testing.T) {
	rel := newFakeRelease(t, "0.1.18", "amd64", elfHeader(elf.EM_X86_64))
	dir := t.TempDir()
	src := rel.source(dir, "0.1.18")
	p, err := src.Resolve(context.Background(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "linux-crsp", "0.1.18", "amd64", "crsp") {
		t.Fatalf("path %s", p)
	}
	if err := CheckLinuxBinary(p, "amd64"); err != nil {
		t.Fatal(err)
	}
	n := len(rel.fetched)
	if _, err := src.Resolve(context.Background(), "amd64"); err != nil || len(rel.fetched) != n {
		t.Fatalf("second call fetched again: %v %v", err, rel.fetched)
	}
	// A kept file that no longer matches its checksum is fetched again.
	if err := os.WriteFile(p, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Resolve(context.Background(), "amd64"); err != nil || len(rel.fetched) == n {
		t.Fatalf("tampered copy was reused: %v", err)
	}
}

func TestLinuxCrspRefusals(t *testing.T) {
	good := elfHeader(elf.EM_X86_64)
	cases := []struct {
		name string
		edit func(*fakeRelease, *LinuxCrspSource)
		want string
	}{
		{"dev build", func(_ *fakeRelease, s *LinuxCrspSource) { s.Version = "dev" }, "not a release"},
		{"no sums asset", func(f *fakeRelease, _ *LinuxCrspSource) { delete(f.assets, "SHA256SUMS") }, "no SHA256SUMS"},
		{"no linux asset", func(f *fakeRelease, _ *LinuxCrspSource) {
			delete(f.assets, "crsp-linux-amd64_0.1.18.tar.gz")
		}, "has no Crsp-0.1.18-linux-x64"},
		{"sum mismatch", func(f *fakeRelease, _ *LinuxCrspSource) {
			f.sums = strings.Repeat("a", 64) + "  crsp-linux-amd64_0.1.18.tar.gz\n"
		}, "does not match the checksum"},
		{"asset not listed", func(f *fakeRelease, _ *LinuxCrspSource) { f.sums = "" }, "does not list"},
		{"windows binary inside", func(f *fakeRelease, s *LinuxCrspSource) {
			*f = *newFakeRelease(t, "0.1.18", "amd64", append([]byte("MZ"), make([]byte, 80)...))
		}, "Windows program"},
		{"no release", func(_ *fakeRelease, s *LinuxCrspSource) {
			s.Release = func(context.Context, string) (update.Release, error) { return update.Release{}, errors.New("offline") }
		}, "offline"},
	}
	for _, c := range cases {
		rel := newFakeRelease(t, "0.1.18", "amd64", good)
		src := rel.source(t.TempDir(), "0.1.18")
		c.edit(rel, &src)
		if c.name == "windows binary inside" {
			src = rel.source(src.DataDir, "0.1.18")
		}
		_, err := src.Resolve(context.Background(), "amd64")
		if !errors.Is(err, ErrNoLinuxCrsp) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestLinuxCrspOwnBinaryOnSameArchLinuxHost(t *testing.T) {
	own := writeFile(t, "crsp", elfHeader(elf.EM_X86_64))
	s := LinuxCrspSource{GOOS: "linux", GOARCH: "amd64", Exe: func() (string, error) { return own, nil }}
	if p, err := s.Resolve(context.Background(), "amd64"); err != nil || p != own {
		t.Fatalf("%s %v", p, err)
	}
	// A Linux arm64 host building an amd64 image does not use its own file.
	s.GOARCH = "arm64"
	if _, err := s.Resolve(context.Background(), "amd64"); !errors.Is(err, ErrNoLinuxCrsp) {
		t.Fatalf("own arm64 binary was offered for amd64: %v", err)
	}
}

// OPS60: the build never copies the controller's own Windows binary.
func TestStageRefusesWindowsBinary(t *testing.T) {
	r, root := stagedRig(t)
	r.b.cfg.CrspHostPath = writeFile(t, "crsp.exe", append([]byte("MZ"), make([]byte, 80)...))
	_, err := r.b.stageBuild(context.Background(), "k1", runtime.GOARCH)
	if !errors.Is(err, ErrNoLinuxCrsp) || !strings.Contains(err.Error(), "Windows program") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "k1")); !os.IsNotExist(err) {
		t.Fatal("the build folder stayed")
	}
}

func TestStageUsesLinuxCrspHook(t *testing.T) {
	r, _ := stagedRig(t)
	r.b.cfg.CrspHostPath = ""
	var got string
	hook := writeFile(t, "linux-crsp", elfHeader(elf.EM_AARCH64))
	r.b.cfg.LinuxCrsp = func(_ context.Context, arch string) (string, error) { got = arch; return hook, nil }
	dir, err := r.b.stageBuild(context.Background(), "k2", "arm64")
	if err != nil || got != "arm64" {
		t.Fatalf("%v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "linux-base", "crsp")); err != nil {
		t.Fatal(err)
	}
}
