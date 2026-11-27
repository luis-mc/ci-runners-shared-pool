package regcreds

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Command is one process to run. The password goes in Stdin only.
type Command struct {
	Name  string
	Args  []string
	Env   []string
	Stdin Secret // never printed: Secret masks itself
}

// Exec runs commands. Real code uses exec.CommandContext; tests use a fake.
type Exec interface {
	Run(ctx context.Context, c Command) error
}

// Files writes files for a runner, a mount or a boot seed.
type Files interface {
	MkdirAll(dir string, perm uint32) error
	WriteFile(name string, data []byte, perm uint32) error
}

// Folder names inside a container and a VM. update-ca-certificates reads the
// files of the first one.
const (
	GuestCADir  = "/usr/local/share/ca-certificates/crsp"
	GuestCAFile = GuestCADir + "/crsp-private-ca.crt"
)

// Mount is a read-only bind mount of a host folder.
type Mount struct{ HostDir, GuestDir string }

// ContainerPlan is what the container backend adds to a pool container: the
// mounted CA folder, the command that adds it to the container's trust store
// and the engine logins (R5.78).
type ContainerPlan struct {
	CAMount *Mount
	// AfterStart run inside the container, as root, before the runner starts.
	AfterStart []Command
	// Logins are engine logins on the host, run before images are pulled.
	Logins []Command
}

// PlanContainer builds the plan. hostCADir is a folder owned by the agent;
// the bundle is written there. The password of each login goes to the
// command's Stdin; --password-stdin keeps it off the command line.
func PlanContainer(ctx context.Context, s *Store, f Files, engine, container, hostCADir string) (ContainerPlan, error) {
	var p ContainerPlan
	bundle, err := s.Bundle(ctx)
	if err != nil {
		return p, err
	}
	if len(bundle) > 0 {
		if err := f.MkdirAll(hostCADir, 0o755); err != nil {
			return p, fmt.Errorf("make the CA folder: %w", err)
		}
		if err := f.WriteFile(filepath.Join(hostCADir, "crsp-private-ca.crt"), bundle, 0o644); err != nil {
			return p, fmt.Errorf("write the CA bundle: %w", err)
		}
		p.CAMount = &Mount{HostDir: hostCADir, GuestDir: GuestCADir}
		p.AfterStart = append(p.AfterStart, Command{Name: engine, Args: []string{"exec", "--user", "0", container, "update-ca-certificates"}})
	}
	logins, err := s.Logins(ctx, KindContainer)
	if err != nil {
		return p, err
	}
	for _, l := range logins {
		pw, err := s.Password(ctx, l)
		if err != nil {
			return p, err
		}
		p.Logins = append(p.Logins, Command{Name: engine, Args: []string{"login", l.Host, "--username", l.Username, "--password-stdin"}, Stdin: pw})
	}
	return p, nil
}

// BootFile is a file written into a VM before it starts its runner.
type BootFile struct {
	Path string
	Data []byte
	Mode uint32
}

// BootPlan is what the VM backend injects at boot (R5.78). The files go in
// the one-time seed disk; the commands run once in the guest.
type BootPlan struct {
	Files    []BootFile
	Commands []string
}

