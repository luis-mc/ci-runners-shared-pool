//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
)

// windowsServiceName is the service the MSI registers (operations R5.2); the
// tray reads the same name (internal/tray.ServiceName).
const windowsServiceName = "crsp"

// stopWait is how long Stop waits for the controller to end before the service
// reports stopped anyway.
const stopWait = 25 * time.Second

// runAsService runs crsp under the Windows service control manager when the
// process was started by it, and reports whether it did. The service's command
// line is `crsp.exe controller`; Stop cancels the controller's context, so it
// shuts down as it does on SIGTERM elsewhere.
func runAsService() bool {
	if is, err := svc.IsWindowsService(); err != nil || !is {
		return false
	}
	if err := svc.Run(windowsServiceName, &serviceHandler{}); err != nil {
		os.Exit(1)
	}
	return true
}

type serviceHandler struct{}

func (h *serviceHandler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseContext = ctx

	logOut := os.Stderr
	if f := openServiceLog(); f != nil {
		defer func() { _ = f.Close() }()
		logOut = f
		os.Stderr = f
	}

	done := make(chan int, 1)
	go func() { done <- run(os.Args[1:], logOut, logOut) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: uint32(stopWait / time.Millisecond)}
				cancel()
				select {
				case <-done:
				case <-time.After(stopWait):
				}
				return false, 0
			}
		case code := <-done:
			// The controller ended on its own (a staged restore asks for a
			// restart): a non-zero code makes the service manager restart it.
			if code != 0 {
				return true, uint32(code)
			}
			return false, 0
		}
	}
}

// openServiceLog opens <data folder>\logs\crsp.log for the controller's log; a
// service has no console. When that cannot be opened (an upgrade can leave the
// folder or file unwritable) it uses another file, and the controller logs why
// at startup. Nil only when no file can be opened at all.
func openServiceLog() *os.File {
	paths := config.DefaultPaths("windows", os.Getenv)
	f, problems, _ := openLogFile(filepath.Join(paths.DataDir, "logs"), os.TempDir(), os.Getpid())
	serviceLogProblems = problems
	if f != nil {
		serviceLogPath = f.Name()
	}
	return f
}

// hideConsoleForTray detaches `crsp tray` from its console window, so the
// autostart entry and the Start Menu shortcut leave no black window.
func hideConsoleForTray(args []string) {
	if len(args) == 0 || args[0] != "tray" {
		return
	}
	_, _, _ = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole").Call()
}
