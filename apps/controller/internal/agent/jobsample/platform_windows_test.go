//go:build windows

package jobsample

import "testing"

// OPS66 R5.62: Windows has the Job Object reader; a job that was never attached is an error, not a crash.
func TestPlatformWindows(t *testing.T) {
	p := NewPlatform()
	if p.JobObject == nil || p.Jobs == nil || p.ProcessTree != nil {
		t.Fatalf("%+v", p)
	}
	q := &OSJobQuerier{}
	if _, err := q.Query("never-attached"); err == nil {
		t.Fatal("query of an unknown job must fail")
	}
	if err := q.Attach("crsp-test-job", 0x7fffff00); err == nil {
		t.Fatal("attach of a process that does not exist must fail")
	}
	q.Close("never-attached")
}
