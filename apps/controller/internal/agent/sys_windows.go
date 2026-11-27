//go:build windows

package agent

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// Disk implements Sys with GetDiskFreeSpaceExW.
func (OSSys) Disk(path string) (int64, int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var free, total, totalFree uint64
	r, _, e := procGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, 0, e
	}
	return int64(total), int64(free), nil //nolint:gosec // disk sizes fit int64
}
