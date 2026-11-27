//go:build windows

package jobsample

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// basicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION; with ioInfo it is the BasicAndIo class.
type basicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

type basicAndIo struct {
	Basic  basicAccounting
	IoInfo windows.IO_COUNTERS
}

// OSJobQuerier creates Job Objects and reads them. It is the Windows reader of OPS66.
type OSJobQuerier struct {
	mu   sync.Mutex
	jobs map[string]windows.Handle
}

var _ JobQuerier = (*OSJobQuerier)(nil)

// Attach creates the Job Object name and puts the process pid into it. A process started later by pid is in
// the job too, so the job holds the runner's process tree from now on. On a system that cannot nest jobs, and
// a process that is already in a job, it returns the error and the job is read as unknown.
func (q *OSJobQuerier) Attach(name string, pid int) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	h, err := windows.CreateJobObject(nil, n)
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // a pid is small
	if err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("open process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(p) }()
	if err := windows.AssignProcessToJobObject(h, p); err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("assign process to job object: %w", err)
	}
	q.mu.Lock()
	if q.jobs == nil {
		q.jobs = map[string]windows.Handle{}
	}
	q.jobs[name] = h
	q.mu.Unlock()
	return nil
}

// Query implements JobQuerier.
func (q *OSJobQuerier) Query(name string) (JobCounters, error) {
	q.mu.Lock()
	h, ok := q.jobs[name]
	q.mu.Unlock()
	if !ok {
		return JobCounters{}, fmt.Errorf("job object %q not attached", name)
	}
	var acc basicAndIo
	if err := windows.QueryInformationJobObject(h, windows.JobObjectBasicAndIoAccountingInformation,
		uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil); err != nil {
		return JobCounters{}, fmt.Errorf("query job accounting: %w", err)
	}
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil); err != nil {
		return JobCounters{}, fmt.Errorf("query job limits: %w", err)
	}
	return JobCounters{
		UserTime100ns: acc.Basic.TotalUserTime, KernelTime100ns: acc.Basic.TotalKernelTime,
		PeakMemory: int64(ext.PeakJobMemoryUsed), Terminated: int64(acc.Basic.TotalTerminatedProcesses),
	}, nil
}

// Close closes the Job Object of a job that ended. The processes in it are not touched.
func (q *OSJobQuerier) Close(name string) {
	q.mu.Lock()
	h, ok := q.jobs[name]
	delete(q.jobs, name)
	q.mu.Unlock()
	if ok {
		_ = windows.CloseHandle(h)
	}
}
