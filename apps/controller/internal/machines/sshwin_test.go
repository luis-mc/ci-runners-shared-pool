package machines_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"golang.org/x/crypto/ssh"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

const goodSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// winMachine answers like a Windows OpenSSH server: `echo %OS%` by its default
// shell, and the encoded PowerShell scripts by what they do. Lines end in CRLF.
type winMachine struct {
	mu          sync.Mutex
	shell       string // "cmd" or "powershell"
	admin       bool
	hasExe      bool
	arch        string
	failInstall bool
	cmds        []string
	scripts     []string
}

func (w *winMachine) HostKey(context.Context, machines.SSHTarget) (string, error) {
	return "SHA256:abc", nil
}

func decodeScript(cmd string) (string, bool) {
	const p = "powershell.exe -NoProfile -NonInteractive -EncodedCommand "
	if !strings.HasPrefix(cmd, p) {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cmd, p))
	if err != nil {
		return "", false
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(u)), true
}

func (w *winMachine) Run(_ context.Context, _ machines.SSHTarget, cmd string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cmds = append(w.cmds, cmd)
	if cmd == "echo %OS%" {
		if w.shell == "cmd" {
			return "Windows_NT\r\n", nil
		}
		return "%OS%\r\n", nil
	}
	s, ok := decodeScript(cmd)
	if !ok {
		return "", fmt.Errorf("unexpected command %q", cmd)
	}
	w.scripts = append(w.scripts, s)
	switch {
	case strings.Contains(s, "net.exe session"):
		out := "CRSP_NOT_ADMIN\r\n"
		if w.admin {
			out = "CRSP_ADMIN\r\n"
		}
		out += "CRSP_ARCH=" + w.arch + "\r\n"
		if w.hasExe {
			out += `CRSP_EXE=C:\Program Files\crsp\crsp.exe` + "\r\n"
		} else {
			out += "CRSP_EXE=\r\n"
		}
		return out, nil
	case strings.Contains(s, "Get-FileHash"):
		w.hasExe = true
		return `CRSP_EXE=C:\Program Files\crsp\crsp.exe` + "\r\n", nil
	case strings.Contains(s, "agent install"):
		if w.failInstall {
			return "the install step needs administrator rights\r\n", errors.New("Process exited with status 1")
		}
		return "The agent is installed and running.\r\n", nil
	}
	return "", errors.New("unknown script")
}

type fakeWinPackages struct {
	arch string
	err  error
	self bool // the controller's own endpoint, not the GitHub release
}

func (f *fakeWinPackages) WindowsPackage(_ context.Context, arch string) (machines.PackageRef, error) {
	f.arch = arch
	if f.err != nil {
		return machines.PackageRef{}, f.err
	}
	return machines.PackageRef{Name: "crsp-windows-" + arch + ".msi", URL: "https://example.test/dl/crsp-windows-" + arch + ".msi", SHA256: goodSum, SelfSigned: f.self}, nil
}

func winEnv(t *testing.T, w *winMachine, p machines.WindowsPackageSource) *env {
	return newEnv(t, func(o *machines.Options) { o.SSH = w; o.WindowsPackages = p })
}

