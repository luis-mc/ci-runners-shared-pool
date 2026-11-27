//go:build windows

package tray

import (
	"syscall"
	"unsafe"
)

var (
	pGetGUIThreadInfo = syscall.NewLazyDLL("user32.dll").NewProc("GetGUIThreadInfo")
)

// guiThreadInfo is GUITHREADINFO; the flags tell whether a menu loop runs on the thread.
type guiThreadInfo struct {
	Size                                               uint32
	Flags                                              uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	Rect                                               [4]int32
}

const (
	guiInMenuMode    = 0x4
	guiPopupMenuMode = 0x10
)

// MenuOpen says whether the user has a menu open in the foreground thread. TrackPopupMenu
// closes when the tray icon or its menu changes under it, so the tray waits (R5.45).
func MenuOpen() bool {
	var gi guiThreadInfo
	gi.Size = uint32(unsafe.Sizeof(gi))
	r, _, _ := pGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&gi)))
	return r != 0 && gi.Flags&(guiInMenuMode|guiPopupMenuMode) != 0
}
