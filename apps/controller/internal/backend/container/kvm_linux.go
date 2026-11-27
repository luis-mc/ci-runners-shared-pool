//go:build linux

package container

import (
	"os"
	"syscall"
)

// ProbeKVM reads /dev/kvm on this machine: whether it is there, whether it opens
// for read and write, and the group that owns it (runners R5.41).
func ProbeKVM() KVMFacts {
	fi, err := os.Stat(KVMDevice)
	if err != nil {
		return KVMFacts{}
	}
	f := KVMFacts{Exists: true}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		f.GID = int(st.Gid)
	}
	if h, err := os.OpenFile(KVMDevice, os.O_RDWR, 0); err == nil {
		_ = h.Close()
		f.Usable = true
	}
	return f
}
