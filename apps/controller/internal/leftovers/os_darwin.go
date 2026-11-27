//go:build darwin

package leftovers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// macOS locations (the same as internal/backend/macos).
const (
	darwinRoot       = "/usr/local/var/crsp/runners"
	darwinDaemonsDir = "/Library/LaunchDaemons"
)

type hostOS struct {
	hostFS
	cmd sys.Commands
	dir string
}

// NewHostOS is the real OS layer of this host: launchd plists, dscl users.
func NewHostOS() OS {
	return &hostOS{hostFS: hostFS{root: darwinRoot}, cmd: sys.ExecCommands{}, dir: darwinDaemonsDir}
}

func (*hostOS) GOOS() string { return "darwin" }
func (*hostOS) Admin() bool  { return os.Geteuid() == 0 }

var plistString = regexp.MustCompile(`<key>(UserName|WorkingDirectory)</key>\s*<string>([^<]*)</string>`)

func (h *hostOS) Services(ctx context.Context) ([]OSService, error) {
	plists, err := filepath.Glob(filepath.Join(h.dir, ServicePrefix+"*.plist"))
	if err != nil {
		return nil, err
	}
	loaded := ""
	if res, err := h.cmd.Run(ctx, sys.Command{Name: "launchctl", Args: []string{"list"}}); err == nil {
		loaded = res.Stdout
	}
	var out []OSService
	for _, p := range plists {
		b, err := os.ReadFile(p) //nolint:gosec // the launchd daemons folder
		if err != nil {
			continue
		}
		s := OSService{Name: strings.TrimSuffix(filepath.Base(p), ".plist")}
		for _, m := range plistString.FindAllStringSubmatch(string(b), -1) {
			if m[1] == "UserName" {
				s.Account = m[2]
			} else {
				s.Binary = m[2]
			}
		}
		for _, l := range strings.Split(loaded, "\n") {
			if f := strings.Fields(l); len(f) >= 3 && f[2] == s.Name && f[0] != "-" {
				s.Running = true
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *hostOS) plistPath(name string) (string, error) {
	if !strings.HasPrefix(name, ServicePrefix) || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("not a runner service: %s", name)
	}
	return filepath.Join(h.dir, name+".plist"), nil
}

func (h *hostOS) StopService(ctx context.Context, name string) error {
	p, err := h.plistPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotExist, name)
	}
	// bootout fails when the job is not loaded; that is a stopped service.
	_, err = h.cmd.Run(ctx, sys.Command{Name: "launchctl", Args: []string{"bootout", "system/" + name}})
	return err
}

func (h *hostOS) DeleteService(_ context.Context, name string) error {
	p, err := h.plistPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotExist, name)
		}
		return err
	}
	return nil
}

// Accounts lists the users by RealName; the runner users are made with the
// real name "CRSP runner <user>", which stands for the crsp description.
func (h *hostOS) Accounts(ctx context.Context) ([]OSAccount, error) {
	res, err := h.cmd.Run(ctx, sys.Command{Name: "dscl", Args: []string{".", "-list", "/Users", "RealName"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("dscl exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var out []OSAccount
	for _, l := range strings.Split(res.Stdout, "\n") {
		name, real, _ := strings.Cut(strings.TrimSpace(l), " ")
		real = strings.TrimSpace(real)
		if name == "" {
			continue
		}
		desc := real
		if strings.HasPrefix(name, "crsp-") && strings.HasPrefix(real, "CRSP runner ") {
			desc = AccountDescription
		}
		out = append(out, OSAccount{Name: name, Description: desc})
	}
	return out, nil
}

func (h *hostOS) DeleteAccount(ctx context.Context, name string) error {
	if !strings.HasPrefix(name, "crsp-") {
		return fmt.Errorf("not a crsp runner account: %s", name)
	}
	res, err := h.cmd.Run(ctx, sys.Command{Name: "dscl", Args: []string{".", "-delete", "/Users/" + name}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Stderr, "eDSRecordNotFound") || strings.Contains(res.Stderr, "not found") {
			return fmt.Errorf("%w: account %s", ErrNotExist, name)
		}
		return fmt.Errorf("dscl exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
