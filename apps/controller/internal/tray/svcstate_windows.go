//go:build windows

package tray

import (
	"context"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ServiceState reads the stop state from the service control manager (R5.12).
func ServiceState(_ context.Context) (Status, bool) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return Status{}, false
	}
	m := &mgr.Mgr{Handle: h}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return Status{}, false
	}
	defer func() { _ = s.Close() }()
	q, err := s.Query()
	if err != nil || q.State == svc.Running {
		return Status{}, false
	}
	code := int(q.Win32ExitCode)
	if q.Win32ExitCode == uint32(windows.ERROR_SERVICE_SPECIFIC_ERROR) {
		code = int(q.ServiceSpecificExitCode)
	}
	return Status{State: StateStopped, ExitCode: code, StoppedAt: time.Time{}}, true
}
