//go:build windows

package tray

import "golang.org/x/sys/windows"

// Lock is the single-instance lock: a mutex in the Local\ namespace (R5.23).
type Lock struct{ h windows.Handle }

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil && l.h != 0 {
		_ = windows.CloseHandle(l.h)
	}
}

// AcquireLock creates the named mutex. ok=false means another tray in this session holds it.
func AcquireLock() (*Lock, bool, error) {
	name, err := windows.UTF16PtrFromString(`Local\crsp-tray`)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(h)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &Lock{h: h}, true, nil
}
