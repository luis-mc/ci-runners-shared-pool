//go:build windows

package load

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemory = kernel32.NewProc("GlobalMemoryStatusEx")
	procSystemTimes  = kernel32.NewProc("GetSystemTimes")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// WinMemory implements Probe with GlobalMemoryStatusEx (R5.15).
func (OSProbe) WinMemory() (float64, int64, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemory.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, 0, errors.New("GlobalMemoryStatusEx failed")
	}
	return float64(m.MemoryLoad), int64(m.TotalPhys), nil //nolint:gosec // physical memory fits int64
}

// WinTimes implements Probe with GetSystemTimes (R5.15). Kernel time includes idle time.
func (OSProbe) WinTimes() (Counters, error) {
	var idle, kernel, user syscall.Filetime
	if r, _, _ := procSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r == 0 {
		return Counters{}, errors.New("GetSystemTimes failed")
	}
	v := func(f syscall.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	total := v(kernel) + v(user)
	return Counters{Busy: total - v(idle), Total: total}, nil
}
