package vmcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Guest work of `crsp vm prepare|run|selinux` (caches R5.46, port of
// vm_check.py): the guest gets a user and an ssh key made for this one run
// (cloud-init seed), the job's package is copied in over scp, and commands run
// over ssh. The key is a temporary file with mode 0600 that is deleted at the
// end of the run; it is never stored and never printed. Only the command
// building, the parsing and the result logic are unit-tested; ssh, scp,
// ssh-keygen, cloud-localds and qemu run behind GuestDeps.

// Defaults of a guest run.
const (
	// DefaultRunTimeout bounds the install and check commands together.
	DefaultRunTimeout = 30 * time.Minute
	// RemoteDir is the folder of the guest user that receives the package.
	RemoteDir = "crsp-job"
	// ExitTimedOut is the exit code reported when the commands ran out of time (as timeout(1)).
	ExitTimedOut = 124
	// sshLost is the exit code ssh gives when it could not talk to the guest.
	sshLost = 255
)

// GuestDeps is Deps plus what a guest session needs.
type GuestDeps struct {
	Deps
	// Exec runs a program to its end, writes its stdout and stderr to out, and
	// returns its exit code. err is set only when the program could not run at
	// all (not found, killed by the context); a non-zero code alone is no error.
	// Built on exec.CommandContext.
	Exec func(ctx context.Context, out io.Writer, name string, args ...string) (code int, err error)
	// FreePort returns a free TCP port on 127.0.0.1.
	FreePort func() (int, error)
}

// GuestSpec is one guest session: the boot plus the user, the key and the ports.
type GuestSpec struct {
	BootSpec
	// Out receives the output of the commands run in the guest (the job log).
	Out io.Writer
}

// SSHOptions are the ssh/scp options of a run: key only, no prompt, no stored
// host key (the guest is new each run and only reachable on 127.0.0.1).
func SSHOptions(keyPath string) []string {
	return []string{
		"-i", keyPath, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=10",
	}
}

// SSHArgs is the `ssh` call that runs one command line in the guest.
func SSHArgs(keyPath, user string, port int, command string) []string {
	a := append(SSHOptions(keyPath), "-p", strconv.Itoa(port), user+"@127.0.0.1", command)
	return a
}

// SCPArgs is the `scp` call that copies src (a file, or a folder's content with
// a trailing "/.") into the guest's dst.
func SCPArgs(keyPath, user string, port int, src, dst string, recursive bool) []string {
	a := append(SSHOptions(keyPath), "-P", strconv.Itoa(port))
	if recursive {
		a = append(a, "-r")
	}
	return append(a, src, user+"@127.0.0.1:"+dst)
}

// KeygenArgs is the `ssh-keygen` call that makes the run's key (no passphrase).
func KeygenArgs(keyPath string) []string {
	return []string{"-q", "-t", "ed25519", "-N", "", "-C", "crsp-vm-run", "-f", keyPath}
}

// SeedArgs is the `cloud-localds` call that builds the seed disk.
func SeedArgs(seed, userData, metaData string) []string { return []string{seed, userData, metaData} }

// UserData is the cloud-init user-data: one user, key login only, no password.
func UserData(user, publicKey string) string {
	return "#cloud-config\n" +
		"users:\n" +
		"  - name: " + user + "\n" +
		"    sudo: ALL=(ALL) NOPASSWD:ALL\n" +
		"    shell: /bin/bash\n" +
		"    lock_passwd: true\n" +
		"    ssh_authorized_keys:\n" +
		"      - " + strings.TrimSpace(publicKey) + "\n" +
		"ssh_pwauth: false\n" +
		"disable_root: true\n"
}

// MetaData is the cloud-init meta-data; the id is new for every run so
// cloud-init never takes a cached answer.
func MetaData(instanceID string) string {
	return "instance-id: " + instanceID + "\nlocal-hostname: crsp-vm\n"
}

// QemuGuestArgs is QemuArgs for a guest that is reached over ssh: it adds the
// cloud-init seed disk and forwards one port of 127.0.0.1 to the guest's ssh.
// Nothing listens on another address.
func QemuGuestArgs(s BootSpec, overlay, seed, serialLog string, sshPort int) []string {
	a := QemuArgs(s, overlay, serialLog)
	for i, v := range a {
		if v == "-nic" {
			a[i+1] = "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:" + strconv.Itoa(sshPort) + "-:22"
		}
	}
	return append(a, "-drive", "file="+seed+",if=virtio,format=raw")
}

// ShellQuote quotes one word for the guest's shell.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// InDir is the command line that runs cmd in the package folder.
func InDir(cmd string) string { return "cd " + RemoteDir + " && " + cmd }

// Step is one command of a run and what came of it.
type Step struct {
	Name     string
	Command  string
	Code     int
	TimedOut bool
	Err      string // why it could not run, when it did not
	Problem  string // what a check found wrong, when the command ran
	Elapsed  time.Duration
}

// Failed says whether the step did not pass.
func (s Step) Failed() bool { return s.TimedOut || s.Err != "" || s.Problem != "" || s.Code != 0 }

// Verdict turns the steps into the exit code and a plain line. Steps after the
// first failure are not run, so the first failing step decides: its own exit
// code, 124 when it timed out, 1 when it could not run, when a check found a
// problem or when ssh lost the guest.
func Verdict(steps []Step) (code int, detail string) {
	for _, s := range steps {
		if !s.Failed() {
			continue
		}
		switch {
		case s.TimedOut:
			return ExitTimedOut, fmt.Sprintf("%s ran out of time", s.Name)
		case s.Err != "":
			return 1, fmt.Sprintf("%s could not run: %s", s.Name, s.Err)
		case s.Problem != "":
			return 1, fmt.Sprintf("%s: %s", s.Name, s.Problem)
		case s.Code == sshLost:
			return 1, fmt.Sprintf("%s lost the guest (ssh exit 255)", s.Name)
		}
		return s.Code, fmt.Sprintf("%s failed with exit code %d", s.Name, s.Code)
	}
	return 0, ""
}

// RunOutcome is the verdict of a guest run.
type RunOutcome struct {
	Pass     bool
	Distro   string
	Image    string
	ExitCode int
	Steps    []Step
	Elapsed  time.Duration
	Detail   string
}

// Guest is a running guest the caller can copy to and run in. It exists only
// inside WithGuest.
type Guest struct {
	deps    GuestDeps
	user    string
	port    int
	keyPath string
	out     io.Writer
}

// Output runs one command line in the guest and returns its output and exit code.
func (g *Guest) Output(ctx context.Context, command string) (string, int, error) {
	var buf bytes.Buffer
	code, err := g.deps.Exec(ctx, &buf, "ssh", SSHArgs(g.keyPath, g.user, g.port, command)...)
	return buf.String(), code, err
}

// Do runs one named step in the guest; its output goes to the job log.
func (g *Guest) Do(ctx context.Context, name, command string, now func() time.Time) Step {
	st := Step{Name: name, Command: command}
	t0 := now()
	code, err := g.deps.Exec(ctx, g.out, "ssh", SSHArgs(g.keyPath, g.user, g.port, command)...)
	st.Elapsed = now().Sub(t0)
	switch {
	case ctx.Err() != nil:
		st.TimedOut = true
	case err != nil:
		st.Err = err.Error()
	default:
		st.Code = code
	}
	return st
}

// CopyIn copies a file, or the content of a folder, into the guest's package
// folder.
func (g *Guest) CopyIn(ctx context.Context, src string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if _, code, err := g.Output(ctx, "mkdir -p "+RemoteDir); err != nil || code != 0 {
		return fmt.Errorf("the package folder could not be made in the guest (exit %d): %v", code, err)
	}
	from, dst, rec := src, RemoteDir+"/", false
	if st.IsDir() {
		from, rec = strings.TrimRight(src, string(filepath.Separator))+string(filepath.Separator)+".", true
	}
	var buf bytes.Buffer
	code, err := g.deps.Exec(ctx, &buf, "scp", SCPArgs(g.keyPath, g.user, g.port, from, dst, rec)...)
	if err != nil {
		return fmt.Errorf("scp could not run: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("scp failed with exit code %d: %s", code, firstLineOf(strings.TrimSpace(buf.String())))
	}
	return nil
}

// WithGuest downloads the image, makes the run's key and seed, boots the guest,
// waits for ssh and calls fn. It always stops the guest and deletes the
// overlay, the seed, the key and the log, also on error and timeout. An error
// is returned for what stops the run being tried (bad spec, no KVM); a guest
// that does not come up is a failing outcome with fn not called.
func WithGuest(ctx context.Context, cat Catalogue, spec GuestSpec, deps GuestDeps, now func() time.Time, fn func(ctx context.Context, g *Guest) ([]Step, error)) (RunOutcome, error) {
	bs, err := spec.Normalize()
	if err != nil {
		return RunOutcome{}, err
	}
	d, err := cat.Get(bs.Distro)
	if err != nil {
		return RunOutcome{}, err
	}
	if !deps.KVMUsable() {
		return RunOutcome{}, errors.New("/dev/kvm is not usable; this runner has no kvm (see the doctor check kvm.host)")
	}
	start := now()
	out := RunOutcome{Distro: bs.Distro}
	fail := func(code int, detail string) (RunOutcome, error) {
		out.Pass, out.ExitCode, out.Detail, out.Elapsed = false, code, detail, now().Sub(start)
		return out, nil
	}
	image, imgName, digest, err := Download(ctx, deps.Fetch, bs.Distro, d, bs.CacheDir)
	if err != nil {
		return fail(1, err.Error())
	}
	out.Image = imgName
	base, release, err := UseBase(ctx, deps.Deps, bs.CacheDir, bs.Distro, image, digest)
	if err != nil {
		return fail(1, err.Error())
	}
	defer release()
	// 0o700: the private key lives here for the length of the run.
	if err := os.MkdirAll(bs.WorkDir, 0o700); err != nil {
		return RunOutcome{}, err
	}
	secret, err := os.MkdirTemp(bs.WorkDir, "key-")
	if err != nil {
		return RunOutcome{}, err
	}
	keyPath := filepath.Join(secret, "id")
	overlay := filepath.Join(bs.WorkDir, "overlay.qcow2")
	seed := filepath.Join(bs.WorkDir, "seed.img")
	serial := filepath.Join(bs.WorkDir, "serial.log")
	defer func() {
		_ = os.RemoveAll(secret) // the private key, always
		_ = os.Remove(overlay)
		_ = os.Remove(seed)
		_ = os.Remove(serial)
	}()

	if p, err := PrepareSeed(ctx, deps, d.User, secret, bs.WorkDir, seed, now); err != nil {
		return fail(1, err.Error())
	} else if p != keyPath {
		return RunOutcome{}, errors.New("internal: the key path changed")
	}
	if err := deps.Run(ctx, "qemu-img", OverlayArgs(base, overlay)...); err != nil {
		return fail(1, "qemu-img could not make the overlay disk: "+err.Error())
	}
	port, err := deps.FreePort()
	if err != nil {
		return fail(1, "no free port for ssh: "+err.Error())
	}

	bctx, cancel := context.WithTimeout(ctx, bs.Timeout)
	defer cancel()
	wait, err := deps.Start(bctx, "qemu-system-x86_64", QemuGuestArgs(bs, overlay, seed, serial, port)...)
	if err != nil {
		return fail(1, "qemu could not start: "+err.Error())
	}
	exited := make(chan error, 1)
	go func() { exited <- wait() }()
	stop := func() {
		cancel()
		if err := <-exited; err != nil {
			// Killed on purpose; its exit error says nothing new.
			return
		}
	}
	defer stop()

	g := &Guest{deps: deps, user: d.User, port: port, keyPath: keyPath, out: spec.Out}
	if g.out == nil {
		g.out = io.Discard
	}
	// Wait for ssh to answer: the guest is up when `true` runs in it.
	for {
		_, code, err := g.Output(bctx, "true")
		if err == nil && code == 0 {
			break
		}
		if bctx.Err() != nil {
			log, _ := os.ReadFile(serial) //nolint:gosec // the work folder of this run
			return fail(1, fmt.Sprintf("ssh did not answer within %s\n%s", bs.Timeout, Tail(string(log), LogTail)))
		}
		select {
		case qerr := <-exited:
			exited <- qerr                // let stop() read it
			log, _ := os.ReadFile(serial) //nolint:gosec // as above
			return fail(1, "qemu ended before ssh answered\n"+Tail(string(log), LogTail))
		default:
		}
		deps.Sleep(bctx, 3*time.Second)
	}

	// The guest is up; the boot timeout no longer applies, the caller's does.
	steps, ferr := fn(ctx, g)
	out.Steps = steps
	if ferr != nil {
		return fail(1, ferr.Error())
	}
	code, detail := Verdict(steps)
	out.Pass, out.ExitCode, out.Detail, out.Elapsed = code == 0, code, detail, now().Sub(start)
	return out, nil
}

// PrepareSeed makes the run's key (fresh, mode 0600, in keyDir) and the
// cloud-init seed disk, and returns the private key path. It never reads the
// private key; only the public half goes into the seed.
func PrepareSeed(ctx context.Context, deps GuestDeps, user, keyDir, workDir, seed string, now func() time.Time) (string, error) {
	keyPath := filepath.Join(keyDir, "id")
	if err := deps.Run(ctx, "ssh-keygen", KeygenArgs(keyPath)...); err != nil {
		return "", errors.New("ssh-keygen could not make the run's key: " + err.Error())
	}
	st, err := os.Stat(keyPath)
	if err != nil {
		return "", fmt.Errorf("the run's key is missing: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return "", err
		}
	}
	pub, err := os.ReadFile(keyPath + ".pub") //nolint:gosec // the public half, in our own folder
	if err != nil {
		return "", fmt.Errorf("the run's public key is missing: %w", err)
	}
	ud, md := filepath.Join(workDir, "user-data"), filepath.Join(workDir, "meta-data")
	defer func() { _ = os.Remove(ud); _ = os.Remove(md) }()
	if err := os.WriteFile(ud, []byte(UserData(user, string(pub))), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(md, []byte(MetaData("crsp-"+strconv.FormatInt(now().UnixNano(), 36))), 0o600); err != nil {
		return "", err
	}
	if err := deps.Run(ctx, "cloud-localds", SeedArgs(seed, ud, md)...); err != nil {
		return "", errors.New("cloud-localds could not build the seed disk: " + err.Error())
	}
	return keyPath, nil
}

// RunSpec is `crsp vm run`: copy a package in and run commands.
type RunSpec struct {
	GuestSpec
	// Package is a file or a folder copied into the guest's package folder.
	Package string
	// Install and Check are the command lines run, in this order, in the package folder.
	Install []string
	Check   []string
	// RunTimeout bounds the copy and all commands together.
	RunTimeout time.Duration
}

// Commands is the ordered list of named steps of a run.
func (r RunSpec) Commands() []Step {
	var s []Step
	for i, c := range r.Install {
		s = append(s, Step{Name: fmt.Sprintf("install %d", i+1), Command: c})
	}
	for i, c := range r.Check {
		s = append(s, Step{Name: fmt.Sprintf("check %d", i+1), Command: c})
	}
	return s
}

// Run copies the package into the guest and runs the install commands and then
// the check commands, stopping at the first that fails. The result is truthful:
// the exit code is the first failing command's, 124 for a timeout.
func Run(ctx context.Context, cat Catalogue, spec RunSpec, deps GuestDeps, now func() time.Time) (RunOutcome, error) {
	if len(spec.Commands()) == 0 {
		return RunOutcome{}, errors.New("nothing to run: give at least one --install or --check command")
	}
	for _, c := range spec.Commands() {
		if strings.TrimSpace(c.Command) == "" {
			return RunOutcome{}, errors.New("an empty command was given")
		}
	}
	if spec.Package != "" {
		if _, err := os.Stat(spec.Package); err != nil {
			return RunOutcome{}, fmt.Errorf("the package: %w", err)
		}
	}
	if spec.RunTimeout < 0 {
		return RunOutcome{}, errors.New("the run timeout cannot be negative")
	}
	if spec.RunTimeout == 0 {
		spec.RunTimeout = DefaultRunTimeout
	}
	return WithGuest(ctx, cat, spec.GuestSpec, deps, now, func(ctx context.Context, g *Guest) ([]Step, error) {
		rctx, cancel := context.WithTimeout(ctx, spec.RunTimeout)
		defer cancel()
		if spec.Package != "" {
			if err := g.CopyIn(rctx, spec.Package); err != nil {
				return nil, err
			}
		} else if _, code, err := g.Output(rctx, "mkdir -p "+RemoteDir); err != nil || code != 0 {
			return nil, fmt.Errorf("the package folder could not be made in the guest: exit %d %v", code, err)
		}
		return runSteps(rctx, g, spec.Commands(), now), nil
	})
}

// runSteps runs the steps in order and stops after the first that fails.
func runSteps(ctx context.Context, g *Guest, plan []Step, now func() time.Time) []Step {
	var done []Step
	for _, p := range plan {
		st := g.Do(ctx, p.Name, InDir(p.Command), now)
		st.Command = p.Command
		done = append(done, st)
		if st.Failed() {
			break
		}
	}
	return done
}
