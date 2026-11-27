//go:build windows

package tray

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const wmClose = 0x0010

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	pEnumWindows     = user32.NewProc("EnumWindows")
	pGetClassName    = user32.NewProc("GetClassNameW")
	pGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
	pPostMessage     = user32.NewProc("PostMessageW")
)

type winProcs struct{}

func newProcs() procLister { return winProcs{} }

// windows returns the handles of the top-level windows of class "SystrayClass",
// the hidden window every tray owns, with their process ids.
func systrayWindows() map[int]uintptr {
	out := map[int]uintptr{}
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		buf := make([]uint16, 64)
		n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if windows.UTF16ToString(buf[:n]) != "SystrayClass" {
			return 1
		}
		var pid uint32
		_, _, _ = pGetWindowThread.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid != 0 {
			out[int(pid)] = h
		}
		return 1
	})
	_, _, _ = pEnumWindows.Call(cb, 0)
	return out
}

func imageDir(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	p := windows.UTF16ToString(buf[:n])
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[:i]
	}
	return ""
}

// Trays are the processes that own a systray window and run an image from dir.
func (winProcs) Trays(dir string) ([]int, error) {
	var out []int
	self := windows.GetCurrentProcessId()
	for pid := range systrayWindows() {
		if uint32(pid) != self && strings.EqualFold(imageDir(pid), dir) {
			out = append(out, pid)
		}
	}
	return out, nil
}

// Ask posts WM_CLOSE to the tray's window: the systray library ends its loop on it.
func (winProcs) Ask(pid int) error {
	h, ok := systrayWindows()[pid]
	if !ok {
		return nil
	}
	r, _, err := pPostMessage.Call(h, wmClose, 0, 0)
	if r == 0 {
		return err
	}
	return nil
}

func (winProcs) Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

func (winProcs) Kill(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 0)
}
