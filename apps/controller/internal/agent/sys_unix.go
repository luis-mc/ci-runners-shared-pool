//go:build !windows

package agent

import "syscall"

// Disk implements Sys.
func (OSSys) Disk(path string) (int64, int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)                                    //nolint:gosec // block size is small and positive
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, nil //nolint:gosec // disk sizes fit int64
}