func TestSSHJoinWindowsInstallsCrspAndRunsAgentInstallForBothShells(t *testing.T) { // CRSP123
	for _, shell := range []string{"cmd", "powershell"} {
		w := &winMachine{shell: shell, admin: true, arch: "AMD64"}
		pk := &fakeWinPackages{}
		e := winEnv(t, w, pk)
		in := sshIn("windows", "SHA256:abc")
		key := in.Identity
		c, err := e.Svc.JoinOverSSH(context.Background(), "owner", in)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if c.Code != "" || c.JoinCode.ID == "" {
			t.Fatalf("%s: the code stays on the machine: %+v", shell, c)
		}
		if pk.arch != "amd64" {
			t.Fatalf("%s: arch = %q", shell, pk.arch)
		}
		if len(w.scripts) != 3 {
			t.Fatalf("%s: probe, download, install; got %d scripts", shell, len(w.scripts))
		}
		dl := w.scripts[1]
		if !strings.Contains(dl, "'"+goodSum+"'") || !strings.Contains(dl, "https://example.test/dl/crsp-windows-amd64.msi") || strings.Index(dl, "Get-FileHash") > strings.Index(dl, "msiexec.exe") {
			t.Fatalf("%s: the hash is checked before the installer runs:\n%s", shell, dl)
		}
		inst := w.scripts[2]
		for _, want := range []string{`& 'C:\Program Files\crsp\crsp.exe' agent install --controller pool.lan:8461 `, "--fingerprint ", "--join-code ", " --yes", "exit $LASTEXITCODE"} {
			if !strings.Contains(inst, want) {
				t.Fatalf("%s: install script lacks %q:\n%s", shell, want, inst)
			}
		}
		for _, b := range key {
			if b != 0 {
				t.Fatal("the key bytes must be zeroed")
			}
		}
		steps := auditDetails(e, "join_code.ssh_step")
		if len(steps) != 4 || steps[0] != "shell: "+map[string]string{"cmd": "cmd", "powershell": "PowerShell"}[shell] || steps[3] != "agent install" {
			t.Fatalf("%s: progress = %v", shell, steps)
		}
	}
}

func auditDetails(e *env, action string) []string {
	var out []string
	for _, a := range e.Repo.AuditLog() {
		if a.Action == action {
			out = append(out, a.Detail)
		}
	}
	return out
}

func TestSSHJoinWindowsSkipsTheDownloadWhenCrspIsThere(t *testing.T) { // CRSP123
	w := &winMachine{shell: "cmd", admin: true, hasExe: true, arch: "ARM64"}
	e := winEnv(t, w, nil) // no package source needed
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc")); err != nil {
		t.Fatal(err)
	}
	if len(w.scripts) != 2 || strings.Contains(strings.Join(w.scripts, ""), "msiexec") {
		t.Fatalf("scripts = %d", len(w.scripts))
	}
}

func TestSSHJoinWindowsRefusesANonAdministrator(t *testing.T) { // CRSP123
	w := &winMachine{shell: "powershell", admin: false, arch: "AMD64"}
	e := winEnv(t, w, &fakeWinPackages{})
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc"))
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "not an administrator") {
		t.Fatalf("got %v", err)
	}
	if len(w.scripts) != 1 {
		t.Fatalf("nothing runs after the refusal: %d scripts", len(w.scripts))
	}
	if len(auditDetails(e, "join_code.ssh_failed")) != 1 {
		t.Fatal("the failure is audited")
	}
}

func TestSSHJoinWindowsFailuresNameTheStepAndHideTheCode(t *testing.T) { // CRSP123
	w := &winMachine{shell: "cmd", admin: true, hasExe: true, arch: "AMD64", failInstall: true}
	e := winEnv(t, w, nil)
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc"))
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("got %v", err)
	}
	// No package source and no crsp.exe: a plain reason, not a panic.
	w2 := &winMachine{shell: "cmd", admin: true, arch: "AMD64"}
	e2 := winEnv(t, w2, nil)
	if _, err := e2.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc")); err == nil || !strings.Contains(err.Error(), "no installer") {
		t.Fatalf("no source: %v", err)
	}
	e3 := winEnv(t, &winMachine{shell: "cmd", admin: true, arch: "AMD64"}, &fakeWinPackages{err: errors.New("release has no SHA256SUMS")})
	if _, err := e3.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc")); err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("source error: %v", err)
	}
}

func TestSSHJoinWindowsAsksToConfirmTheHostKeyFirst(t *testing.T) { // CRSP123
	w := &winMachine{shell: "cmd", admin: true, arch: "AMD64"}
	e := winEnv(t, w, &fakeWinPackages{})
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", ""))
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || len(w.cmds) != 0 {
		t.Fatalf("err=%v ran=%v", err, w.cmds)
	}
}

