package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeAgentInstallers struct {
	calls []string
	err   error
	self  bool
}

func (f *fakeAgentInstallers) AgentInstaller(_ context.Context, goos, arch string) (machines.PackageRef, error) {
	f.calls = append(f.calls, goos+"/"+arch)
	if f.err != nil {
		return machines.PackageRef{}, f.err
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	short := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	name := "CrspAgent-1.2.3-" + map[string]string{"windows": "win", "linux": "linux", "macos": "macos"}[goos] + "-" + short + "." + ext
	return machines.PackageRef{Name: name, URL: "https://pool.lan:8460/agent-installers/" + name, SHA256: goodSum, SelfSigned: f.self}, nil
}

// unwrapSh gives the script inside `sh -c '...'` back, undoing the single-quote escape.
func unwrapSh(cmd string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(cmd, "sh -c '"), "'")
	return strings.ReplaceAll(s, `'\''`, "'")
}

func TestSSHJoinLinuxPushesTheAgentInstallerWhenCrspIsMissing(t *testing.T) { // CRSP304
	f := &fakeSSH{}
	ai := &fakeAgentInstallers{self: true}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f; o.AgentInstallers = ai })
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("linux", "SHA256:abc")); err != nil {
		t.Fatal(err)
	}
	if len(ai.calls) != 2 {
		t.Fatalf("both CPUs are asked for: %v", ai.calls)
	}
	if len(f.runs) != 1 || !strings.HasPrefix(f.runs[0], "sh -c '") {
		t.Fatalf("one sh -c command, got %v", f.runs)
	}
	cmd := unwrapSh(f.runs[0])
	for _, want := range []string{
		"command -v crsp", "CrspAgent-1.2.3-linux-x64.tar.gz", "CrspAgent-1.2.3-linux-arm64.tar.gz",
		goodSum, "sha256sum -c", "sudo -n sh crsp-agent/install.sh", "K='k'", "agent install --controller pool.lan:8461", "--yes",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("the command lacks %q:\n%s", want, cmd)
		}
	}
	// the checksum is checked before the archive is opened
	if strings.Index(cmd, "sha256sum -c") > strings.Index(cmd, "tar xzf") {
		t.Fatalf("the archive is opened before its checksum is checked:\n%s", cmd)
	}
	// the install command runs after the installer step
	if strings.Index(cmd, "install.sh") > strings.Index(cmd, "agent install --controller") {
		t.Fatalf("agent install runs before the installer:\n%s", cmd)
	}
	steps := auditDetails(e, "join_code.ssh_step")
	if len(steps) != 1 || strings.Contains(steps[0], goodSum) {
		t.Fatalf("progress = %v", steps)
	}
}

func TestSSHJoinMacOSAsksForTheMacInstallerAndKeepsTLSForTheRelease(t *testing.T) { // CRSP304
	f := &fakeSSH{}
	ai := &fakeAgentInstallers{} // not self-signed: the release keeps the normal TLS check
	e := newEnv(t, func(o *machines.Options) { o.SSH = f; o.AgentInstallers = ai })
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("macos", "SHA256:abc")); err != nil {
		t.Fatal(err)
	}
	if ai.calls[0] != "macos/amd64" || !strings.Contains(unwrapSh(f.runs[0]), "CrspAgent-1.2.3-macos-arm64.tar.gz") || !strings.Contains(unwrapSh(f.runs[0]), "K=''") {
		t.Fatalf("calls %v\n%s", ai.calls, f.runs[0])
	}
}

func TestSSHJoinWithoutAgentInstallersKeepsTheOldCommand(t *testing.T) { // CRSP304
	f := &fakeSSH{}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f })
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("linux", "SHA256:abc")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.runs[0], "sudo -n crsp agent install") {
		t.Fatalf("command = %q", f.runs[0])
	}
}

