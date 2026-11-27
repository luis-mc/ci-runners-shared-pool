// Package memcap puts a hard memory cap on one job at its runner class ceiling
// (scheduling R5.95). CPU is counted by the scheduler; memory is enforced. A job
// over its cap is stopped and marked "over memory" (R5.96). One Enforcer
// interface sits over three mechanisms: a cgroup memory limit on Linux (inside
// the pool container), a job object memory limit on Windows, and a process-tree
// watcher on macOS. Each has a fake for tests.
package memcap

import (
	"errors"
	"fmt"
)

// Job names the process tree a cap applies to: the job's id and the pid of the
// root process of its tree.
type Job struct {
	ID  string
	PID int
}

// Guard is the cap of one running job.
type Guard interface {
	// Exceeded is true once the job's tree went over the cap and was stopped.
	Exceeded() bool
	// Release removes the cap when the job ended.
	Release() error
}

// Enforcer starts a cap for a job.
type Enforcer interface {
	// Name says which mechanism it is: "cgroup", "jobobject" or "watcher".
	Name() string
	// Start caps the job's tree at limitBytes. A limit of 0 or less is an error:
	// a job never runs without its cap.
	Start(job Job, limitBytes int64) (Guard, error)
}

// ErrNoLimit is returned for a cap of 0 or less.
var ErrNoLimit = errors.New("memory cap must be above 0")

// GiB is one gibibyte in bytes.
const GiB int64 = 1 << 30

// LimitBytes is the cap of a class ceiling given in GiB.
func LimitBytes(memoryGiB int) int64 { return int64(memoryGiB) * GiB }

// OverMemoryText is the wait or end reason of a job stopped over its cap. It
// names the class to raise: the one the job ran under and the next one up
// ("" when the runner is already xl) (scheduling R5.96).
func OverMemoryText(class string, limitGiB int, next string) string {
	if next == "" {
		return fmt.Sprintf("over memory: the job went over the %s class limit of %d GiB. This is the largest class; raise its memory in the class settings.", class, limitGiB)
	}
	return fmt.Sprintf("over memory: the job went over the %s class limit of %d GiB. Raise the class of this runner to %s.", class, limitGiB, next)
}

// Fake is an Enforcer for tests. Trip(id) marks a job as over its cap.
type Fake struct {
	Guards map[string]*FakeGuard
	Err    error
}

// FakeGuard is the Guard of Fake.
type FakeGuard struct {
	Job      Job
	Limit    int64
	Over     bool
	Released bool
}

// Exceeded implements Guard.
func (g *FakeGuard) Exceeded() bool { return g.Over }

// Release implements Guard.
func (g *FakeGuard) Release() error { g.Released = true; return nil }

// Name implements Enforcer.
func (f *Fake) Name() string { return "fake" }

// Start implements Enforcer.
func (f *Fake) Start(j Job, limit int64) (Guard, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if limit <= 0 {
		return nil, ErrNoLimit
	}
	if f.Guards == nil {
		f.Guards = map[string]*FakeGuard{}
	}
	g := &FakeGuard{Job: j, Limit: limit}
	f.Guards[j.ID] = g
	return g, nil
}

// Trip marks the job as over its cap.
func (f *Fake) Trip(id string) {
	if g := f.Guards[id]; g != nil {
		g.Over = true
	}
}