// A real SSH exchange with an in-process Windows-like server: the key is used
// for the sign-in only, and it is in no audit entry, no error and no file.
func TestSSHJoinWindowsOverARealSSHExchangeNeverPersistsTheKey(t *testing.T) { // CRSP123
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	userPub, userPriv, _ := ed25519.GenerateKey(rand.Reader)
	userSSHPub, _ := ssh.NewPublicKey(userPub)
	blk, err := ssh.MarshalPrivateKey(userPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := pem.EncodeToMemory(blk)
	keyCopy := string(keyFile)
	body := strings.Join(strings.Split(strings.TrimSpace(keyCopy), "\n")[1:3], "")

	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(userSSHPub.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostSigner)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	w := &winMachine{shell: "cmd", admin: true, hasExe: true, arch: "AMD64"}
	go func() {
		for conn, err := ln.Accept(); err == nil; conn, err = ln.Accept() {
			go serveWin(conn, cfg, w)
		}
	}()
	host, portS, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	for _, c := range portS {
		port = port*10 + int(c-'0')
	}
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	e := newEnv(t, func(o *machines.Options) { o.SSH = machines.NewSSHRunner() })
	in := machines.SSHJoinInput{OS: "windows", SSHTarget: machines.SSHTarget{Host: host, Port: port, User: "ci", Identity: keyFile, HostFingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey())}}
	if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", in); err != nil {
		t.Fatal(err)
	}
	if len(w.scripts) != 2 {
		t.Fatalf("scripts = %d", len(w.scripts))
	}
	for _, a := range e.Repo.AuditLog() {
		if strings.Contains(a.Detail+a.Action+a.Subject, body) {
			t.Fatalf("the key is in the audit log: %+v", a)
		}
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("the join wrote files: %v", left)
	}
}

// serveWin runs each exec request through the winMachine and answers like sshd.
func serveWin(conn net.Conn, cfg *ssh.ServerConfig, w *winMachine) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for rq := range creqs {
				if rq.Type != "exec" {
					_ = rq.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(rq.Payload[:4])
				out, runErr := w.Run(context.Background(), machines.SSHTarget{}, string(rq.Payload[4:4+n]))
				_ = rq.Reply(true, nil)
				_, _ = ch.Write([]byte(out))
				status := byte(0)
				if runErr != nil {
					status = 1
				}
				_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, status})
				_ = ch.Close()
				return
			}
		}()
	}
}

// CRSP133: from the controller's own address the machine accepts only the
// certificate chain with the pinned fingerprint; from GitHub it keeps normal TLS.
func TestSSHJoinWindowsPinsTheControllerCertificate(t *testing.T) {
	for _, self := range []bool{true, false} {
		w := &winMachine{shell: "powershell", admin: true, arch: "AMD64"}
		e := winEnv(t, w, &fakeWinPackages{self: self})
		if _, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("windows", "SHA256:abc")); err != nil {
			t.Fatal(err)
		}
		dl := w.scripts[1]
		if !self {
			if strings.Contains(dl, "ServerCertificateValidationCallback") || strings.Contains(dl, "CrspPin") {
				t.Fatalf("the GitHub download keeps normal TLS:\n%s", dl)
			}
			continue
		}
		pin := "[CrspPin]::Enable('" + e.CA.Fingerprint() + "')"
		i, j := strings.Index(dl, pin), strings.Index(dl, "Invoke-WebRequest")
		if i < 0 || j < i {
			t.Fatalf("the pin is set before the download:\n%s", dl)
		}
		if strings.Contains(dl, "delegate {") || !strings.Contains(dl, "ComputeHash(el.Certificate.RawData)") {
			t.Fatalf("the callback must compare the SHA-256 of the certificate, never accept all:\n%s", dl)
		}
	}
}
