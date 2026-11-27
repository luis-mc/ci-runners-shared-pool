//go:build windows

package updatewindow

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Win32 calls of the window, by syscall: no cgo and no GUI framework.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx        = user32.NewProc("RegisterClassExW")
	pCreateWindowEx         = user32.NewProc("CreateWindowExW")
	pDefWindowProc          = user32.NewProc("DefWindowProcW")
	pGetMessage             = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessage        = user32.NewProc("DispatchMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pShowWindow             = user32.NewProc("ShowWindow")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pAdjustWindowRectExFor  = user32.NewProc("AdjustWindowRectExForDpi")
	pSetWindowPos           = user32.NewProc("SetWindowPos")
	pGetWindowRect          = user32.NewProc("GetWindowRect")
	pBeginPaint             = user32.NewProc("BeginPaint")
	pEndPaint               = user32.NewProc("EndPaint")
	pInvalidateRect         = user32.NewProc("InvalidateRect")
	pSetTimer               = user32.NewProc("SetTimer")
	pKillTimer              = user32.NewProc("KillTimer")
	pLoadCursor             = user32.NewProc("LoadCursorW")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pMonitorFromPoint       = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfo         = user32.NewProc("GetMonitorInfoW")
	pPostMessage            = user32.NewProc("PostMessageW")
	pSendMessage            = user32.NewProc("SendMessageW")
	pFindWindow             = user32.NewProc("FindWindowW")
	pSetProcessDpiAwareness = user32.NewProc("SetProcessDpiAwarenessContext")
	pDestroyWindow          = user32.NewProc("DestroyWindow")
	pSetCapture             = user32.NewProc("SetCapture")
	pReleaseCapture         = user32.NewProc("ReleaseCapture")
	pGetKeyState            = user32.NewProc("GetKeyState")
	pFlashWindowEx          = user32.NewProc("FlashWindowEx")
	pCreateIconFromResource = user32.NewProc("CreateIconFromResourceEx")
	pGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	pTrackMouseEvent        = user32.NewProc("TrackMouseEvent")
	pFillRect               = user32.NewProc("FillRect")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pIsIconic               = user32.NewProc("IsIconic")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pCreateFont             = gdi32.NewProc("CreateFontW")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pTextOut                = gdi32.NewProc("TextOutW")
	pGetTextExtentPoint32   = gdi32.NewProc("GetTextExtentPoint32W")
	pGetTextMetrics         = gdi32.NewProc("GetTextMetricsW")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pSetDIBitsToDevice      = gdi32.NewProc("SetDIBitsToDevice")

	pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	pGetDpiForMonitor      = shcore.NewProc("GetDpiForMonitor")
	pMulDiv                = kernel32.NewProc("MulDiv")
)

// Messages, styles and flags.
const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmPaint        = 0x000F
	wmEraseBkgnd   = 0x0014
	wmKeyDown      = 0x0100
	wmTimer        = 0x0113
	wmMouseMove    = 0x0200
	wmLButtonDown  = 0x0201
	wmLButtonUp    = 0x0202
	wmMouseLeave   = 0x02A3
	wmDPIChanged   = 0x02E0
	wmApp          = 0x8000
	wmSetIcon      = 0x0080
	wmSetCursor    = 0x0020
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsClipChildren = 0x02000000
	swShow         = 5
	swRestore      = 9
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	idcArrow       = 32512
	srccopy        = 0x00CC0020
	transparent    = 1
	vkTab          = 0x09
	vkReturn       = 0x0D
	vkEscape       = 0x1B
	vkSpace        = 0x20
	vkLeft         = 0x25
	vkUp           = 0x26
	vkRight        = 0x27
	vkDown         = 0x28
	vkShift        = 0x10
	fwNormal       = 400
	fwBold         = 700
	dwmDarkMode    = 20
	monitorNearest = 2
	flashTray      = 0x2
	flashTimerNoFG = 0xC
	iconBig        = 1
	iconSmall      = 0
	smCxIcon       = 11
	smCxSmIcon     = 49
	tmeLeave       = 0x2
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
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

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	Rc        rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type textMetric struct {
	Height, Ascent, Descent, InternalLeading, ExternalLeading int32
	AveCharWidth, MaxCharWidth, Weight, Overhang              int32
	DigitizedAspectX, DigitizedAspectY                        int32
	FirstChar, LastChar, DefaultChar, BreakChar               uint16
	Italic, Underlined, StruckOut, PitchAndFamily, CharSet    uint8
}

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type flashInfo struct {
	Size    uint32
	Hwnd    uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

type trackMouse struct {
	Size      uint32
	Flags     uint32
	Hwnd      uintptr
	HoverTime uint32
}

type bitmapInfo struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func wstr(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func mulDiv(a, b, c int) int {
	r, _, _ := pMulDiv.Call(uintptr(int32(a)), uintptr(int32(b)), uintptr(int32(c)))
	return int(int32(r))
}

func ptr[T any](v *T) uintptr { return uintptr(unsafe.Pointer(v)) }

func invalidate(h uintptr) { _, _, _ = pInvalidateRect.Call(h, 0, 0) }

func dpiOfMonitor(mon uintptr) int {
	var x, y uint32
	if r, _, _ := pGetDpiForMonitor.Call(mon, 0, ptr(&x), ptr(&y)); r != 0 || x == 0 {
		return 96
	}
	return int(x)
}

func rgb(c RGB) uintptr { return uintptr(c.COLORREF()) }

func fillRectColor(dc uintptr, r rect, c RGB) {
	br, _, _ := pCreateSolidBrush.Call(rgb(c))
	_, _, _ = pFillRect.Call(dc, ptr(&r), br)
	_, _, _ = pDeleteObject.Call(br)
}

// openURL opens a page with the default browser; it never blocks the window.
func openURL(url string) {
	if url == "" {
		return
	}
	_ = windows.ShellExecute(0, wstr("open"), wstr(url), nil, nil, swShow)
}
