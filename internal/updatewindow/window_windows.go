//go:build windows

package updatewindow

import (
	"bytes"
	"context"
	"image/png"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

const (
	className  = "CrspUpdateWindow"
	mutexName  = `Local\crsp-update-window`
	timerTick  = 1
	timerAnim  = 2
	wmRefresh  = wmApp + 1
	pollEvery  = time.Second
	installMax = 3 * time.Minute // the service answers Install after the first stage
)

// win is the one window of the process.
type win struct {
	hwnd  uintptr
	dpi   int
	fonts *fonts
	svc   Service

	mu    sync.Mutex // the model and ui are touched by the window thread and the poller
	model *Model
	ui    uiState

	layout Layout
	view   View
	lastSt State
	animOn bool
}

var theWin *win

// runWindow is the native update window. It returns when the window closes.
func runWindow(c Config, o Options) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Per-monitor DPI aware v2: Windows never scales the window as a bitmap.
	_, _, _ = pSetProcessDpiAwareness.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4

	// One window only: a second start brings the first to the front.
	if _, err := windows.CreateMutex(nil, false, wstr(mutexName)); err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			focusFirst()
			return 0
		}
	}

	w := &win{svc: o.Service, model: NewModel(c.From, c.To, c.Mode), ui: newUI()}
	theWin = w
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A first reading before the window opens: it decides what the first picture is.
	if w.svc != nil {
		sctx, sc := context.WithTimeout(ctx, 4*time.Second)
		st, err := w.svc.Status(sctx)
		sc()
		w.model.Observe(SnapshotOf(st, err))
		if w.model.From == "" && err == nil {
			w.model.From = st.Version
		}
	}

	pt := point{}
	if c.HasAt {
		pt = point{int32(c.At[0]), int32(c.At[1])}
	} else {
		_, _, _ = pGetCursorPos.Call(ptr(&pt))
	}
	mon, _, _ := pMonitorFromPoint.Call(uintptr(*(*int64)(unsafe.Pointer(&pt))), monitorNearest)
	w.dpi = dpiOfMonitor(mon)
	var mi monitorInfo
	mi.Size = uint32(unsafe.Sizeof(mi))
	_, _, _ = pGetMonitorInfo.Call(mon, ptr(&mi))

	w.fonts = newFonts(w.dpi)
	w.relayout()
	outerW, outerH := w.outerSize()
	x := int(mi.Work.Left) + (int(mi.Work.Right-mi.Work.Left)-outerW)/2
	y := int(mi.Work.Top) + (int(mi.Work.Bottom-mi.Work.Top)-outerH)/2

	cursor, _, _ := pLoadCursor.Call(0, idcArrow)
	big, small := loadIcons(w.dpi)
	inst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cls := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: syscall.NewCallback(wndProc), Instance: inst,
		Icon: big, IconSm: small, Cursor: cursor, ClassName: wstr(className)}
	_, _, _ = pRegisterClassEx.Call(ptr(&cls))
	// A caption with the close button only: no minimise, no maximise, no resize frame.
	h, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(wstr(className))), uintptr(unsafe.Pointer(wstr(WindowTitle))),
		uintptr(wsCaption|wsSysMenu|wsClipChildren), uintptr(int32(x)), uintptr(int32(y)), uintptr(outerW), uintptr(outerH), 0, 0, inst, 0)
	if h == 0 {
		say(o.Stderr, "error: could not create the window")
		return 1
	}
	w.hwnd = h
	_, _, _ = pSendMessage.Call(h, wmSetIcon, iconBig, big)
	_, _, _ = pSendMessage.Call(h, wmSetIcon, iconSmall, small)
	dark := int32(1)
	_, _, _ = pDwmSetWindowAttribute.Call(h, dwmDarkMode, ptr(&dark), 4)

	// Shown once and brought to the front once; never always-on-top.
	_, _, _ = pShowWindow.Call(h, swShow)
	_, _, _ = pSetForegroundWindow.Call(h)
	w.afterChange()

	go w.poll(ctx)

	var m msg
	for {
		r, _, _ := pGetMessage.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		_, _, _ = pTranslateMessage.Call(ptr(&m))
		_, _, _ = pDispatchMessage.Call(ptr(&m))
	}
	w.fonts.free()
	return 0
}

// focusFirst brings the window of the first start to the front.
func focusFirst() {
	h, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(wstr(className))), 0)
	if h == 0 {
		return
	}
	if r, _, _ := pIsIconic.Call(h); r != 0 {
		_, _, _ = pShowWindow.Call(h, swRestore)
	}
	_, _, _ = pSetForegroundWindow.Call(h)
}

// loadIcons makes the crsp disc icon for the title bar and the taskbar at this DPI.
func loadIcons(dpi int) (big, small uintptr) {
	mk := func(metric uintptr) uintptr {
		cx, _, _ := pGetSystemMetricsForDpi.Call(metric, uintptr(dpi))
		n := int(cx)
		if n < 16 {
			n = 16
		}
		var b bytes.Buffer
		_ = png.Encode(&b, tray.DrawIcon(n, tray.DiscColour(tray.StateHealthy)))
		data := b.Bytes()
		h, _, _ := pCreateIconFromResource.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, uintptr(n), uintptr(n), 0)
		return h
	}
	return mk(smCxIcon), mk(smCxSmIcon)
}

