//go:build windows

package health

import (
	"syscall"
	"time"
)

var procTick = syscall.NewLazyDLL("kernel32.dll").NewProc("GetTickCount64")

// tickCount is GetTickCount64: milliseconds since boot.
func tickCount() (time.Duration, error) {
	if err := procTick.Find(); err != nil {
		return 0, err
	}
	r, _, _ := procTick.Call()
	return time.Duration(r) * time.Millisecond, nil //nolint:gosec // milliseconds since boot
}
