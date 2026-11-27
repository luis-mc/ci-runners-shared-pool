//go:build linux

package leftovers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Linux locations (the same as internal/backend/linux).
const (
	linuxRoot    = "/var/lib/crsp/runners"
	linuxUnitDir = "/etc/systemd/system"
)

type hostOS struct {
	hostFS
	cmd     sys.Commands
	unitDir string
	passwd  string
}

// NewHostOS is the real OS layer of this host: systemd unit files, /etc/passwd.
func NewHostOS() OS {
	return &hostOS{hostFS: hostFS{root: linuxRoot}, cmd: sys.ExecCommands{}, unitDir: linuxUnitDir, passwd: "/etc/passwd"}
}

func (*hostOS) GOOS() string { return "linux" }
func (*hostOS) Admin() bool  { return os.Geteuid() == 0 }

func (h *hostOS) Services(ctx context.Context) ([]OSService, error) {
	units, err := filepath.Glob(filepath.Join(h.unitDir, ServicePrefix+"*.service"))
	if err != nil {
		return nil, err
	}
	var out []OSService
	for _, u := range units {
		b, err := os.ReadFile(u) //nolint:gosec // the systemd unit folder
		if err != nil {
			continue
		}
		s := OSService{Name: strings.TrimSuffix(filepath.Base(u), ".service")}
		var work, exec string
		for _, l := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
			if !ok {
				continue
			}
			switch k {
			case "User":
				s.Account = v
			case "WorkingDirectory":
				work = strings.TrimPrefix(v, "!")
			case "ExecStart":
				exec = strings.TrimLeft(strings.Fields(v + " ")[0], "@-+!:")
			}
		}
		s.Binary = exec
		if s.Binary == "" {
			s.Binary = work
		}
		if res, err := h.cmd.Run(ctx, sys.Command{Name: "systemctl", Args: []string{"is-active", s.Name + ".service"}}); err == nil {
			s.Running = strings.TrimSpace(res.Stdout) == "active"
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *hostOS) systemctl(ctx context.Context, args ...string) (sys.Result, error) {
	res, err := h.cmd.Run(ctx, sys.Command{Name: "systemctl", Args: args})
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("systemctl %s exited %d: %s", args[0], res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res, err
}

func (h *hostOS) unitPath(name string) (string, error) {
	if !strings.HasPrefix(name, ServicePrefix) || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("not a runner service: %s", name)
	}
	return filepath.Join(h.unitDir, name+".service"), nil
}

func (h *hostOS) StopService(ctx context.Context, name string) error {
	p, err := h.unitPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotExist, name)
	}
	if _, err := h.systemctl(ctx, "stop", name+".service"); err != nil {
		return err
	}
	return nil
}

func (h *hostOS) DeleteService(ctx context.Context, name string) error {
	p, err := h.unitPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotExist, name)
	}
	if _, err := h.systemctl(ctx, "disable", name+".service"); err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_, err = h.systemctl(ctx, "daemon-reload")
	return err
}

// Accounts reads /etc/passwd. useradd gives the runner users no description, so
// a crsp- user whose home is a folder under the runners folder is a crsp runner account.
func (h *hostOS) Accounts(context.Context) ([]OSAccount, error) {
	b, err := os.ReadFile(h.passwd)
	if err != nil {
		return nil, err
	}
	var out []OSAccount
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 6 || f[0] == "" {
			continue
		}
		desc := f[4]
		if strings.HasPrefix(f[0], "crsp-") && strings.HasPrefix(f[5], h.root+"/") {
			desc = AccountDescription
		}
		out = append(out, OSAccount{Name: f[0], Description: desc})
	}
	return out, nil
}

func (h *hostOS) DeleteAccount(ctx context.Context, name string) error {
	if !strings.HasPrefix(name, "crsp-") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("not a crsp runner account: %s", name)
	}
	res, err := h.cmd.Run(ctx, sys.Command{Name: "userdel", Args: []string{name}})
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		return nil
	case 6: // the user does not exist
		return fmt.Errorf("%w: account %s", ErrNotExist, name)
	}
	return fmt.Errorf("userdel exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
}
