package machines_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeSSH struct {
	mu      sync.Mutex
	runs    []string
	targets []machines.SSHTarget
	fail    error
	out     string
}

func (f *fakeSSH) HostKey(context.Context, machines.SSHTarget) (string, error) {
	return "SHA256:abc", nil
}

func (f *fakeSSH) Run(_ context.Context, t machines.SSHTarget, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs, f.targets = append(f.runs, cmd), append(f.targets, t)
	return f.out, f.fail
}

func sshIn(os string, fp string) machines.SSHJoinInput {
	return machines.SSHJoinInput{OS: os, SSHTarget: machines.SSHTarget{Host: "build1.lan", User: "ci", Identity: []byte("KEYFILE"), HostFingerprint: fp}}
}

func TestSSHJoinAsksToConfirmTheHostKeyFirst(t *testing.T) { // RES3
	f := &fakeSSH{}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f })
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("linux", ""))
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || len(nc.Names) != 1 || nc.Names[0] != "SHA256:abc" {
		t.Fatalf("want the fingerprint to confirm, got %v", err)
	}
	if len(f.runs) != 0 {
		t.Fatal("nothing runs before the host key is confirmed")
	}
}

func TestSSHJoinRunsTheInstallCommandOnceWithoutPassword(t *testing.T) { // RES3
	f := &fakeSSH{}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f })
	in := sshIn("linux", "SHA256:abc")
	key := in.Identity
	c, err := e.Svc.JoinOverSSH(context.Background(), "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 1 || !strings.HasPrefix(f.runs[0], "sudo -n crsp agent install --controller pool.lan:8461 ") || !strings.HasSuffix(f.runs[0], " --yes") || !strings.Contains(f.runs[0], "--join-code ") {
		t.Fatalf("command = %v", f.runs)
	}
	if c.Code != "" || c.JoinCode.ID == "" {
		t.Fatalf("the code stays on the machine: %+v", c)
	}
	for _, b := range key {
		if b != 0 {
			t.Fatal("the key bytes must be zeroed after the join")
		}
	}
	if f.targets[0].User != "ci" || f.targets[0].Port != 22 {
		t.Fatalf("target = %+v", f.targets[0])
	}
}

func TestSSHJoinRefusesAnUnknownSystemBadInputAndNoRunner(t *testing.T) { // RES3
	f := &fakeSSH{}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f })
	ctx := context.Background()
	if _, err := e.Svc.JoinOverSSH(ctx, "o", sshIn("freebsd", "SHA256:abc")); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("unknown system: %v", err)
	}
	bad := sshIn("linux", "SHA256:abc")
	bad.Host = "a b; rm -rf /"
	if _, err := e.Svc.JoinOverSSH(ctx, "o", bad); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("host: %v", err)
	}
	bad = sshIn("linux", "SHA256:abc")
	bad.User = "-oProxyCommand=x"
	if _, err := e.Svc.JoinOverSSH(ctx, "o", bad); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("user: %v", err)
	}
	bad = sshIn("macos", "SHA256:abc")
	bad.Identity = nil
	if _, err := e.Svc.JoinOverSSH(ctx, "o", bad); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("no key: %v", err)
	}
	if len(f.runs) != 0 {
		t.Fatal("nothing ran")
	}
	none := newEnv(t)
	if _, err := none.Svc.JoinOverSSH(ctx, "o", sshIn("linux", "SHA256:abc")); !errors.Is(err, machines.ErrNotSupported) {
		t.Fatalf("no runner: %v", err)
	}
}

func TestSSHJoinFailureWithdrawsTheCodeAndHidesIt(t *testing.T) { // RES3
	f := &fakeSSH{fail: errors.New("exit status 1")}
	e := newEnv(t, func(o *machines.Options) { o.SSH = f })
	_, err := e.Svc.JoinOverSSH(context.Background(), "owner", sshIn("linux", "SHA256:abc"))
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("got %v", err)
	}
	// The code that was made is withdrawn: a join with it must fail.
	f.fail, f.out = errors.New("boom"), ""
	if strings.Contains(err.Error(), "--join-code ") {
		t.Fatal("the error must not carry the command")
	}
}

// A real SSH exchange against an in-process server: public key only, host key pinned.
func TestRealSSHRunnerPinsHostKeyAndRunsCommand(t *testing.T) { // RES3
	hostPub, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	_ = hostPub
	userPub, userPriv, _ := ed25519.GenerateKey(rand.Reader)
	userSSHPub, _ := ssh.NewPublicKey(userPub)
	blk, err := ssh.MarshalPrivateKey(userPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := pem.EncodeToMemory(blk)

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
	got := make(chan string, 1)
	go func() {
		for conn, err := ln.Accept(); err == nil; conn, err = ln.Accept() {
			go serveOne(conn, cfg, got)
		}
	}()
	host, portS, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	for _, c := range portS {
		port = port*10 + int(c-'0')
	}
	r := machines.NewSSHRunner()
	tg := machines.SSHTarget{Host: host, Port: port, User: "ci", Identity: keyFile}
	fp, err := r.HostKey(context.Background(), tg)
	if err != nil || fp != ssh.FingerprintSHA256(hostSigner.PublicKey()) {
		t.Fatalf("host key: %q %v", fp, err)
	}
	tg.HostFingerprint = "SHA256:wrong"
	if _, err := r.Run(context.Background(), tg, "echo hi"); err == nil || !strings.Contains(err.Error(), "host key changed") {
		t.Fatalf("a wrong host key must stop the join: %v", err)
	}
	tg.HostFingerprint = fp
	if _, err := r.Run(context.Background(), tg, "echo hi"); err != nil {
		t.Fatal(err)
	}
	if cmd := <-got; cmd != "echo hi" {
		t.Fatalf("server saw %q", cmd)
	}
	tg.Identity = []byte("not a key")
	if _, err := r.Run(context.Background(), tg, "echo hi"); err == nil || !strings.Contains(err.Error(), "not a private key") {
		t.Fatalf("bad key file: %v", err)
	}
}

func serveOne(conn net.Conn, cfg *ssh.ServerConfig, got chan<- string) {
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
		for rq := range creqs {
			if rq.Type != "exec" {
				_ = rq.Reply(false, nil)
				continue
			}
			n := binary.BigEndian.Uint32(rq.Payload[:4])
			select {
			case got <- string(rq.Payload[4 : 4+n]):
			default:
			}
			_ = rq.Reply(true, nil)
			_, _ = ch.Write([]byte("ok\n"))
			_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			_ = ch.Close()
			break
		}
	}
}