// PlanVM builds the boot plan. The password is written to a 0600 file in the
// seed, read by the login command on its standard input, and the file is
// removed by the same script.
func PlanVM(ctx context.Context, s *Store) (BootPlan, error) {
	var p BootPlan
	bundle, err := s.Bundle(ctx)
	if err != nil {
		return p, err
	}
	if len(bundle) > 0 {
		p.Files = append(p.Files, BootFile{Path: GuestCAFile, Data: bundle, Mode: 0o644})
		p.Commands = append(p.Commands, "update-ca-certificates")
	}
	logins, err := s.Logins(ctx, KindVM)
	if err != nil {
		return p, err
	}
	for i, l := range logins {
		pw, err := s.Password(ctx, l)
		if err != nil {
			return p, err
		}
		file := fmt.Sprintf("/run/crsp/login-%d", i)
		p.Files = append(p.Files, BootFile{Path: file, Data: []byte(pw.Reveal()), Mode: 0o600})
		p.Commands = append(p.Commands, fmt.Sprintf("docker login %s --username %s --password-stdin < %s; rm -f %s",
			shellQuote(l.Host), shellQuote(l.Username), file, file))
	}
	return p, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// PersistentPlan is what a persistent runner gets: environment lines for its
// own env file, files in its own folder, and engine logins into its own
// config folder. The host trust store and the host's engine login are never
// touched (R5.78).
type PersistentPlan struct {
	Env    []string
	Logins []Command
}

// PlanPersistent writes the CA file and an engine config folder under
// runnerDir and returns the env lines. systemBundle is the host's public
// bundle, which the combined file starts with, so tools that replace the trust
// list (SSL_CERT_FILE and the like) still trust public sites.
func PlanPersistent(ctx context.Context, s *Store, f Files, engine, runnerDir string, systemBundle []byte) (PersistentPlan, error) {
	var p PersistentPlan
	bundle, err := s.Bundle(ctx)
	if err != nil {
		return p, err
	}
	caDir := filepath.Join(runnerDir, "crsp-ca")
	if len(bundle) > 0 {
		if err := f.MkdirAll(caDir, 0o755); err != nil {
			return p, fmt.Errorf("make the runner CA folder: %w", err)
		}
		extra := filepath.Join(caDir, "private-ca.pem")
		combined := filepath.Join(caDir, "combined.pem")
		if err := f.WriteFile(extra, bundle, 0o644); err != nil {
			return p, fmt.Errorf("write the CA bundle: %w", err)
		}
		all := append(append([]byte(nil), systemBundle...), '\n')
		all = append(all, bundle...)
		if err := f.WriteFile(combined, all, 0o644); err != nil {
			return p, fmt.Errorf("write the combined CA file: %w", err)
		}
		p.Env = append(p.Env,
			"NODE_EXTRA_CA_CERTS="+extra,
			"SSL_CERT_FILE="+combined,
			"GIT_SSL_CAINFO="+combined,
			"REQUESTS_CA_BUNDLE="+combined,
			"CARGO_HTTP_CAINFO="+combined,
		)
	}
	logins, err := s.Logins(ctx, KindPersistent)
	if err != nil {
		return p, err
	}
	if len(logins) > 0 {
		cfg := filepath.Join(runnerDir, "docker-config")
		if err := f.MkdirAll(cfg, 0o700); err != nil {
			return p, fmt.Errorf("make the runner engine config folder: %w", err)
		}
		p.Env = append(p.Env, "DOCKER_CONFIG="+cfg)
		for _, l := range logins {
			pw, err := s.Password(ctx, l)
			if err != nil {
				return p, err
			}
			p.Logins = append(p.Logins, Command{Name: engine, Args: []string{"login", l.Host, "--username", l.Username, "--password-stdin"},
				Env: []string{"DOCKER_CONFIG=" + cfg}, Stdin: pw})
		}
	}
	return p, nil
}

// RunLogins runs login commands. The error text never holds a command's
// Stdin.
func RunLogins(ctx context.Context, e Exec, cmds []Command) error {
	for _, c := range cmds {
		if err := e.Run(ctx, c); err != nil {
			return fmt.Errorf("%s %s: %w", c.Name, strings.Join(c.Args[:min(2, len(c.Args))], " "), err)
		}
	}
	return nil
}

// OSExec runs commands on this machine with exec.CommandContext. It passes the
// Stdin of a command on the process's standard input and nothing else.
type OSExec struct{}

// Run implements Exec.
func (OSExec) Run(ctx context.Context, c Command) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if len(c.Env) > 0 {
		cmd.Env = append(cmd.Environ(), c.Env...)
	}
	if c.Stdin.v != "" {
		cmd.Stdin = strings.NewReader(c.Stdin.v)
	}
	// The output is dropped: an engine can echo what it was given.
	return cmd.Run()
}
