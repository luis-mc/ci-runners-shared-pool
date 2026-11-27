//go:build windows

package cpucap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// The cpu rate control flags of JOBOBJECT_CPU_RATE_CONTROL_INFORMATION.
const (
	jobCPURateControlEnable  = 0x1
	jobCPURateControlHardCap = 0x4
)

// cpuRateControl is JOBOBJECT_CPU_RATE_CONTROL_INFORMATION: the flags and a
// CpuRate in hundredths of a percent of all cores (1 to 10000).
type cpuRateControl struct {
	ControlFlags uint32
	CPURate      uint32
}

// Job is a Job Object that holds runner process trees and caps their CPU.
type Job struct {
	h windows.Handle
	// assigned are the processes already in the job: assigning one again fails.
	assigned map[uint32]bool
}

// NewJob creates an unnamed Job Object whose CPU use is capped at pct percent of
// all cores, as a hard cap.
func NewJob(pct int) (*Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("creating the job object: %w", err)
	}
	j := &Job{h: h, assigned: map[uint32]bool{}}
	if err := j.SetCap(pct); err != nil {
		_ = j.Close()
		return nil, err
	}
	return j, nil
}

// SetCap sets the hard CPU cap of the job. 100 is a cap of all cores (rate
// 10000): Windows rejects an empty rate control (flags 0, rate 0) with
// "The parameter is incorrect", so the cap is never cleared that way.
func (j *Job) SetCap(pct int) error {
	pct = min(max(pct, 1), 100)
	info := cpuRateControl{ControlFlags: jobCPURateControlEnable | jobCPURateControlHardCap, CPURate: uint32(pct * 100)} //nolint:gosec // 1 to 100
	if _, err := windows.SetInformationJobObject(j.h, windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { //nolint:gosec // fixed size
		return fmt.Errorf("setting the CPU rate: %w", err)
	}
	return nil
}

// Rate reads the cap back: the percent of all cores, 100 when there is none.
func (j *Job) Rate() (int, error) {
	var info cpuRateControl
	var n uint32
	if err := windows.QueryInformationJobObject(j.h, windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &n); err != nil { //nolint:gosec // fixed size
		return 0, err
	}
	if info.ControlFlags&jobCPURateControlEnable == 0 {
		return 100, nil
	}
	return int(info.CPURate / 100), nil
}

// AssignTree puts the process with pid and every child it has now in the job.
// A child it starts later joins the job by itself. A process already in the job
// is not an error.
func (j *Job) AssignTree(pid uint32) (int, error) {
	pids, err := processTree(pid)
	if err != nil {
		return 0, err
	}
	n := 0
	var first error
	for _, p := range pids {
		if j.assigned[p] {
			n++
			continue
		}
		ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, p)
		if err != nil {
			if first == nil {
				first = fmt.Errorf("opening process %d: %w", p, err)
			}
			continue
		}
		err = windows.AssignProcessToJobObject(j.h, ph)
		_ = windows.CloseHandle(ph)
		if err != nil {
			if first == nil {
				first = fmt.Errorf("assigning process %d: %w", p, err)
			}
			continue
		}
		j.assigned[p] = true
		n++
	}
	return n, first
}

// Close closes the job handle. Processes in it keep running and keep the cap
// only while the handle is open, so the applier holds it for the agent's life.
func (j *Job) Close() error { return windows.CloseHandle(j.h) }

// processTree returns pid and all its descendants from a process snapshot.
func processTree(root uint32) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	children := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e)) //nolint:gosec // fixed size
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, err
	}
	for {
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	out := []uint32{root}
	for i := 0; i < len(out); i++ {
		out = append(out, children[out[i]]...)
	}
	return out, nil
}

type osApplier struct {
	mu  sync.Mutex
	job *Job
}

func newOS() Applier { return &osApplier{} }

// Apply implements Applier: it finds the process of every runner service, puts
// its tree in one Job Object and sets the CPU rate on it (R5.64).
func (a *osApplier) Apply(_ context.Context, pct int) Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job == nil {
		j, err := NewJob(pct)
		if err != nil {
			return Result{State: Failed, Detail: err.Error()}
		}
		a.job = j
	} else if err := a.job.SetCap(pct); err != nil {
		return Result{State: Failed, Detail: err.Error()}
	}
	m, err := mgr.Connect()
	if err != nil {
		return Result{State: Failed, Detail: "the service manager could not be opened: " + err.Error()}
	}
	defer func() { _ = m.Disconnect() }()
	names, err := m.ListServices()
	if err != nil {
		return Result{State: Failed, Detail: "the services could not be listed: " + err.Error()}
	}
	targets := 0
	var first error
	for _, n := range names {
		if !strings.HasPrefix(n, ServicePrefix) {
			continue
		}
		s, err := m.OpenService(n)
		if err != nil {
			continue
		}
		st, err := s.Query()
		_ = s.Close()
		if err != nil || st.State != svc.Running || st.ProcessId == 0 {
			continue
		}
		c, err := a.job.AssignTree(st.ProcessId)
		targets += c
		if err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return Result{State: Failed, Targets: targets, Detail: first.Error()}
	}
	if targets == 0 {
		return Result{State: Applied, Detail: "no runner service is running; the cap is set when one starts"}
	}
	return Result{State: Applied, Targets: targets}
}
