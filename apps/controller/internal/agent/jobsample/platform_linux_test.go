//go:build linux

package jobsample

import "testing"

// OPS66 R5.62: Linux has the cgroup reader and the /proc tree reader, and no Job Object.
func TestPlatformLinux(t *testing.T) {
	p := NewPlatform()
	if p.Cgroup == nil || p.ProcessTree == nil || p.JobObject != nil || p.Jobs != nil {
		t.Fatalf("%+v", p)
	}
	procs, err := ProcFS{}.List()
	if err != nil || len(procs) == 0 {
		t.Fatalf("/proc: %v %d", err, len(procs))
	}
}
