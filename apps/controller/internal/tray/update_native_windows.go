//go:build windows

package tray

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OSNativeWindow starts the update window from a copy of the signed binary in the temp
// folder, so the window keeps running while the update replaces the program folder.
func OSNativeWindow(exe, version string) NativeWindow {
	return func(_ context.Context, mode string, st Status) error {
		copyPath, err := stageCopy(exe, version)
		if err != nil {
			return err
		}
		x, y, hasAt := cursorPos()
		from := version
		if st.Version != "" {
			from = st.Version
		}
		args := append([]string{UpdateWindowCommand}, UpdateWindowArgs(mode, from, st.Update.Latest, x, y, hasAt)...)
		cmd := exec.CommandContext(context.Background(), copyPath, args...) //nolint:gosec // our own signed binary
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}   // CREATE_NO_WINDOW: a window, not a console
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
}

// stageCopy copies exe to %TEMP%\crsp-update-window\crsp-<version>.exe unless it is there
// already (a running window holds the file, and that one is the right one). Older copies are
// removed when they are not in use.
func stageCopy(exe, version string) (string, error) {
	dir := filepath.Join(os.TempDir(), "crsp-update-window")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "crsp-" + strings.NewReplacer(`\`, "_", "/", "_", ":", "_").Replace(version) + ".exe"
	dst := filepath.Join(dir, name)
	if old, _ := filepath.Glob(filepath.Join(dir, "crsp-*.exe")); len(old) > 0 {
		for _, o := range old {
			if o != dst {
				_ = os.Remove(o) // fails while a window of that copy runs; fine
			}
		}
	}
	src, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	if d, err := os.Stat(dst); err == nil && d.Size() == src.Size() {
		return dst, nil
	}
	in, err := os.Open(exe) //nolint:gosec // our own binary
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700) //nolint:gosec // temp folder of this user
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, serr := os.Stat(dst); serr == nil {
			return dst, nil // another start won the race
		}
		return "", err
	}
	return dst, nil
}

func cursorPos() (x, y int, ok bool) {
	var p struct{ X, Y int32 }
	r, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y), r != 0
}
