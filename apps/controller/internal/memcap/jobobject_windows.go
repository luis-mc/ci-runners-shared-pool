//go:build windows

package memcap

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// JobObjectEnforcer caps a job with a Windows job object memory limit: the
// system refuses allocations of the tree beyond JobMemoryLimit and the
// peak shows it.
type JobObjectEnforcer struct{}

var _ Enforcer = JobObjectEnforcer{}

// Name implements Enforcer.
func (JobObjectEnforcer) Name() string { return "jobobject" }

// Start implements Enforcer.
func (JobObjectEnforcer) Start(j Job, limit int64) (Guard, error) {
	if limit <= 0 {
		return nil, ErrNoLimit
	}
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("memcap: create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	info.JobMemoryLimit = uintptr(limit)
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("memcap: set job memory limit: %w", err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(j.PID)) //nolint:gosec // a pid is small
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("memcap: open process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(p) }()
	if err := windows.AssignProcessToJobObject(h, p); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("memcap: assign process: %w", err)
	}
	return &jobGuard{h: h, limit: limit}, nil
}

type jobGuard struct {
	h     windows.Handle
	limit int64
}

// Exceeded is true when the job's peak memory reached the cap.
func (g *jobGuard) Exceeded() bool {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(g.h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return false
	}
	return int64(info.PeakJobMemoryUsed) >= g.limit
}

// Release closes the job; KILL_ON_JOB_CLOSE ends anything left in it.
func (g *jobGuard) Release() error { return windows.CloseHandle(g.h) }
