package scheduler_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

type factLog struct {
	mu   sync.Mutex
	jobs []scheduler.JobRef
	fs   []jobsdata.Facts
}

func (l *factLog) add(j scheduler.JobRef, f jobsdata.Facts) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobs, l.fs = append(l.jobs, j), append(l.fs, f)
}

func (l *factLog) last() jobsdata.Facts {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fs) == 0 {
		return jobsdata.Facts{}
	}
	return l.fs[len(l.fs)-1]
}

func TestFactsWaitThenSlotThenLost(t *testing.T) {
	e := newEnv(t)
	log := &factLog{}
	e.eng.SetFacts(log.add)
	e.fill(2) // the default machine has two job slots, both busy
	j := job("o/r", 1, "x")
	d := e.gate(e.req("w", j, 1, quiet))
	if len(d.Go) != 0 {
		t.Fatalf("want a wait: %+v", d)
	}
	before := len(log.fs)
	f := log.last()
	if f.WaitReason == "" || f.WaitText == "" || f.MachineID != m1 || log.jobs[before-1].Job != "x" {
		t.Fatalf("wait fact %+v", f)
	}
	// A beat with the same answer says nothing new.
	if _, err := e.eng.Beat(context.Background(), m1, "w", quiet); err != nil {
		t.Fatal(err)
	}
	if len(log.fs) != before {
		t.Fatalf("a repeated wait was reported again: %d", len(log.fs)-before)
	}
	// Free a slot: the job goes, with its queue and slot times.
	if _, err := e.eng.Release(context.Background(), m1, job("o/r", 900, "build"), nil); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Second)
	d = e.gate(e.req("w", j, 1, quiet))
	g := goes(t, d)
	f = log.last()
	if f.SlotAt.IsZero() || !f.SlotAt.Equal(g.TakenAt) || f.QueuedAt.IsZero() || f.QueuedAt.After(f.SlotAt) || f.Light == nil || *f.Light {
		t.Fatalf("go fact %+v", f)
	}
	// Its lease runs out: lost.
	e.advance(leaseLimit + time.Second)
	if _, err := e.eng.Sweep(context.Background(), e.now); err != nil {
		t.Fatal(err)
	}
	if f = log.last(); !f.Lost || f.MachineID != m1 {
		t.Fatalf("lost fact %+v", f)
	}
}

func TestFactsNormalReleaseSaysNothingAndOffIsOff(t *testing.T) {
	e := newEnv(t)
	goes(t, e.gate(e.req("a", job("o/r", 1, "x"), 1, quiet))) // no hook set: nothing breaks
	log := &factLog{}
	e.eng.SetFacts(log.add)
	if _, err := e.eng.Release(context.Background(), m1, job("o/r", 1, "x"), nil); err != nil {
		t.Fatal(err)
	}
	if len(log.fs) != 0 {
		t.Fatalf("a normal end reported %+v", log.fs)
	}
}
