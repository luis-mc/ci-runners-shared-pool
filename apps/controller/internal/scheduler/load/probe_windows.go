//go:build windows

package load

import (
	"runtime"
	"syscall"
	"unsafe"
)

type osProbe struct{}

// NewProbe returns the probe of this operating system.
func NewProbe() Probe { return osProbe{} }

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

// Memory reads GlobalMemoryStatusEx (R5.40).
func (osProbe) Memory() (float64, bool) {
	m := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	r, _, _ := procGlobalMemory.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, false
	}
	return MemoryPct(float64(m.TotalPhys), float64(m.AvailPhys))
}

type fileTime struct{ Low, High uint32 }

func (f fileTime) u64() uint64 { return uint64(f.High)<<32 | uint64(f.Low) }

// CPURaw reads GetSystemTimes; kernel time includes idle (R5.41).
func (osProbe) CPURaw() (Raw, bool) {
	var idle, kernel, user fileTime
	r, _, _ := procSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return Raw{}, false
	}
	return WindowsRaw(idle.u64(), kernel.u64(), user.u64())
}

// LoadAvgPerCore is not available on Windows.
func (osProbe) LoadAvgPerCore() (float64, bool) { _ = runtime.NumCPU(); return 0, false }
