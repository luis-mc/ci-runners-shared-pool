package vmcheck_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func TestQemuAndOverlayArgs(t *testing.T) {
	s, err := vmcheck.BootSpec{Distro: "rocky-9", CacheDir: "/c", WorkDir: "/w"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if s.MemMB != 1024 || s.CPUs != 2 || s.Timeout != 5*time.Minute || s.Ready != "login:" {
		t.Fatalf("defaults %+v", s)
	}
	got := vmcheck.QemuArgs(s, "/w/o.qcow2", "/w/s.log")
	want := []string{"-enable-kvm", "-cpu", "host", "-m", "1024", "-smp", "2",
		"-drive", "file=/w/o.qcow2,if=virtio,format=qcow2", "-nic", "user,model=virtio-net-pci",
		"-display", "none", "-no-reboot", "-serial", "file:/w/s.log", "-monitor", "none"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args %v", got)
	}
	if o := vmcheck.OverlayArgs("/b", "/o"); !reflect.DeepEqual(o, []string{"create", "-f", "qcow2", "-F", "qcow2", "-b", "/b", "/o"}) {
		t.Fatalf("overlay %v", o)
	}
	if n := vmcheck.ImageFile("/c", "rocky-9", strings.Repeat("ab", 32)); n != filepath.Join("/c", "images", "rocky-9-abababababababab.qcow2") {
		t.Fatalf("image file %s", n)
	}
}

func TestNormalizeRefusesWhatCannotBoot(t *testing.T) {
	base := vmcheck.BootSpec{Distro: "x", CacheDir: "/c", WorkDir: "/w"}
	for name, mut := range map[string]func(*vmcheck.BootSpec){
		"no distro":  func(s *vmcheck.BootSpec) { s.Distro = "" },
		"tiny mem":   func(s *vmcheck.BootSpec) { s.MemMB = 64 },
		"neg time":   func(s *vmcheck.BootSpec) { s.Timeout = -1 },
		"bad ready":  func(s *vmcheck.BootSpec) { s.Ready = "(" },
		"no folders": func(s *vmcheck.BootSpec) { s.CacheDir = "" },
	} {
		s := base
		mut(&s)
		if _, err := s.Normalize(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestReadyAndTail(t *testing.T) {
	log := "[ 1.0] kernel\r\nFedora 45\r\nlocalhost login: \r\n"
	if l, ok := vmcheck.Ready(log, `login:`); !ok || l != "localhost login:" {
		t.Fatalf("%q %v", l, ok)
	}
	if _, ok := vmcheck.Ready("booting\nstill booting", `login:`); ok {
		t.Fatal("matched too early")
	}
	if _, ok := vmcheck.Ready("x", "("); ok {
		t.Fatal("a bad pattern must not match")
	}
	if got := vmcheck.Tail("a\n\nb\nc\nd\n", 2); got != "c\nd" {
		t.Fatalf("tail %q", got)
	}
}

type fakeNet struct {
	files map[string]string
	gets  []string
}

func (f *fakeNet) Text(_ context.Context, u string) (string, error) {
	if s, ok := f.files[u]; ok {
		return s, nil
	}
	return "", errors.New("404 " + u)
}

func (f *fakeNet) Get(_ context.Context, u string) (io.ReadCloser, error) {
	f.gets = append(f.gets, u)
	s, ok := f.files[u]
	if !ok {
		return nil, errors.New("404 " + u)
	}
	return io.NopCloser(strings.NewReader(s)), nil
}

func sum256(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

const imgBody = "not a real disk"

func testCatalogue(t *testing.T, digest string) (vmcheck.Catalogue, *fakeNet) {
	t.Helper()
	cat, err := vmcheck.LoadOnly([]byte(`{"distros":{"t-1":{"base_url":"https://m/","checksum_url":"https://m/SUMS","checksum_format":"sums","algo":"sha256","pattern":"t-1.qcow2","user":"t"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return cat, &fakeNet{files: map[string]string{
		"https://m/SUMS":      digest + "  t-1.qcow2\n",
		"https://m/t-1.qcow2": imgBody,
	}}
}

func TestDownloadChecksDigestAndCachesByDigest(t *testing.T) {
	cat, net := testCatalogue(t, sum256(imgBody))
	dir := t.TempDir()
	p, name, dg, err := vmcheck.Download(context.Background(), net, "t-1", cat["t-1"], dir)
	if err != nil || name != "t-1.qcow2" || dg != sum256(imgBody) {
		t.Fatalf("%s %s %s %v", p, name, dg, err)
	}
	if b, _ := os.ReadFile(p); string(b) != imgBody {
		t.Fatal("content")
	}
	// A second call finds the file and does not download again.
	if _, _, _, err = vmcheck.Download(context.Background(), net, "t-1", cat["t-1"], dir); err != nil || len(net.gets) != 1 {
		t.Fatalf("gets %v err %v", net.gets, err)
	}
}

func TestDownloadRefusesAWrongDigestAndLeavesNothing(t *testing.T) {
	cat, net := testCatalogue(t, sum256("something else"))
	dir := t.TempDir()
	_, _, _, err := vmcheck.Download(context.Background(), net, "t-1", cat["t-1"], dir)
	if !errors.Is(err, vmcheck.ErrDigest) {
		t.Fatalf("err %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "images", "*"))
	if len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

// fakeVM stands in for qemu: it writes the serial log it is told to.
type fakeVM struct {
	log      string
	exitWith error
	started  []string
	overlay  []string
	killed   bool
}

func (v *fakeVM) deps(net vmcheck.Fetcher, kvm bool) vmcheck.Deps {
	return vmcheck.Deps{
		Fetch:     net,
		KVMUsable: func() bool { return kvm },
		Run: func(_ context.Context, name string, args ...string) error {
			v.overlay = append([]string{name}, args...)
			return nil
		},
		Start: func(ctx context.Context, name string, args ...string) (func() error, error) {
			v.started = append([]string{name}, args...)
			for _, a := range args {
				if p, ok := strings.CutPrefix(a, "file:"); ok && v.log != "" {
					_ = os.WriteFile(p, []byte(v.log), 0o600)
				}
			}
			return func() error {
				if v.exitWith != nil {
					return v.exitWith
				}
				<-ctx.Done() // runs until killed
				v.killed = true
				return ctx.Err()
			}, nil
		},
		Sleep: func(ctx context.Context, _ time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(time.Millisecond):
			}
		},
	}
}

func bootSpec(t *testing.T) vmcheck.BootSpec {
	return vmcheck.BootSpec{Distro: "t-1", Timeout: 50 * time.Millisecond, CacheDir: t.TempDir(), WorkDir: filepath.Join(t.TempDir(), "w")}
}

func TestBootPassesOnTheReadinessLineAndStopsTheGuest(t *testing.T) {
	cat, net := testCatalogue(t, sum256(imgBody))
	vm := &fakeVM{log: "boot\nt login: \n"}
	spec := bootSpec(t)
	out, err := vmcheck.Boot(context.Background(), cat, spec, vm.deps(net, true), time.Now)
	if err != nil || !out.Pass || out.Line != "t login:" || out.Image != "t-1.qcow2" {
		t.Fatalf("%+v %v", out, err)
	}
	if !vm.killed || vm.started[0] != "qemu-system-x86_64" || vm.overlay[0] != "qemu-img" {
		t.Fatalf("started %v overlay %v killed %v", vm.started, vm.overlay, vm.killed)
	}
	if left, _ := os.ReadDir(spec.WorkDir); len(left) != 0 {
		t.Fatalf("work folder not clean: %v", left)
	}
}

func TestBootFailsOnTimeoutWithTheLogTail(t *testing.T) {
	cat, net := testCatalogue(t, sum256(imgBody))
	vm := &fakeVM{log: "kernel panic\n"}
	out, err := vmcheck.Boot(context.Background(), cat, bootSpec(t), vm.deps(net, true), time.Now)
	if err != nil || out.Pass || !strings.Contains(out.Detail, "kernel panic") || !strings.Contains(out.Detail, `"login:"`) {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestBootFailsWhenQemuEndsEarly(t *testing.T) {
	cat, net := testCatalogue(t, sum256(imgBody))
	vm := &fakeVM{log: "Could not access KVM\n", exitWith: errors.New("exit status 1")}
	spec := bootSpec(t)
	spec.Timeout = 5 * time.Second
	out, err := vmcheck.Boot(context.Background(), cat, spec, vm.deps(net, true), time.Now)
	if err != nil || out.Pass || !strings.Contains(out.Detail, "qemu ended before") || !strings.Contains(out.Detail, "Could not access KVM") {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestBootNeedsKVMAndAKnownDistro(t *testing.T) {
	cat, net := testCatalogue(t, sum256(imgBody))
	vm := &fakeVM{}
	if _, err := vmcheck.Boot(context.Background(), cat, bootSpec(t), vm.deps(net, false), time.Now); err == nil || !strings.Contains(err.Error(), "kvm.host") {
		t.Fatalf("no kvm: %v", err)
	}
	s := bootSpec(t)
	s.Distro = "arch"
	if _, err := vmcheck.Boot(context.Background(), cat, s, vm.deps(net, true), time.Now); err == nil || !strings.Contains(err.Error(), "unknown distro") {
		t.Fatalf("unknown: %v", err)
	}
	if vm.started != nil {
		t.Fatal("qemu must not start")
	}
}

func TestBootDigestMismatchIsAFailNotAStart(t *testing.T) {
	cat, net := testCatalogue(t, sum256("other"))
	vm := &fakeVM{}
	out, err := vmcheck.Boot(context.Background(), cat, bootSpec(t), vm.deps(net, true), time.Now)
	if err != nil || out.Pass || !strings.Contains(out.Detail, "published digest") || vm.started != nil {
		t.Fatalf("%+v %v %v", out, err, vm.started)
	}
}

func TestMirroredCatalogueMovesEveryDownloadAndDefaultIsUnchanged(t *testing.T) { // OPS37
	cat, _ := testCatalogue(t, sum256(imgBody))
	if same := cat.Mirrored(addresses.Set{}); !reflect.DeepEqual(same, cat) {
		t.Fatalf("no mirror changed the catalogue: %+v", same)
	}
	m := cat.Mirrored(addresses.Set{VMImageMirror: "https://mirror.test/vm"})
	if m["t-1"].BaseURL != "https://mirror.test/vm/t-1/" || m["t-1"].ChecksumURL != "https://mirror.test/vm/t-1/SUMS" {
		t.Fatalf("%+v", m["t-1"])
	}
	if cat["t-1"].BaseURL != "https://m/" {
		t.Fatal("the source catalogue was changed")
	}
	net := &fakeNet{files: map[string]string{
		"https://mirror.test/vm/t-1/SUMS":      sum256(imgBody) + "  t-1.qcow2\n",
		"https://mirror.test/vm/t-1/t-1.qcow2": imgBody,
	}}
	if _, _, _, err := vmcheck.Download(context.Background(), net, "t-1", m["t-1"], t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(net.gets) != 1 || net.gets[0] != "https://mirror.test/vm/t-1/t-1.qcow2" {
		t.Fatalf("downloaded from %v", net.gets)
	}
}