func TestSSHJoinLinuxWithNoInstallerSourceAnswerWithoutRunningAnything(t *testing.T) { // CRSP304
	f := &fakeSSH{}
	e := newEnv(t, func(o *machines.Options) {
		o.SSH = f
		o.AgentInstallers = &fakeAgentInstallers{err: errors.New("offline and not cached")}
	})
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("linux", "SHA256:abc"))
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "offline and not cached") {
		t.Fatalf("err = %v", err)
	}
	if len(f.runs) != 0 {
		t.Fatalf("a command ran: %v", f.runs)
	}
}

func TestSSHJoinWindowsPrefersTheAgentZip(t *testing.T) { // CRSP304
	w := &winMachine{shell: "powershell", admin: true, arch: "ARM64"}
	ai := &fakeAgentInstallers{self: true}
	e := newEnv(t, func(o *machines.Options) {
		o.SSH = w
		o.AgentInstallers = ai
		o.WindowsPackages = &fakeWinPackages{}
	})
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc")); err != nil {
		t.Fatal(err)
	}
	if len(ai.calls) != 1 || ai.calls[0] != "windows/arm64" {
		t.Fatalf("calls = %v", ai.calls)
	}
	dl := w.scripts[1]
	for _, want := range []string{"CrspAgent-1.2.3-win-arm64.zip", "Expand-Archive", `crsp-agent\install.ps1`, "'" + goodSum + "'", "CrspPin"} {
		if !strings.Contains(dl, want) {
			t.Fatalf("download script lacks %q:\n%s", want, dl)
		}
	}
	if strings.Contains(dl, "msiexec") || strings.Index(dl, "Get-FileHash") > strings.Index(dl, "Expand-Archive") {
		t.Fatalf("the hash must be checked before the archive is opened, and no MSI is used:\n%s", dl)
	}
}

func TestInstallCommandsShowTheLANLinkWhenAgentInstallersAreThere(t *testing.T) { // CRSP304
	ai := &fakeAgentInstallers{self: true}
	e := newEnv(t, func(o *machines.Options) { o.AgentInstallers = ai; o.ConsoleURL = "https://pool.lan:8460" })
	got := map[string]string{}
	for _, c := range e.Svc.InstallCommands("CODE1") {
		got[c.OS] = c.Command
	}
	lin := unwrapSh(got["linux"])
	for _, want := range []string{"https://pool.lan:8460/agent-installers/CrspAgent-1.2.3-linux-x64.tar.gz", goodSum, "tar xzf", "sudo sh crsp-agent/install.sh", "sudo crsp agent install", "--join-code CODE1"} {
		if !strings.Contains(lin, want) {
			t.Fatalf("linux command lacks %q:\n%s", want, lin)
		}
	}
	if strings.Contains(lin, "sudo -n") {
		t.Fatal("a person is asked for the sudo password, so no -n")
	}
	win := got["windows"]
	for _, want := range []string{"CrspAgent-1.2.3-win-x64.zip", "CrspAgent-1.2.3-win-arm64.zip", "PROCESSOR_ARCHITECTURE", "Expand-Archive", "CrspPin", "& $crsp agent install", "--join-code CODE1"} {
		if !strings.Contains(win, want) {
			t.Fatalf("windows command lacks %q:\n%s", want, win)
		}
	}
	if strings.Index(win, "Get-FileHash") > strings.Index(win, "Expand-Archive") {
		t.Fatal("the checksum is checked before the archive is opened")
	}
}

func TestInstallCommandsStayPlainWithoutAConsoleAddressOrOnAFailure(t *testing.T) { // CRSP304
	for name, mod := range map[string]func(*machines.Options){
		"no console": func(o *machines.Options) { o.AgentInstallers = &fakeAgentInstallers{} },
		"no installer": func(o *machines.Options) {
			o.AgentInstallers = &fakeAgentInstallers{err: errors.New("x")}
			o.ConsoleURL = "https://pool.lan:8460"
		},
	} {
		e := newEnv(t, mod)
		for _, c := range e.Svc.InstallCommands("C") {
			if !strings.Contains(c.Command, "agent install --controller pool.lan:8461") || strings.Contains(c.Command, "agent-installers") {
				t.Fatalf("%s: %s: %q", name, c.OS, c.Command)
			}
		}
	}
}
