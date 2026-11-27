package machines

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// NewSSHRunner is the real SSHRunner over golang.org/x/crypto/ssh. It signs in
// with the key only (no password, no agent) and checks the host key against the
// fingerprint the owner confirmed.
func NewSSHRunner() SSHRunner { return sshRunner{} }

type sshRunner struct{}

func (sshRunner) dial(ctx context.Context, t SSHTarget, signer ssh.Signer, hostKey ssh.HostKeyCallback) (*ssh.Client, error) {
	d := net.Dialer{Timeout: 15 * time.Second}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		HostKeyCallback: hostKey,
		Timeout:         15 * time.Second,
	}
	if signer != nil {
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// lockedBuffer is written by the stdout and the stderr copies at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var errSeen = errors.New("host key seen")

// HostKey implements SSHRunner: it reads the key the host presents and stops
// before any sign-in.
func (r sshRunner) HostKey(ctx context.Context, t SSHTarget) (string, error) {
	var fp string
	cb := func(_ string, _ net.Addr, k ssh.PublicKey) error {
		fp = ssh.FingerprintSHA256(k)
		return errSeen
	}
	_, err := r.dial(ctx, t, nil, cb)
	if fp != "" {
		return fp, nil
	}
	if err == nil {
		err = errors.New("the host did not present a key")
	}
	return "", err
}

// Run implements SSHRunner.
func (r sshRunner) Run(ctx context.Context, t SSHTarget, command string) (string, error) {
	signer, err := ssh.ParsePrivateKey(t.Identity)
	if err != nil {
		var pm *ssh.PassphraseMissingError
		if errors.As(err, &pm) {
			return "", errors.New("the key file needs a passphrase; use a key without one for this join")
		}
		return "", errors.New("the file is not a private key")
	}
	want := t.HostFingerprint
	cb := func(_ string, _ net.Addr, k ssh.PublicKey) error {
		if got := ssh.FingerprintSHA256(k); got != want {
			return fmt.Errorf("the host key changed: expected %s, got %s", want, got)
		}
		return nil
	}
	cl, err := r.dial(ctx, t, signer, cb)
	if err != nil {
		return "", err
	}
	defer func() { _ = cl.Close() }()
	sess, err := cl.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close() }()
	out := &lockedBuffer{}
	sess.Stdout, sess.Stderr = out, out
	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()
	select {
	case err = <-done:
		return out.String(), err
	case <-ctx.Done():
		_ = sess.Close()
		return out.String(), ctx.Err()
	}
}
