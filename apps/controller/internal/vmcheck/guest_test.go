package vmcheck_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func TestGuestCommandBuilding(t *testing.T) {
	ssh := vmcheck.SSHArgs("/k/id", "fedora", 2222, "true")
	if ssh[len(ssh)-1] != "true" || ssh[len(ssh)-2] != "fedora@127.0.0.1" {
		t.Fatalf("ssh %v", ssh)
	}
	j := strings.Join(ssh, " ")
	for _, want := range []string{"-i /k/id", "IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "-p 2222"} {
		if !strings.Contains(j, want) {
			t.Errorf("ssh args lack %q: %s", want, j)
		}
	}
	scp := strings.Join(vmcheck.SCPArgs("/k/id", "u", 22, "/src/.", "crsp-job/", true), " ")
	if !strings.Contains(scp, "-P 22 -r /src/. u@127.0.0.1:crsp-job/") {
		t.Fatalf("scp %s", scp)
	}
	if kg := vmcheck.KeygenArgs("/k/id"); !reflect.DeepEqual(kg[len(kg)-2:], []string{"-f", "/k/id"}) || !strings.Contains(strings.Join(kg, " "), "-N  ") && kg[5] != "" {
		t.Fatalf("keygen %v", kg)
	}
	ud := vmcheck.UserData("fedora", "ssh-ed25519 AAAApub crsp-vm-run\n")
	for _, want := range []string{"#cloud-config", "- name: fedora", "NOPASSWD:ALL", "lock_passwd: true", "- ssh-ed25519 AAAApub crsp-vm-run\n", "ssh_pwauth: false"} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data lacks %q:\n%s", want, ud)
		}
	}
	if !strings.Contains(vmcheck.MetaData("i-1"), "instance-id: i-1") {
		t.Fatal("meta-data")
	}
	s, _ := vmcheck.BootSpec{Distro: "x", CacheDir: "/c", WorkDir: "/w"}.Normalize()
	q := strings.Join(vmcheck.QemuGuestArgs(s, "/w/o", "/w/seed", "/w/log", 4321), " ")
	if !strings.Contains(q, "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:4321-:22") || !strings.Contains(q, "file=/w/seed,if=virtio,format=raw") {
		t.Fatalf("qemu %s", q)
	}
	if strings.Contains(strings.Join(vmcheck.QemuArgs(s, "/w/o", "/w/log"), " "), "hostfwd") {
		t.Fatal("the plain boot must open no port")
	}
	if vmcheck.ShellQuote("it's") != `'it'\''s'` || vmcheck.InDir("make") != "cd crsp-job && make" {
		t.Fatal("quoting")
	}
}

func TestVerdictIsTheFirstFailure(t *testing.T) {
	ok := vmcheck.Step{Name: "install 1"}
	cases := []struct {
		name  string
		steps []vmcheck.Step
		code  int
		has   string
	}{
		{"all pass", []vmcheck.Step{ok, ok}, 0, ""},
		{"exit code kept", []vmcheck.Step{ok, {Name: "check 1", Code: 7}, {Name: "check 2", Code: 9}}, 7, "exit code 7"},
		{"timeout", []vmcheck.Step{{Name: "check 1", TimedOut: true, Code: -1}}, 124, "ran out of time"},
		{"could not run", []vmcheck.Step{{Name: "check 1", Err: "no ssh"}}, 1, "could not run: no ssh"},
		{"lost guest", []vmcheck.Step{{Name: "check 1", Code: 255}}, 1, "lost the guest"},
		{"a check problem", []vmcheck.Step{{Name: "avc denials", Problem: "2 AVC"}}, 1, "avc denials: 2 AVC"},
	}
	for _, c := range cases {
		code, detail := vmcheck.Verdict(c.steps)
		if code != c.code || !strings.Contains(detail, c.has) {
			t.Errorf("%s: %d %q", c.name, code, detail)
		}
	}
}

