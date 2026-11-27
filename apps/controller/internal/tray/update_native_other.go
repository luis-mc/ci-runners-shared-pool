//go:build !windows

package tray

import (
	"context"
	"os"
	"os/exec"
)

// OSNativeWindow is the update window of macOS and Linux (TRAY7, D19): a web page in an app
// window of the browser, in its own profile so it opens at its own size. The tray starts
// `crsp update-window` from its own binary; the page is served by that process, which lives
// on while the update restarts the service. With no Chromium-family browser there is no such
// window, the call fails with ErrNoNativeWindow and the console page opens instead (R5.43).
func OSNativeWindow(exe, version string) NativeWindow {
	return func(_ context.Context, mode string, st Status) error {
		if _, err := OSBrowserLookup().Find(); err != nil {
			return ErrNoNativeWindow
		}
		if exe == "" {
			var err error
			if exe, err = os.Executable(); err != nil {
				return err
			}
		}
		from := version
		if st.Version != "" {
			from = st.Version
		}
		args := append([]string{UpdateWindowCommand, "--web"}, UpdateWindowArgs(mode, from, st.Update.Latest, 0, 0, false)...)
		cmd := exec.CommandContext(context.Background(), exe, args...) //nolint:gosec // our own binary
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
}

// OSFirstStart starts the first-start window once per user (TRAY7, D20). It does nothing when
// the window was shown before or no browser can show it.
func OSFirstStart(exe string) {
	if !FirstStartDue() {
		return
	}
	if _, err := OSBrowserLookup().Find(); err != nil {
		return
	}
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return
		}
	}
	cmd := exec.CommandContext(context.Background(), exe, FirstStartCommand) //nolint:gosec // our own binary
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// OSToast is the desktop notice; a click cannot be caught here.
func OSToast(title, body string, _ func()) { DesktopNotice(title, body) }
