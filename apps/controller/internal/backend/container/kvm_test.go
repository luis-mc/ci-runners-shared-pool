package container

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestKVMDeviceOnlyAndLabelNeedsUsable: runners R5.41, R6.1.
func TestKVMDeviceOnlyAndLabelNeedsUsable(t *testing.T) {
	args := KVMArgs(993)
	if strings.Join(args, " ") != "--device-cgroup-rule c 10:232 rwm --group-add 993" {
		t.Errorf("args %v", args)
	}
	if got := KVMArgs(0); strings.Join(got, " ") != "--device-cgroup-rule c 10:232 rwm" {
		t.Errorf("no gid: %v", got)
	}
	// The guard of pool.go wants the whole call: the job user and no capability.
	safe := []string{"run", "-d", "--read-only", "--user", "1001:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
	if err := CheckRunArgs(append(append([]string{}, safe...), append(args, "img")...)); err != nil {
		t.Errorf("kvm device refused: %v", err)
	}
	for _, bad := range [][]string{
		{"--privileged"}, {"--privileged=true"}, {"--pid=host"}, {"--net=host"}, {"--network", "host"},
		{"--ipc", "host"}, {"--device", "/dev/sda"}, {"--device", "/dev/kvm"}, {"--device=/dev/kvm:/dev/kvm"},
		{"--device-cgroup-rule", "c 10:231 rwm"}, {"--device-cgroup-rule", "a *:* rwm"},
	} {
		if err := CheckRunArgs(append(append([]string{}, safe...), append(bad, "img")...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if !errors.Is(CheckRunArgs(append(append([]string{}, safe...), "--privileged", "img")), ErrPrivileged) {
		t.Error("privileged error not ErrPrivileged")
	}
	cases := []struct {
		exists, usable, waited bool
		device, label          bool
		reason                 string
	}{
		{true, true, false, true, true, ""},
		{true, false, false, false, false, ReasonKVMNotUsable},
		{false, false, false, false, false, ReasonKVMNone},
		{false, false, true, false, false, ReasonKVMWaitedOut},
	}
	for _, c := range cases {
		d := DecideKVM(c.exists, c.usable, c.waited)
		if d.Device != c.device || d.Label != c.label || d.Reason != c.reason {
			t.Errorf("%+v -> %+v", c, d)
		}
	}
}

func TestKVMWaitIsBoundedAndStartsWithoutKVM(t *testing.T) {
	var slept time.Duration
	var logged []string
	w := KVMWait{
		Exists:   func() bool { return false },
		Sleep:    func(_ context.Context, d time.Duration) { slept += d },
		Interval: 2 * time.Second, Bound: 10 * time.Second,
		Log: func(m string) { logged = append(logged, m) },
	}
	if w.Wait(context.Background()) {
		t.Fatal("appeared")
	}
	if slept != 10*time.Second {
		t.Errorf("slept %s, want the bound", slept)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "starting without KVM") {
		t.Errorf("log %v", logged)
	}
	if d := DecideKVM(false, false, true); d.Label || d.Device {
		t.Errorf("after the wait: %+v", d)
	}

	// The device appears after a few looks: no log, and the wait ends early.
	looks := 0
	slept, logged = 0, nil
	w.Exists = func() bool { looks++; return looks > 3 }
	if !w.Wait(context.Background()) {
		t.Fatal("did not see the device")
	}
	if slept != 6*time.Second || len(logged) != 0 {
		t.Errorf("slept %s log %v", slept, logged)
	}

	// A cancelled context ends the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Exists = func() bool { return false }
	if w.Wait(ctx) {
		t.Error("cancelled wait reported the device")
	}
}

func TestLoaderModuleByVendor(t *testing.T) {
	for in, want := range map[string]string{
		"processor : 0\nvendor_id\t: GenuineIntel\n": "kvm_intel",
		"vendor_id\t: AuthenticAMD":                  "kvm_amd",
		"vendor_id\t: Other":                         "",
		"":                                           "",
	} {
		if got := LoaderModule(in); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

// TestComposeKVMLoaderOnlyPrivileged: only the loader is privileged; the pool
// gets /dev/kvm as a device, the loader restarts unless stopped, and the pool
// waits for the device (handover lesson 25).
func TestComposeKVMLoaderOnlyPrivileged(t *testing.T) {
	b, err := os.ReadFile("../../../../../images/layers/compose.kvm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := string(b)
	if strings.Count(c, "privileged: true") != 1 {
		t.Errorf("want exactly one privileged service (the loader), got %d", strings.Count(c, "privileged: true"))
	}
	poolAt := strings.Index(c, "\n  pool:")
	if poolAt < 0 || strings.Contains(c[poolAt:], "privileged") {
		t.Error("the pool must not be privileged")
	}
	for _, want := range []string{"kvm-loader:", "restart: unless-stopped", "modprobe", "kvm_intel", "kvm_amd",
		"device_cgroup_rules:", "c 10:232 rwm", "kvm-wait.sh", "network_mode: none"} {
		if !strings.Contains(c, want) {
			t.Errorf("compose.kvm.yaml lacks %q", want)
		}
	}
	// Lesson 28: no `devices:` mapping of /dev/kvm in any compose file.
	if strings.Contains(c, "/dev/kvm:/dev/kvm") || regexp.MustCompile(`(?m)^\s*devices:`).MatchString(c) {
		t.Error("compose.kvm.yaml maps /dev/kvm as a device; use device_cgroup_rules (lesson 28)")
	}
	if strings.Contains(c, "docker.sock") || strings.Contains(c, "pid: host") {
		t.Error("socket or host pid in the compose file")
	}
}

func TestKVMWaitScriptBounded(t *testing.T) {
	b, err := os.ReadFile("../../../../../images/layers/kvm/kvm-wait.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"CRSP_KVM_WAIT_SECONDS:-60", "-lt \"$bound\"", "starting without KVM", "exec \"$@\"", "mknod", "/proc/misc"} {
		if !strings.Contains(s, want) {
			t.Errorf("kvm-wait.sh lacks %q", want)
		}
	}
	if strings.Contains(s, "modprobe") || strings.Contains(s, "sudo") {
		t.Error("the wait script must not load modules or escalate")
	}
}

// runFields splits a recorded `docker run` line into arguments; the cgroup rule
// holds spaces and stays one argument.
func runFields(run string) []string {
	run = strings.ReplaceAll(run, KVMCgroupRule, "RULE")
	f := strings.Fields(strings.TrimPrefix(run, "docker"))
	for i := range f {
		if f[i] == "RULE" {
			f[i] = KVMCgroupRule
		}
	}
	return f
}

// TestNoComposeFileMapsKVMDevice: lesson 28, no compose file or run argument
// in the repository maps /dev/kvm as a device.
func TestNoComposeFileMapsKVMDevice(t *testing.T) {
	root := "../../.."
	mapping := regexp.MustCompile(`(?m)^\s*-\s*['"]?/dev/kvm\b`)
	for _, dir := range []string{"images", "packaging", "installer"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Error(rerr)
				return nil
			}
			if mapping.Match(b) {
				t.Errorf("%s maps /dev/kvm as a device (lesson 28)", p)
			}
			return nil
		})
	}
}

// fakeTools makes a folder with a mknod that records its arguments and creates
// the node as a plain file, a sleep that returns at once, and a chgrp.
func fakeTools(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // test stub
			t.Fatal(err)
		}
	}
	write("mknod", `echo "$@" >>"$FAKE_LOG"
[ -z "${FAKE_MKNOD_FAIL:-}" ] || exit 1
for last; do :; done
shift; shift
touch "$FAKE_DEV"
`)
	write("sleep", `echo sleep >>"$FAKE_LOG"
if [ -n "${FAKE_MISC_AFTER:-}" ]; then printf ' 232 kvm\n' >"$CRSP_PROC_MISC"; fi
`)
	write("chgrp", `echo "chgrp $@" >>"$FAKE_LOG"
`)
	return dir
}

func runWait(t *testing.T, misc string, extra ...string) (devExists bool, out, log string) {
	t.Helper()
	tools := fakeTools(t)
	work := t.TempDir()
	dev := filepath.Join(work, "kvm")
	logf := filepath.Join(work, "log")
	miscf := filepath.Join(work, "misc")
	if err := os.WriteFile(miscf, []byte(misc), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../../../../images/layers/kvm/kvm-wait.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "sh", script, "sh", "-c", "echo KVM=$CRSP_KVM")
	cmd.Env = append([]string{"PATH=" + tools + ":/usr/bin:/bin", "CRSP_KVM_DEV=" + dev, "CRSP_PROC_MISC=" + miscf,
		"CRSP_KVM_WAIT_SECONDS=6", "CRSP_KVM_STEP_SECONDS=2", "FAKE_LOG=" + logf, "FAKE_DEV=" + dev}, extra...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kvm-wait.sh: %v: %s", err, b)
	}
	_, statErr := os.Stat(dev)
	l, _ := os.ReadFile(logf)
	return statErr == nil, string(b), string(l)
}

// TestKVMWaitScriptCreatesNodeWhenMiscListsKVM: lesson 28; fake /proc/misc.
func TestKVMWaitScriptCreatesNodeWhenMiscListsKVM(t *testing.T) {
	// kvm is listed at once: mknod 10:232, no sleep, KVM=1.
	ok, out, log := runWait(t, " 63 vga_arbiter\n232 kvm\n")
	if !ok || !strings.Contains(out, "KVM=1") || !strings.Contains(log, "-m 0660") || !strings.Contains(log, "c 10 232") || strings.Contains(log, "sleep") {
		t.Errorf("listed: ok=%v out=%q log=%q", ok, out, log)
	}
	// kvm appears in /proc/misc after one wait step.
	ok, out, log = runWait(t, "63 vga_arbiter\n", "FAKE_MISC_AFTER=1")
	if !ok || !strings.Contains(out, "KVM=1") || !strings.Contains(log, "sleep") || !strings.Contains(log, "c 10 232") {
		t.Errorf("later: ok=%v out=%q log=%q", ok, out, log)
	}
	// The group is set when asked.
	_, _, log = runWait(t, "232 kvm\n", "CRSP_KVM_GID=993")
	if !strings.Contains(log, "chgrp 993") {
		t.Errorf("no chgrp: %q", log)
	}
	// kvm never listed: the wait is bounded, no node, starts without KVM.
	ok, out, log = runWait(t, "63 vga_arbiter\n")
	if ok || !strings.Contains(out, "KVM=0") || !strings.Contains(out, "starting without KVM") || strings.Contains(log, "mknod") {
		t.Errorf("never: ok=%v out=%q log=%q", ok, out, log)
	}
	// mknod refused: still bounded, starts without KVM.
	ok, out, _ = runWait(t, "232 kvm\n", "FAKE_MKNOD_FAIL=1")
	if ok || !strings.Contains(out, "KVM=0") {
		t.Errorf("refused: ok=%v out=%q", ok, out)
	}
}