// relayout computes the view and the layout for this DPI. The caller holds no lock.
func (w *win) relayout() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.view = w.model.View()
	w.layout = Compute(w.fonts.metrics(), w.view)
}

func (w *win) outerSize() (int, int) {
	r := rect{0, 0, int32(w.layout.W), int32(w.layout.H)}
	_, _, _ = pAdjustWindowRectExFor.Call(ptr(&r), uintptr(wsCaption|wsSysMenu), 0, 0, uintptr(w.dpi))
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// afterChange follows a change of the view: it resizes the window to the content, keeps the
// focus on a button that exists, starts or stops the bar's animation, and flashes the
// taskbar button when the update ends.
func (w *win) afterChange() {
	prev := w.layout
	w.relayout()
	w.mu.Lock()
	v, l := w.view, w.layout
	if w.ui.focus < 0 || w.ui.focus >= len(v.Buttons) || w.lastSt != v.State {
		w.ui.focus = indexOf(v, v.Default)
	}
	if w.ui.hot >= len(v.Buttons) {
		w.ui.hot = -1
	}
	changed := w.lastSt != v.State
	w.lastSt = v.State
	w.mu.Unlock()

	if l.W != prev.W || l.H != prev.H {
		ow, oh := w.outerSize()
		var wr rect
		_, _, _ = pGetWindowRect.Call(w.hwnd, ptr(&wr))
		_, _, _ = pSetWindowPos.Call(w.hwnd, 0, uintptr(wr.Left), uintptr(wr.Top), uintptr(ow), uintptr(oh), swpNoZOrder|swpNoActivate)
	}
	if v.Bar == BarIndeterminate && !w.animOn {
		_, _, _ = pSetTimer.Call(w.hwnd, timerAnim, 60, 0)
		w.animOn = true
	} else if v.Bar != BarIndeterminate && w.animOn {
		_, _, _ = pKillTimer.Call(w.hwnd, timerAnim)
		w.animOn = false
	}
	if changed && (v.State == StateDone || v.State == StateFailed) {
		fi := flashInfo{Hwnd: w.hwnd, Flags: flashTray | flashTimerNoFG, Count: 3}
		fi.Size = uint32(unsafe.Sizeof(fi))
		_, _, _ = pFlashWindowEx.Call(ptr(&fi))
	}
	invalidate(w.hwnd)
}

// poll reads the service every second, across the restart: no answer is "restarting", not an error.
func (w *win) poll(ctx context.Context) {
	if w.svc == nil {
		return
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			st, err := w.svc.Status(sctx)
			cancel()
			w.mu.Lock()
			w.model.Observe(SnapshotOf(st, err))
			w.mu.Unlock()
			_, _, _ = pPostMessage.Call(w.hwnd, wmRefresh, 0, 0)
		}
	}
}

// activate does what a button says.
func (w *win) activate(id ButtonID) {
	w.mu.Lock()
	if _, busy := w.ui.busy[id]; busy {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	switch id {
	case BtnNotNow, BtnHide, BtnClose:
		_, _, _ = pDestroyWindow.Call(w.hwnd) // the update, if any, goes on in the service
	case BtnUpdate:
		w.mu.Lock()
		w.model.Requested()
		w.mu.Unlock()
		w.afterChange()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), installMax)
			defer cancel()
			_, err := w.svc.Install(ctx)
			w.mu.Lock()
			w.model.RequestReturned(err)
			w.mu.Unlock()
			_, _, _ = pPostMessage.Call(w.hwnd, wmRefresh, 0, 0)
		}()
	case BtnCancel:
		w.mu.Lock()
		w.ui.busy = map[ButtonID]string{BtnCancel: "Cancelling..."}
		w.mu.Unlock()
		invalidate(w.hwnd)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := w.svc.Cancel(ctx)
			w.mu.Lock()
			w.ui.busy = nil
			if err == nil {
				w.model.Cancelled()
			} // else it is past the point of no return: the next reading shows the real step
			w.mu.Unlock()
			_, _, _ = pPostMessage.Call(w.hwnd, wmRefresh, 0, 0)
		}()
	}
}

// buttonAt is the index of the button under (x, y), or -1.
func (w *win) buttonAt(x, y int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, it := range w.layout.Items {
		if it.Kind != KindButton {
			continue
		}
		if x >= it.Rect.Min.X && x < it.Rect.Max.X && y >= it.Rect.Min.Y && y < it.Rect.Max.Y {
			return n
		}
		n++
	}
	return -1
}

func (w *win) buttonID(i int) (ButtonID, bool) {
	if i < 0 || i >= len(w.view.Buttons) {
		return "", false
	}
	return w.view.Buttons[i].ID, true
}

