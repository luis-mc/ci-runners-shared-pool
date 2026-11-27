//go:build windows

package hooks

import (
	"syscall"
	"unsafe"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// processInfo looks a process up in a toolhelp snapshot (R5.31).
func processInfo(pid int64) (slots.ProcessInfo, bool) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return slots.ProcessInfo{}, false
	}
	defer func() { _ = syscall.CloseHandle(snap) }()
	var pe syscall.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := syscall.Process32First(snap, &pe); err != nil {
		return slots.ProcessInfo{}, false
	}
	for {
		if int64(pe.ProcessID) == pid {
			return slots.ProcessInfo{Parent: int64(pe.ParentProcessID), Name: syscall.UTF16ToString(pe.ExeFile[:])}, true
		}
		if err := syscall.Process32Next(snap, &pe); err != nil {
			return slots.ProcessInfo{}, false
		}
	}
}
