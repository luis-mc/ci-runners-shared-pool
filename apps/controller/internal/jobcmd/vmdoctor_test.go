package jobcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

const digestHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeHost is a Linux machine where everything is fine; a test breaks one thing.
func fakeHost() *VMHost {
	return &VMHost{
		GOOS:      "linux",
		Hostname:  func() string { return "box" },
		KVMUsable: func() bool { return true },
		LookPath:  func(string) error { return nil },
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			return "", errors.New("not asked")
		},
		FreeBytes:    func(string) (int64, error) { return 100 * gib, nil },
		BridgeExists: func(string) bool { return true },
		HashFile:     func(string, string) (string, error) { return digestHex, nil },
	}
}

// cacheWith makes a cache with the image of rocky-9 and, when pkgs is not nil,
// its base disk and manifest.
func cacheWith(t *testing.T, distro string, withBase bool, pkgs []string) string {
	t.Helper()
	dir := t.TempDir()
	if err := vmcheck.EnsureCacheDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", distro+"-"+digestHex[:16]+".qcow2"), []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	if withBase {
		base := vmcheck.BaseFile(dir, distro, vmcheck.BaseKey(digestHex, pkgs))
		if err := os.WriteFile(base, []byte("base"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := vmcheck.WriteManifest(base, pkgs); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runDoctor(t *testing.T, host *VMHost, args ...string) (code int, out, errs string) {
	t.Helper()
	var o, e bytes.Buffer
	env := Env{Getenv: func(string) string { return "" }, Stdout: &o, Stderr: &e, VMHost: host}
	return VM(append([]string{"doctor"}, args...), env), o.String(), e.String()
}

func TestVMDoctorGoodMachineIsOneLinePerCheckAndExitsZero(t *testing.T) {
	dir := cacheWith(t, "rocky-9", true, []string{"git"})
	code, out, errs := runDoctor(t, fakeHost(), "--cache", dir, "--need", "git")
	if code != 0 || errs != "" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 7 {
		t.Fatalf("want 7 lines (one per check), got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "OK ") {
			t.Errorf("not ok: %q", l)
		}
	}
}

func TestVMDoctorEachFailureHasItsFix(t *testing.T) {
	dir := cacheWith(t, "rocky-9", true, nil)
	cases := []struct {
		name string
		mut  func(*VMHost)
		args []string
		want []string
		code int
	}{
		{"no kvm", func(h *VMHost) { h.KVMUsable = func() bool { return false } }, nil, []string{"FAIL vm.accel", "modprobe"}, 1},
		{"no cloud-localds", func(h *VMHost) {
			h.LookPath = func(n string) error {
				if n == "cloud-localds" {
					return errors.New("no")
				}
				return nil
			}
		}, nil, []string{"FAIL vm.seedtool", "cloud-image-utils"}, 1},
		{"no qemu-img", func(h *VMHost) {
			h.LookPath = func(n string) error {
				if n == "qemu-img" {
					return errors.New("no")
				}
				return nil
			}
		}, nil, []string{"FAIL vm.tools", "qemu-img"}, 1},
		{"low disk", func(h *VMHost) { h.FreeBytes = func(string) (int64, error) { return 3 * gib, nil } }, nil, []string{"FAIL vm.disk", "crsp vm prune"}, 1},
		{"sha differs", func(h *VMHost) {
			h.HashFile = func(string, string) (string, error) { return strings.Repeat("f", 64), nil }
		}, nil, []string{"FAIL vm.basedisk", "does not match its digest", "crsp vm prepare rocky-9"}, 1},
		{"no bridge", func(h *VMHost) { h.BridgeExists = func(string) bool { return false } }, nil, []string{"WARN vm.network", "virbr0"}, 0},
		{"package drift", nil, []string{"--need", "zip"}, []string{"WARN vm.packages", "zip missing"}, 0},
	}
	for _, c := range cases {
		h := fakeHost()
		if c.mut != nil {
			c.mut(h)
		}
		code, out, _ := runDoctor(t, h, append([]string{"--cache", dir}, c.args...)...)
		if code != c.code {
			t.Errorf("%s: exit %d, want %d\n%s", c.name, code, c.code, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q in\n%s", c.name, w, out)
			}
		}
		if !strings.Contains(out, "fix:") {
			t.Errorf("%s: no fix line\n%s", c.name, out)
		}
	}
}

func TestVMDoctorImageMissingForAskedDistroAndEmptyCache(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := runDoctor(t, fakeHost(), "--cache", dir, "--distro", "rocky-9")
	if code != 1 || !strings.Contains(out, "FAIL vm.basedisk") || !strings.Contains(out, "not in the VM cache") {
		t.Fatalf("%d\n%s", code, out)
	}
	code, out, _ = runDoctor(t, fakeHost(), "--cache", filepath.Join(dir, "none"))
	if code != 0 || !strings.Contains(out, "INFO vm.basedisk") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVMDoctorBaseWithoutManifestReadsAsNoPackages(t *testing.T) {
	dir := cacheWith(t, "rocky-9", true, nil)
	if err := os.Remove(vmcheck.ManifestFile(vmcheck.BaseFile(dir, "rocky-9", vmcheck.BaseKey(digestHex, nil)))); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runDoctor(t, fakeHost(), "--cache", dir, "--need", "git")
	if code != 0 || !strings.Contains(out, "WARN vm.packages: The rocky-9 base disk drifted: git missing") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVMDoctorBaseOfAnotherImageIsNotTheBase(t *testing.T) {
	dir := cacheWith(t, "rocky-9", false, nil)
	other := vmcheck.BaseFile(dir, "rocky-9", vmcheck.BaseKey(strings.Repeat("9", 64), nil))
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, _ := runDoctor(t, fakeHost(), "--cache", dir)
	if !strings.Contains(out, "WARN vm.basedisk: The rocky-9 base disk is not made yet") {
		t.Fatalf("\n%s", out)
	}
}

func TestVMDoctorUnreadableImageSkips(t *testing.T) {
	dir := cacheWith(t, "rocky-9", true, nil)
	h := fakeHost()
	h.HashFile = func(string, string) (string, error) { return "", errors.New("io") }
	code, out, _ := runDoctor(t, h, "--cache", dir)
	if code != 0 || !strings.Contains(out, "SKIP vm.basedisk") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVMDoctorWindowsAndMacProbes(t *testing.T) {
	dir := t.TempDir()
	h := fakeHost()
	h.GOOS = "windows"
	h.Output = func(_ context.Context, name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "Get-VMSwitch") {
			return "Default Switch\r\n", nil
		}
		return "Disabled\r\n", nil
	}
	code, out, _ := runDoctor(t, h, "--cache", dir)
	if code != 1 || !strings.Contains(out, "FAIL vm.accel: Hyper-V is not available") || !strings.Contains(out, "OK   vm.network: The bridge Default Switch is there") {
		t.Fatalf("windows %d\n%s", code, out)
	}
	m := fakeHost()
	m.GOOS = "darwin"
	m.Output = func(_ context.Context, name string, args ...string) (string, error) { return "1\n", nil }
	code, out, _ = runDoctor(t, m, "--cache", dir)
	if code != 0 || !strings.Contains(out, "OK   vm.accel: Virtualization.framework is available") || !strings.Contains(out, "SKIP vm.network") {
		t.Fatalf("mac %d\n%s", code, out)
	}
	m.Output = func(context.Context, string, ...string) (string, error) { return "", errors.New("no") }
	if _, out, _ = runDoctor(t, m, "--cache", dir); !strings.Contains(out, "SKIP vm.accel") {
		t.Fatalf("mac without answer\n%s", out)
	}
}

func TestVMDoctorJSONAndUsage(t *testing.T) {
	code, out, _ := runDoctor(t, fakeHost(), "--cache", t.TempDir(), "--json")
	var fs []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &fs) != nil || len(fs) != 7 {
		t.Fatalf("%d %d\n%s", code, len(fs), out)
	}
	for _, bad := range [][]string{{"--bogus"}, {"extra"}, {"--distro", "nope-1"}, {"--floor-gib", "x"}} {
		if code, _, errs := runDoctor(t, fakeHost(), bad...); code != ExitVMUsage || errs == "" {
			t.Errorf("%v: %d %q", bad, code, errs)
		}
	}
}

func TestVMDoctorSettingsFromEnvironment(t *testing.T) {
	var o, e bytes.Buffer
	vars := map[string]string{VMPackagesVar: "git, zip", VMFloorVar: "90", VMBridgeVar: "br9"}
	h := fakeHost()
	h.BridgeExists = func(n string) bool { return n == "br9" }
	env := Env{Getenv: func(k string) string { return vars[k] }, Stdout: &o, Stderr: &e, VMHost: h}
	dir := cacheWith(t, "rocky-9", true, []string{"git"})
	if code := VM([]string{"doctor", "--cache", dir}, env); code != 0 {
		t.Fatalf("%d %s %s", code, o.String(), e.String())
	}
	out := o.String()
	for _, w := range []string{"(floor 90 GiB)", "The bridge br9 is there", "zip missing"} {
		if !strings.Contains(out, w) {
			t.Errorf("no %q in\n%s", w, out)
		}
	}
	vars[VMFloorVar] = "-1"
	if code := VM([]string{"doctor", "--cache", dir}, env); code != ExitVMUsage {
		t.Errorf("bad floor: %d", code)
	}
}
