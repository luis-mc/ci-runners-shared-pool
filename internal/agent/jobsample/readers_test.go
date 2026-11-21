package jobsample

import (
	"context"
	"errors"
	"testing"
	"time"
)

// OPS66 R5.62 Linux: memory.peak and the oom_kill count are read; an OOM only counts above the count of the
// first sample; a kernel without memory.peak leaves the peak 0.
func TestCgroupPeakAndOOM(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"/cg/cpu.stat": "usage_usec 0\n", "/cg/memory.current": "100", "/cg/memory.peak": "500",
		"/cg/memory.events": "low 0\noom 0\noom_kill 2\n",
	}
	cs := NewCgroupSampler(fakeFS(files))
	tg := Target{RunnerID: "r", CgroupPath: "/cg", JobID: 1}
	s, _, _ := cs.Sample(ctx, tg, t0)
	if s.MemPeakBytes != 500 || s.OOMKilled {
		t.Fatalf("first sample %+v: the count of the first sample is the base", s)
	}
	files["/cg/memory.events"] = "oom_kill 3\n"
	if s, _, _ = cs.Sample(ctx, tg, t0.Add(time.Second)); !s.OOMKilled {
		t.Fatalf("a new oom_kill is an OOM: %+v", s)
	}
	cs.Forget(tg)
	delete(files, "/cg/memory.peak")
	delete(files, "/cg/memory.events")
	if s, _, _ = cs.Sample(ctx, tg, t0); !s.Known || s.MemPeakBytes != 0 || s.OOMKilled {
		t.Fatalf("old kernel: %+v", s)
	}
}

type fakeJobs struct {
	c   JobCounters
	err error
}

func (f *fakeJobs) Query(string) (JobCounters, error) { return f.c, f.err }

// OPS66 R5.62 Windows: CPU is the user plus kernel time (100 ns units) over the wall time, memory is the
// job's peak, a process ended for a limit is an OOM.
func TestJobObjectSampler(t *testing.T) {
	ctx := context.Background()
	f := &fakeJobs{c: JobCounters{UserTime100ns: 0, KernelTime100ns: 0, PeakMemory: 1000}}
	js := NewJobObjectSampler(f)
	tg := Target{RunnerID: "r", JobObject: "crsp-job-1"}
	if s, _, _ := js.Sample(ctx, tg, t0); !s.Known || s.CPUMillis != 0 || s.MemoryBytes != 1000 || s.MemPeakBytes != 1000 {
		t.Fatalf("first %+v", s)
	}
	// 15 s user + 5 s kernel over 10 s wall = 2 cores.
	f.c = JobCounters{UserTime100ns: 150_000_000, KernelTime100ns: 50_000_000, PeakMemory: 4000, Terminated: 1}
	s, _, _ := js.Sample(ctx, tg, t0.Add(10*time.Second))
	if s.CPUMillis != 2000 || s.MemoryBytes != 4000 || !s.OOMKilled {
		t.Fatalf("second %+v", s)
	}
	f.err = errors.New("gone")
	if s, r, _ := js.Sample(ctx, tg, t0); s.Known || r == "" {
		t.Fatal("a failed query is unknown with a reason")
	}
	if s, r, _ := js.Sample(ctx, Target{}, t0); s.Known || r == "" {
		t.Fatal("no job object is unknown with a reason")
	}
	js.Forget(tg)
	if js.Interval() != Interval || js.Source() != JobObject {
		t.Fatal("interval and source")
	}
}

// OPS66 R5.62 macOS: the ps table gives CPU in centiseconds and memory in KiB, and the tree sampler sums it.
func TestPSTableAndTree(t *testing.T) {
	out := []byte("  1     0   2048   0:01.50\n 10     1   1024  1-02:03:04.25\n bad line\n 11    10    512   0:00.00\n 99     1    64   0:10.00\n")
	procs, err := PSTable{Run: func() ([]byte, error) { return out, nil }}.List()
	if err != nil || len(procs) != 4 {
		t.Fatalf("procs %v %v", procs, err)
	}
	if procs[1].CPUTicks != (86400+2*3600+3*60+4)*100+25 || procs[1].RSSBytes != 1024*1024 || procs[0].CPUTicks != 150 {
		t.Fatalf("%+v", procs)
	}
	if _, err := (PSTable{Run: func() ([]byte, error) { return nil, errors.New("no ps") }}).List(); err == nil {
		t.Fatal("ps failure is an error")
	}
	tree := NewProcessTreeSampler(PSTable{Run: func() ([]byte, error) { return out, nil }})
	s, _, _ := tree.Sample(context.Background(), Target{RunnerID: "r", RootPID: 10}, t0)
	if !s.Known || s.MemoryBytes != (1024+512)*1024 { // the process 10 and its child 11, not 99
		t.Fatalf("%+v", s)
	}
	if tree.Interval() != TreeInterval {
		t.Fatal("tree interval")
	}
}

func TestParsePSTimeRejects(t *testing.T) {
	for _, s := range []string{"x", "1:x", "a-1:00", "1:2:3:4", "1:00.zz"} {
		if _, ok := parsePSTime(s); ok {
			t.Fatalf("%q parsed", s)
		}
	}
}

type noteSender struct {
	recSender
	got []Outcome
}

func (n *noteSender) NoteOutcome(_ Target, o Outcome) { n.got = append(n.got, o) }

// OPS66 R5.61: the collector keeps the faster reader at its own period, and tells the sender the outcome.
func TestCollectorIntervalAndOutcome(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/cg/cpu.stat": "usage_usec 0\n", "/cg/memory.current": "1", "/cg/memory.peak": "77", "/cg/memory.events": "oom_kill 0\n"}
	snd := &noteSender{}
	c := &Collector{Cgroup: NewCgroupSampler(fakeFS(files)), Sender: snd}
	tg := Target{RunnerID: "r", CgroupPath: "/cg"}
	c.Start(tg, t0)
	for i := 0; i < 3; i++ { // ticks at 0, 0.5 s and 1 s: the half-second tick is skipped
		c.Tick(ctx, t0.Add(time.Duration(i)*500*time.Millisecond))
	}
	files["/cg/memory.events"] = "oom_kill 1\n"
	c.Finish(ctx, tg, t0.Add(1500*time.Millisecond), true)
	if len(snd.calls) != 1 || snd.calls[0].n != 3 || !snd.calls[0].final { // 0 s, 1 s and the last sample
		t.Fatalf("calls %+v", snd.calls)
	}
	want := Outcome{OOMKilled: true, CutShort: true, MemoryPeakBytes: 77}
	if len(snd.got) != 1 || snd.got[0] != want {
		t.Fatalf("outcome %+v", snd.got)
	}
}

// OPS66 R5.62: the collector picks the Job Object reader for a job that has a job object.
func TestCollectorPicksJobObject(t *testing.T) {
	snd := &recSender{}
	c := &Collector{JobObject: NewJobObjectSampler(&fakeJobs{c: JobCounters{PeakMemory: 5}}), Sender: snd}
	tg := Target{RunnerID: "r", JobObject: "j"}
	c.Start(tg, t0)
	c.Finish(context.Background(), tg, t0.Add(time.Second), false)
	if len(snd.calls) != 1 || snd.calls[0].n != 1 {
		t.Fatalf("%+v", snd.calls)
	}
}