// fakeGuest stands in for qemu, ssh, scp, ssh-keygen and cloud-localds.
type fakeGuest struct {
	mu        sync.Mutex
	secret    string // the private key content, made at run time
	keyPath   string
	keyBad    []string // what was wrong with the key whenever ssh ran
	execs     [][]string
	runs      [][]string
	sshUpAt   int // the ssh `true` that first answers (1 = at once)
	trues     int
	answers   map[string]string // substring of the command -> output
	codes     map[string]int    // substring of the command -> exit code
	block     string            // a command that never ends until the context does
	udHadPub  bool
	seedBuilt bool
	started   []string
}

func (f *fakeGuest) deps(net vmcheck.Fetcher, vm *fakeVM) vmcheck.GuestDeps {
	d := vm.deps(net, true)
	inner := d.Run
	d.Run = func(ctx context.Context, name string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.runs = append(f.runs, append([]string{name}, args...))
		switch name {
		case "ssh-keygen":
			b := make([]byte, 24)
			_, _ = rand.Read(b)
			f.secret = "key-" + hex.EncodeToString(b) // no real key block in a test
			f.keyPath = args[len(args)-1]
			if err := os.WriteFile(f.keyPath, []byte(f.secret), 0o600); err != nil {
				return err
			}
			return os.WriteFile(f.keyPath+".pub", []byte("ssh-ed25519 AAAApubonly crsp-vm-run\n"), 0o644) //nolint:gosec // test public half
		case "cloud-localds":
			ud, err := os.ReadFile(args[1])
			f.udHadPub = err == nil && strings.Contains(string(ud), "AAAApubonly") && !strings.Contains(string(ud), f.secret)
			f.seedBuilt = true
			return os.WriteFile(args[0], []byte("seed"), 0o600)
		}
		return inner(ctx, name, args...)
	}
	start := d.Start
	d.Start = func(ctx context.Context, name string, args ...string) (func() error, error) {
		f.started = append([]string{name}, args...)
		return start(ctx, name, args...)
	}
	return vmcheck.GuestDeps{
		Deps:     d,
		FreePort: func() (int, error) { return 40022, nil },
		Exec: func(ctx context.Context, out io.Writer, name string, args ...string) (int, error) {
			f.mu.Lock()
			f.execs = append(f.execs, append([]string{name}, args...))
			cmd := args[len(args)-1]
			if name == "scp" {
				cmd = "scp"
			}
			// Windows has no Unix mode bits: there the key must only exist.
			if st, err := os.Stat(f.keyPath); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
				f.keyBad = append(f.keyBad, "key missing or not 0600 during "+name)
			}
			block := f.block != "" && strings.Contains(cmd, f.block)
			if cmd == "true" {
				f.trues++
				if f.trues < f.sshUpAt {
					f.mu.Unlock()
					return 255, nil
				}
			}
			var code int
			var reply string
			for k, v := range f.answers {
				if strings.Contains(cmd, k) {
					reply = v
				}
			}
			for k, v := range f.codes {
				if strings.Contains(cmd, k) {
					code = v
				}
			}
			f.mu.Unlock()
			if block {
				<-ctx.Done()
				return -1, ctx.Err()
			}
			_, _ = io.WriteString(out, reply)
			return code, nil
		},
	}
}

func guestSpec(t *testing.T) vmcheck.GuestSpec {
	return vmcheck.GuestSpec{BootSpec: bootSpec(t), Out: io.Discard}
}

func guestFixture(t *testing.T, serial string) (vmcheck.Catalogue, *fakeGuest, vmcheck.GuestDeps, *fakeVM) {
	cat, net := testCatalogue(t, sum256(imgBody))
	d, err := cat.Get("t-1")
	if err != nil {
		t.Fatal(err)
	}
	cat["fedora-t"] = d
	vm := &fakeVM{log: serial}
	g := &fakeGuest{sshUpAt: 1}
	return cat, g, g.deps(net, vm), vm
}

func (f *fakeGuest) sshCommands() []string {
	var out []string
	for _, e := range f.execs {
		if e[0] == "ssh" {
			out = append(out, e[len(e)-1])
		}
	}
	return out
}

