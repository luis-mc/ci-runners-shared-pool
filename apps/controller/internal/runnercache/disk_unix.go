//go:build !windows

package runnercache

import "syscall"

// OSDisk reads the free space of the volume that holds path.
func OSDisk(path string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := int64(st.Bsize)                                                       //nolint:gosec // block size is small and positive
	return Disk{Total: int64(st.Blocks) * bs, Free: int64(st.Bavail) * bs}, nil //nolint:gosec // disk sizes fit int64
}