func lo(l uintptr) int { return int(int16(l & 0xffff)) }
func hi(l uintptr) int { return int(int16((l >> 16) & 0xffff)) }

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	w := theWin
	if w == nil || w.hwnd == 0 {
		r, _, _ := pDefWindowProc.Call(hwnd, m, wp, lp)
		return r
	}
	switch m {
	case wmPaint:
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, ptr(&ps))
		w.paint(dc)
		_, _, _ = pEndPaint.Call(hwnd, ptr(&ps))
		return 0
	case wmEraseBkgnd:
		return 1
	case wmRefresh:
		w.afterChange()
		return 0
	case wmTimer:
		if wp == timerAnim {
			w.mu.Lock()
			w.ui.tick += max(2, w.fonts.unit/4)
			w.mu.Unlock()
			invalidate(hwnd)
		}
		return 0
	case wmDPIChanged:
		w.dpi = int(wp & 0xffff)
		w.fonts.free()
		w.fonts = newFonts(w.dpi)
		r := (*rect)(*(*unsafe.Pointer)(unsafe.Pointer(&lp))) // lParam of WM_DPICHANGED points to the suggested rectangle
		w.relayout()
		ow, oh := w.outerSize()
		_, _, _ = pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(ow), uintptr(oh), swpNoZOrder|swpNoActivate)
		invalidate(hwnd)
		return 0
	case wmMouseMove:
		i := w.buttonAt(lo(lp), hi(lp))
		w.mu.Lock()
		changed := w.ui.hot != i
		w.ui.hot = i
		w.mu.Unlock()
		tme := trackMouse{Flags: tmeLeave, Hwnd: hwnd}
		tme.Size = uint32(unsafe.Sizeof(tme))
		_, _, _ = pTrackMouseEvent.Call(ptr(&tme))
		if changed {
			invalidate(hwnd)
		}
		return 0
	case wmMouseLeave:
		w.mu.Lock()
		w.ui.hot = -1
		w.mu.Unlock()
		invalidate(hwnd)
		return 0
	case wmLButtonDown:
		i := w.buttonAt(lo(lp), hi(lp))
		if i >= 0 {
			w.mu.Lock()
			w.ui.down, w.ui.focus = i, i
			w.mu.Unlock()
			_, _, _ = pSetCapture.Call(hwnd)
			invalidate(hwnd)
		}
		return 0
	case wmLButtonUp:
		w.mu.Lock()
		was := w.ui.down
		w.ui.down = -1
		w.mu.Unlock()
		_, _, _ = pReleaseCapture.Call()
		invalidate(hwnd)
		if was >= 0 && w.buttonAt(lo(lp), hi(lp)) == was {
			w.mu.Lock()
			id, ok := w.buttonID(was)
			w.mu.Unlock()
			if ok {
				w.activate(id)
			}
		}
		return 0
	case wmKeyDown:
		w.key(wp)
		return 0
	case wmClose:
		// The close button of the title bar is the Escape key: Not now, Hide or Close. It never
		// stops an update.
		w.mu.Lock()
		id := w.view.EscapeIs
		w.mu.Unlock()
		w.activate(id)
		return 0
	case wmDestroy:
		_, _, _ = pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, m, wp, lp)
	return r
}

// key handles the keyboard: Tab goes left to right on the button bar, Enter presses the
// default button, Escape the Not now / Hide / Close button, Space the focused one.
func (w *win) key(vk uintptr) {
	w.mu.Lock()
	v := w.view
	n := len(v.Buttons)
	switch vk {
	case vkTab:
		if n > 0 {
			shift, _, _ := pGetKeyState.Call(vkShift)
			if int16(shift) < 0 {
				w.ui.focus = (w.ui.focus - 1 + n) % n
			} else {
				w.ui.focus = (w.ui.focus + 1) % n
			}
		}
		w.mu.Unlock()
		invalidate(w.hwnd)
	case vkReturn:
		w.mu.Unlock()
		w.activate(v.Default)
	case vkEscape:
		w.mu.Unlock()
		w.activate(v.EscapeIs)
	case vkSpace:
		id, ok := w.buttonID(w.ui.focus)
		w.mu.Unlock()
		if ok {
			w.activate(id)
		}
	default:
		w.mu.Unlock()
	}
}

// paint draws into a back buffer and copies it, so nothing flickers.
func (w *win) paint(dc uintptr) {
	w.mu.Lock()
	l, v, ui := w.layout, w.view, w.ui
	busy := map[ButtonID]string{}
	for k, s := range w.ui.busy {
		busy[k] = s
	}
	ui.busy = busy
	w.mu.Unlock()
	mem, _, _ := pCreateCompatibleDC.Call(dc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(dc, uintptr(l.W), uintptr(l.H))
	old, _, _ := pSelectObject.Call(mem, bmp)
	f := &fonts{regular: w.fonts.regular, bold: w.fonts.bold, unit: w.fonts.unit, dc: mem}
	drawLayout(mem, f, Phosphor, l, v, ui)
	_, _, _ = pBitBlt.Call(dc, 0, 0, uintptr(l.W), uintptr(l.H), mem, 0, 0, srccopy)
	_, _, _ = pSelectObject.Call(mem, old)
	_, _, _ = pDeleteObject.Call(bmp)
	_, _, _ = pDeleteDC.Call(mem)
}
