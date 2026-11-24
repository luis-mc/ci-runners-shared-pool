//go:build windows

package leftovers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// WindowsRoot is the folder of the runner folders on Windows.
const WindowsRoot = `C:\ProgramData\crsp\runners`

type hostOS struct {
	hostFS
	cmd sys.Commands
}

// NewHostOS is the real OS layer of this host. Services go through the service
// control manager by their exact name (never a wildcard on a display name:
// Get-Service cannot open a name of 84 characters); accounts through PowerShell.
func NewHostOS() OS { return &hostOS{hostFS: hostFS{root: WindowsRoot}, cmd: sys.ExecCommands{}} }

func (*hostOS) GOOS() string { return "windows" }

func (*hostOS) Admin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// connect opens the service control manager with the rights to list and read.
func connectRead() (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	return &mgr.Mgr{Handle: h}, nil
}

func scmErr(err error) error {
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("%w: %v", ErrNotExist, err)
	}
	return err
}

func (*hostOS) Services(context.Context) ([]OSService, error) {
	m, err := connectRead()
	if err != nil {
		return nil, fmt.Errorf("connect to the service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	names, err := m.ListServices()
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	var out []OSService
	for _, n := range names {
		if !strings.HasPrefix(n, ServicePrefix) {
			continue
		}
		name16, err := windows.UTF16PtrFromString(n)
		if err != nil {
			continue
		}
		h, err := windows.OpenService(m.Handle, name16, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
		if err != nil {
			// A service that vanished, or one we may not read, is not listed.
			continue
		}
		s := &mgr.Service{Name: n, Handle: h}
		cfg, cerr := s.Config()
		st, qerr := s.Query()
		_ = s.Close()
		if cerr != nil {
			continue
		}
		o := OSService{Name: n, Binary: cfg.BinaryPathName, Account: cfg.ServiceStartName}
		if qerr == nil {
			o.Running = st.State == svc.Running
		}
		out = append(out, o)
	}
	return out, nil
}

func (*hostOS) StopService(ctx context.Context, name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(name)
	if err != nil {
		return scmErr(err)
	}
	defer func() { _ = s.Close() }()
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Stopped {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return errors.New("the service did not stop within 30 seconds")
}

func (*hostOS) DeleteService(_ context.Context, name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(name)
	if err != nil {
		return scmErr(err)
	}
	defer func() { _ = s.Close() }()
	return scmErr(s.Delete())
}

const (
	scriptAccounts = `$ErrorActionPreference = 'Stop'
$rows = @(Get-LocalUser | ForEach-Object { [pscustomobject]@{ Name = $_.Name; Description = $_.Description } })
ConvertTo-Json -InputObject $rows -Compress
`
	scriptDeleteAccount = `$ErrorActionPreference = 'Stop'
if (-not (Get-LocalUser -Name $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue)) { [Console]::Error.WriteLine('crsp-not-found'); exit 3 }
Remove-LocalUser -Name $env:CRSP_ACCOUNT
`
)

// psStdin ends a script with a blank line. `powershell -Command -` reads
// stdin line by line and runs a multi-line statement only after a blank line
// (OPS92).
func psStdin(script string) string {
	return strings.TrimRight(script, "\r\n") + "\n\n"
}

func (h *hostOS) ps(ctx context.Context, script string, env map[string]string) (sys.Result, error) {
	return h.cmd.Run(ctx, sys.Command{
		Name:  "powershell.exe",
		Args:  []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-"},
		Stdin: psStdin(script),
		Env:   env,
	})
}

func (h *hostOS) Accounts(ctx context.Context) ([]OSAccount, error) {
	res, err := h.ps(ctx, scriptAccounts, nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("powershell exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" || out == "null" {
		return nil, nil
	}
	if strings.HasPrefix(out, "{") {
		out = "[" + out + "]"
	}
	var rows []OSAccount
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("read the account list: %w", err)
	}
	return rows, nil
}

func (h *hostOS) DeleteAccount(ctx context.Context, name string) error {
	res, err := h.ps(ctx, scriptDeleteAccount, map[string]string{"CRSP_ACCOUNT": name})
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		return nil
	case 3:
		return fmt.Errorf("%w: account %s", ErrNotExist, name)
	}
	return fmt.Errorf("powershell exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
}
