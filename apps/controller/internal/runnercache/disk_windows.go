//go:build windows

package runnercache

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// OSDisk reads the free space of the volume that holds path.
func OSDisk(path string) (Disk, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Disk{}, err
	}
	var free, total, totalFree uint64
	r, _, e := procGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return Disk{}, e
	}
	return Disk{Total: int64(total), Free: int64(free)}, nil //nolint:gosec // disk sizes fit int64
}
