//go:build windows

package runnerprocs

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// End ends one process at once.
func End(pid int64) error {
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	return p.Kill()
}

// Self is the SID of the account this process runs as, "" when unknown.
func Self() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
}

func ownerSID(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return ""
	}
	defer func() { _ = tok.Close() }()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
}

func List() ([]Proc, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = syscall.CloseHandle(snap) }()
	var pe syscall.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := syscall.Process32First(snap, &pe); err != nil {
		return nil, err
	}
	var res []Proc
	for {
		res = append(res, Proc{PID: int64(pe.ProcessID), PPID: int64(pe.ParentProcessID),
			Name: syscall.UTF16ToString(pe.ExeFile[:]), Owner: ownerSID(pe.ProcessID)})
		if err := syscall.Process32Next(snap, &pe); err != nil {
			return res, nil
		}
	}
}
