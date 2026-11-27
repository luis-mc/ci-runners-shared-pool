//go:build windows

package tray

import (
	"bytes"
	"image/png"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OSToast shows one desktop toast with the crsp icon and calls click when the person clicks
// it. It is a balloon of the Windows notification area (Windows 10 and 11 draw it as a toast):
// no modal box, no PowerShell. The notification icon lives only while the toast shows.
func OSToast(title, body string, click func()) { go toast(title, body, click) }

const (
	wmToast       = 0x8000 + 7
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nifMessage    = 0x1
	nifIcon       = 0x2
	nifTip        = 0x4
	nifInfo       = 0x10
	niifUser      = 0x4
	niifLarge     = 0x20
	ninUserClick  = 0x405
	ninTimeout    = 0x404
	ninHide       = 0x403
	hwndMessage   = ^uintptr(2) // HWND_MESSAGE = -3
	toastLifetime = 30 * time.Second
)

type notifyIconData struct {
	Size            uint32
	Hwnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

type toastMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
	Private uint32
}

type toastClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

func fill(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
}

func toast(title, body string, click func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user32 := windows.NewLazySystemDLL("user32.dll")
	shell := windows.NewLazySystemDLL("shell32.dll")
	register, create := user32.NewProc("RegisterClassExW"), user32.NewProc("CreateWindowExW")
	def, getMsg := user32.NewProc("DefWindowProcW"), user32.NewProc("GetMessageW")
	dispatch, post := user32.NewProc("DispatchMessageW"), user32.NewProc("PostQuitMessage")
	destroy, icon := user32.NewProc("DestroyWindow"), user32.NewProc("CreateIconFromResourceEx")
	setTimer := user32.NewProc("SetTimer")
	notify := shell.NewProc("Shell_NotifyIconW")

	var b bytes.Buffer
	_ = png.Encode(&b, DrawIcon(32, DiscColour(StateHealthy)))
	data := b.Bytes()
	hicon, _, _ := icon.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, 32, 32, 0)

	done := false
	proc := syscall.NewCallback(func(h, m, wp, lp uintptr) uintptr {
		switch m {
		case wmToast:
			switch lp & 0xffff {
			case ninUserClick:
				if click != nil && !done {
					done = true
					click()
				}
				_, _, _ = destroy.Call(h)
			case ninTimeout, ninHide:
				_, _, _ = destroy.Call(h)
			}
			return 0
		case 0x0113: // WM_TIMER: the toast has lived long enough
			_, _, _ = destroy.Call(h)
			return 0
		case 0x0002: // WM_DESTROY
			_, _, _ = post.Call(0)
			return 0
		}
		r, _, _ := def.Call(h, m, wp, lp)
		return r
	})
	name, _ := windows.UTF16PtrFromString("CrspToast")
	inst, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	cls := toastClass{WndProc: proc, Instance: inst, ClassName: name}
	cls.Size = uint32(unsafe.Sizeof(cls))
	_, _, _ = register.Call(uintptr(unsafe.Pointer(&cls)))
	h, _, _ := create.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, inst, 0)
	if h == 0 {
		return
	}
	nid := notifyIconData{Hwnd: h, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmToast, Icon: hicon}
	nid.Size = uint32(unsafe.Sizeof(nid))
	fill(nid.Tip[:], ProductName)
	if r, _, _ := notify.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return
	}
	nid.Flags = nifInfo
	nid.InfoFlags = niifUser | niifLarge
	nid.BalloonIcon = hicon
	fill(nid.Info[:], body)
	fill(nid.InfoTitle[:], title)
	_, _, _ = notify.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	_, _, _ = setTimer.Call(h, 1, uintptr(toastLifetime/time.Millisecond), 0)

	var m toastMsg
	for {
		r, _, _ := getMsg.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		_, _, _ = dispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
	_, _, _ = notify.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}
