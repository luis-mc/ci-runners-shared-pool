package machines

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// Join over SSH (machines R5.3a, RES3, owner decision D59). It is the optional
// path beside copy-and-wait: the controller opens one SSH session to a Linux
// or macOS machine, runs the install command there and closes it. The key file
// is read for that one session; it is not written to the database, to disk or
// to a log, and its bytes are zeroed afterwards. There is no password option.

// SSHTarget is where and as whom the controller connects.
type SSHTarget struct {
	Host string
	Port int
	User string
	// Identity is the private key file's content, used once.
	Identity []byte
	// HostFingerprint is the SHA256 fingerprint of the host key the owner
	// confirmed ("SHA256:..."); empty on the first call.
	HostFingerprint string
}

// SSHRunner runs one command on a remote host over SSH. The real one is in
// sshexec.go; tests give a fake.
type SSHRunner interface {
	// HostKey returns the host key fingerprint the host presents.
	HostKey(ctx context.Context, t SSHTarget) (string, error)
	// Run connects, checks the host key against t.HostFingerprint, runs the
	// command and returns its combined output.
	Run(ctx context.Context, t SSHTarget, command string) (string, error)
}

// SSHJoinInput is a join over SSH request. OS is the remote system ("linux",
// "macos" or "windows").
type SSHJoinInput struct {
	OS string
	SSHTarget
}

var (
	hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-:]*[A-Za-z0-9])?$`)
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_\-.]{0,31}$`)
)

// JoinOverSSH makes a join code and runs the install command on the machine.
// The first call (no confirmed host fingerprint) only reads the host key and
// answers NeedsConfirmationError carrying the fingerprint; nothing is made and
// nothing is run. The second call carries the fingerprint the owner confirmed.
func (s *Service) JoinOverSSH(ctx context.Context, actor string, in SSHJoinInput) (CreatedCode, error) {
	defer func() {
		for i := range in.Identity {
			in.Identity[i] = 0
		}
	}()
	if s.o.SSH == nil {
		return CreatedCode{}, fmt.Errorf("%w: joining over SSH is not available here", ErrNotSupported)
	}
	if in.OS != "linux" && in.OS != "macos" && in.OS != "windows" {
		return CreatedCode{}, fmt.Errorf("%w: SSH join is for Linux, macOS and Windows", ErrInvalid)
	}
	in.Host, in.User = strings.TrimSpace(in.Host), strings.TrimSpace(in.User)
	if in.Host == "" || !hostRe.MatchString(in.Host) {
		return CreatedCode{}, fmt.Errorf("%w: the host is not a name or an address", ErrInvalid)
	}
	if !userRe.MatchString(in.User) {
		return CreatedCode{}, fmt.Errorf("%w: the user name is not valid", ErrInvalid)
	}
	if len(in.Identity) == 0 {
		return CreatedCode{}, fmt.Errorf("%w: choose an SSH key file", ErrInvalid)
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if in.Port < 1 || in.Port > 65535 {
		return CreatedCode{}, fmt.Errorf("%w: the port is not valid", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if in.HostFingerprint == "" {
		fp, err := s.o.SSH.HostKey(ctx, in.SSHTarget)
		if err != nil {
			return CreatedCode{}, fmt.Errorf("%w: could not reach %s: %s", ErrInvalid, net.JoinHostPort(in.Host, fmt.Sprint(in.Port)), err)
		}
		return CreatedCode{}, &NeedsConfirmationError{
			Message: "Confirm the host key of " + in.Host + " before crsp signs in.",
			Names:   []string{fp},
		}
	}
	created, err := s.CreateJoinCode(ctx, actor, DefaultCodeTTL)
	if err != nil {
		return CreatedCode{}, err
	}
	var cmd string
	for _, c := range s.InstallCommands(created.Code) {
		if c.OS == in.OS {
			cmd = strings.Replace(c.Command, "sudo ", "sudo -n ", 1) // no password: sudo must not ask
		}
	}
	cmd += " --yes" // nobody can type the yes over SSH
	var out string
	if in.OS == "windows" {
		out, err = s.joinWindows(ctx, in, strings.TrimPrefix(cmd, "crsp.exe "), func(step string) {
			_ = s.o.Repo.Audit(ctx, actor, "join_code.ssh_step", created.JoinCode.ID, step)
		})
	} else {
		out, err = s.o.SSH.Run(ctx, in.SSHTarget, cmd)
	}
	if err != nil {
		_ = s.o.Repo.Audit(ctx, actor, "join_code.ssh_failed", created.JoinCode.ID, in.User+"@"+in.Host)
		_, _ = s.o.Repo.RevokeJoinCode(ctx, created.JoinCode.ID, s.now())
		if errors.Is(err, ErrNotAdmin) {
			return CreatedCode{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
		return CreatedCode{}, fmt.Errorf("%w: the install command did not finish on %s: %s", ErrInvalid, in.Host, strings.ReplaceAll(tail(err.Error()+" "+out), created.Code, "<join code>"))
	}
	_ = s.o.Repo.Audit(ctx, actor, "join_code.ssh", created.JoinCode.ID, in.User+"@"+in.Host)
	created.Code = "" // the code stays on the machine; the caller needs only its record
	return created, nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