func TestRunCopiesThePackageAndRunsInstallThenCheck(t *testing.T) {
	cat, g, deps, vm := guestFixture(t, "")
	pkg := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkg, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Package: pkg, Install: []string{"make install"}, Check: []string{"make check"}}
	spec.Distro = "t-1"
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || !out.Pass || out.ExitCode != 0 || len(out.Steps) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	cmds := g.sshCommands()
	if want := []string{"true", "mkdir -p crsp-job", "cd crsp-job && make install", "cd crsp-job && make check"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("ssh commands %v", cmds)
	}
	var scp []string
	for _, e := range g.execs {
		if e[0] == "scp" {
			scp = e
		}
	}
	if !strings.Contains(strings.Join(scp, " "), "-r "+pkg+"/. t@127.0.0.1:crsp-job/") && !strings.Contains(strings.Join(scp, " "), "127.0.0.1:crsp-job/") {
		t.Fatalf("scp %v", scp)
	}
	if !strings.Contains(strings.Join(vm.started, " "), "hostfwd=tcp:127.0.0.1:40022-:22") || !g.udHadPub || !g.seedBuilt {
		t.Fatalf("qemu %v seed %v pub %v", vm.started, g.seedBuilt, g.udHadPub)
	}
}

func TestRunKeyIsPrivateNeverLoggedAndDeleted(t *testing.T) {
	cat, g, deps, _ := guestFixture(t, "")
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"true"}}
	spec.Distro = "t-1"
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || !out.Pass {
		t.Fatalf("%+v %v", out, err)
	}
	if len(g.keyBad) != 0 {
		t.Fatalf("key problems: %v", g.keyBad)
	}
	if _, err := os.Stat(g.keyPath); !os.IsNotExist(err) {
		t.Fatalf("the key is still there: %v", err)
	}
	if left, _ := os.ReadDir(spec.WorkDir); len(left) != 0 {
		t.Fatalf("work folder not clean: %v", left)
	}
	all := out.Detail + strings.Join(g.sshCommands(), "\n")
	for _, c := range append(g.execs, g.runs...) {
		all += strings.Join(c, " ")
	}
	if g.secret == "" || strings.Contains(all, g.secret) {
		t.Fatal("the private key content reached a command line or the outcome")
	}
}

func TestRunTwoRunsMakeTwoKeys(t *testing.T) {
	cat, g, deps, _ := guestFixture(t, "")
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"true"}}
	spec.Distro = "t-1"
	if _, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now); err != nil {
		t.Fatal(err)
	}
	first := g.secret
	if _, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now); err != nil {
		t.Fatal(err)
	}
	if first == g.secret {
		t.Fatal("the key was reused")
	}
}

func TestRunStopsAtTheFirstFailureAndKeepsItsExitCode(t *testing.T) {
	cat, g, deps, vm := guestFixture(t, "")
	g.codes = map[string]int{"make check": 7}
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Install: []string{"make install"}, Check: []string{"make check", "make later"}}
	spec.Distro = "t-1"
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || out.Pass || out.ExitCode != 7 || len(out.Steps) != 2 || !strings.Contains(out.Detail, "check 1 failed with exit code 7") {
		t.Fatalf("%+v %v", out, err)
	}
	for _, c := range g.sshCommands() {
		if strings.Contains(c, "make later") {
			t.Fatal("a command ran after the failure")
		}
	}
	if !vm.killed {
		t.Fatal("the guest was left running")
	}
}

func TestRunTimeoutIsReportedAsTimedOut(t *testing.T) {
	cat, g, deps, vm := guestFixture(t, "")
	g.block = "sleep"
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"sleep 999"}, RunTimeout: 30 * time.Millisecond}
	spec.Distro = "t-1"
	spec.Timeout = time.Minute
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || out.Pass || out.ExitCode != vmcheck.ExitTimedOut || !out.Steps[0].TimedOut {
		t.Fatalf("%+v %v", out, err)
	}
	if !vm.killed {
		t.Fatal("the guest was left running")
	}
}

func TestRunFailsWithTheSerialTailWhenSSHNeverAnswers(t *testing.T) {
	cat, g, deps, _ := guestFixture(t, "boot line\nkernel panic\n")
	g.sshUpAt = 1 << 30
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"true"}}
	spec.Distro = "t-1"
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || out.Pass || out.ExitCode != 1 || !strings.Contains(out.Detail, "ssh did not answer") || !strings.Contains(out.Detail, "kernel panic") {
		t.Fatalf("%+v %v", out, err)
	}
	if len(out.Steps) != 0 {
		t.Fatal("a command ran without a guest")
	}
	if _, err := os.Stat(g.keyPath); !os.IsNotExist(err) {
		t.Fatal("the key was kept")
	}
}

func TestRunWaitsForSSHThenRuns(t *testing.T) {
	cat, g, deps, _ := guestFixture(t, "")
	g.sshUpAt = 3
	spec := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"echo ok"}}
	spec.Distro = "t-1"
	out, err := vmcheck.Run(context.Background(), cat, spec, deps, time.Now)
	if err != nil || !out.Pass || g.trues != 3 {
		t.Fatalf("%+v %v tries %d", out, err, g.trues)
	}
}

func TestRunRefusesWhatCannotRun(t *testing.T) {
	cat, _, deps, _ := guestFixture(t, "")
	for name, s := range map[string]vmcheck.RunSpec{
		"no commands": {GuestSpec: guestSpec(t)},
		"empty":       {GuestSpec: guestSpec(t), Check: []string{" "}},
		"no package":  {GuestSpec: guestSpec(t), Check: []string{"x"}, Package: "/does/not/exist"},
		"neg timeout": {GuestSpec: guestSpec(t), Check: []string{"x"}, RunTimeout: -1},
	} {
		s.Distro = "t-1"
		if _, err := vmcheck.Run(context.Background(), cat, s, deps, time.Now); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	deps.KVMUsable = func() bool { return false }
	s := vmcheck.RunSpec{GuestSpec: guestSpec(t), Check: []string{"x"}}
	s.Distro = "t-1"
	if _, err := vmcheck.Run(context.Background(), cat, s, deps, time.Now); err == nil || !strings.Contains(err.Error(), "/dev/kvm") {
		t.Fatalf("no kvm: %v", err)
	}
}

func TestSELinuxParsers(t *testing.T) {
	if got := vmcheck.ParseSemodule("abrt\t1.0\n\ncrsp_pool 1.2.3\n"); !reflect.DeepEqual(got, []string{"abrt", "crsp_pool"}) || !vmcheck.HasModule(got, "crsp_pool") || vmcheck.HasModule(got, "crsp") {
		t.Fatalf("semodule %v", got)
	}
	if vmcheck.ContextOfType("system_u:system_r:crsp_t:s0\n") != "crsp_t" || vmcheck.ContextOfType("LABEL") != "" || vmcheck.ContextOfType("") != "" {
		t.Fatal("context")
	}
	if vmcheck.ModuleName("/x/crsp_pool.pp") != "crsp_pool" {
		t.Fatal("module name")
	}
	if vmcheck.ModeVerdict("Enforcing\n") != "" || vmcheck.ModeVerdict("Permissive") == "" || vmcheck.ModeVerdict("") == "" {
		t.Fatal("mode")
	}
	if !vmcheck.SELinuxFamily("fedora-45") || !vmcheck.SELinuxFamily("rocky-9") || vmcheck.SELinuxFamily("debian-12") || vmcheck.SELinuxFamily("ubuntu-24.04") {
		t.Fatal("family")
	}
}

func TestSELinuxValidate(t *testing.T) {
	ok := vmcheck.SELinuxSpec{RPM: "a.rpm", Module: "m.pp", Service: "crsp.service", ContextType: "crsp_t"}
	ok.Distro = "fedora-45"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*vmcheck.SELinuxSpec){
		"debian":       func(s *vmcheck.SELinuxSpec) { s.Distro = "debian-12" },
		"context only": func(s *vmcheck.SELinuxSpec) { s.Service = "" },
		"bad unit":     func(s *vmcheck.SELinuxSpec) { s.Service = "a; rm -rf /" },
		"bad type":     func(s *vmcheck.SELinuxSpec) { s.ContextType = "a b" },
		"not pp":       func(s *vmcheck.SELinuxSpec) { s.Module = "m.te" },
		"not rpm":      func(s *vmcheck.SELinuxSpec) { s.RPM = "a.deb" },
	} {
		s := ok
		mut(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func selinuxFixture(t *testing.T) (vmcheck.Catalogue, *fakeGuest, vmcheck.GuestDeps, vmcheck.SELinuxSpec) {
	cat, g, deps, _ := guestFixture(t, "")
	dir := t.TempDir()
	rpm, mod := filepath.Join(dir, "crsp.rpm"), filepath.Join(dir, "crsp_pool.pp")
	for _, f := range []string{rpm, mod} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g.answers = map[string]string{
		"getenforce":                  "Enforcing\n",
		"sudo semodule -l":            "crsp_pool\t1.0\n",
		"systemctl show":              "4242\n",
		"ps -Z":                       "system_u:system_r:crsp_t:s0\n",
		"sudo cat /var/log/audit/aud": "type=SYSCALL msg=ok\n",
	}
	s := vmcheck.SELinuxSpec{GuestSpec: guestSpec(t), RPM: rpm, Module: mod, Service: "crsp.service", ContextType: "crsp_t"}
	s.Distro = "fedora-t"
	return cat, g, deps, s
}

func TestSELinuxPassesWhenEnforcingLoadedRightContextAndNoAVC(t *testing.T) {
	cat, g, deps, spec := selinuxFixture(t)
	out, err := vmcheck.SELinux(context.Background(), cat, spec, deps, time.Now)
	if err != nil || !out.Pass || out.ExitCode != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	cmds := strings.Join(g.sshCommands(), "\n")
	for _, want := range []string{"getenforce", "sudo dnf install -y './crsp.rpm'", "sudo semodule -i 'crsp_pool.pp'", "sudo systemctl start 'crsp.service'", "systemctl show -p MainPID --value 'crsp.service'", "ps -Z -o label= -p 4242"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("no %q in\n%s", want, cmds)
		}
	}
}

func TestSELinuxFindsEachProblem(t *testing.T) {
	cases := map[string]struct {
		mut  func(*fakeGuest)
		code int
		has  string
	}{
		"permissive":        {func(g *fakeGuest) { g.answers["getenforce"] = "Permissive\n" }, 1, "SELinux is Permissive, not Enforcing"},
		"module not loaded": {func(g *fakeGuest) { g.answers["sudo semodule -l"] = "other\t1\n" }, 1, "module crsp_pool is not in the loaded list"},
		"wrong context":     {func(g *fakeGuest) { g.answers["ps -Z"] = "system_u:system_r:unconfined_service_t:s0\n" }, 1, `expected type "crsp_t"`},
		"not running":       {func(g *fakeGuest) { g.answers["systemctl show"] = "0\n" }, 1, "no main process"},
		"avc": {func(g *fakeGuest) {
			g.answers["sudo cat /var/log/audit/aud"] = "type=AVC msg=audit(1): avc:  denied  { read } for pid=1 comm=\"crsp\"\n"
		}, 1, "1 AVC denial(s)"},
		"install fails": {func(g *fakeGuest) { g.codes = map[string]int{"dnf install": 3} }, 3, "install rpm failed with exit code 3"},
		"audit missing": {func(g *fakeGuest) { g.codes = map[string]int{"sudo cat /var/log/audit": 1} }, 1, "audit log failed with exit code 1"},
	}
	for name, c := range cases {
		cat, g, deps, spec := selinuxFixture(t)
		c.mut(g)
		out, err := vmcheck.SELinux(context.Background(), cat, spec, deps, time.Now)
		if err != nil || out.Pass || out.ExitCode != c.code || !strings.Contains(out.Detail, c.has) {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}

func TestSELinuxRefusesADistroWithoutSELinux(t *testing.T) {
	cat, _, deps, spec := selinuxFixture(t)
	spec.Distro = "debian-12"
	if _, err := vmcheck.SELinux(context.Background(), cat, spec, deps, time.Now); err == nil {
		t.Fatal("debian accepted")
	}
}
