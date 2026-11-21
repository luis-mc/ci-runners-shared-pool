package jobsample

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func fakeFS(files map[string]string) FileReader {
	return func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("missing")
	}
}

type fakeTable struct {
	procs []Proc
	err   error
}

func (f *fakeTable) List() ([]Proc, error) { return f.procs, f.err }

func TestCgroupAndProcessTree(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/cg/cpu.stat": "usage_usec 1000000\nuser_usec 5\n", "/cg/memory.current": "2048\n"}
	cs := NewCgroupSampler(fakeFS(files))
	tg := Target{RunnerID: "r1", JobID: 7, CgroupPath: "/cg"}
	s, reason, err := cs.Sample(ctx, tg, t0)
	if err != nil || reason != "" || !s.Known || s.MemoryBytes != 2048 || s.CPUMillis != 0 {
		t.Fatalf("first sample: %+v %q %v", s, reason, err)
	}
	files["/cg/cpu.stat"] = "usage_usec 3500000\n" // 2.5 CPU s over 5 s = 0.5 cores
	s, _, _ = cs.Sample(ctx, tg, t0.Add(5*time.Second))
	if s.CPUMillis != 500 {
		t.Fatalf("cgroup cpu millis = %d, want 500", s.CPUMillis)
	}

	table := &fakeTable{procs: []Proc{
		{PID: 1, PPID: 0, CPUTicks: 999, RSSBytes: 999},
		{PID: 10, PPID: 1, CPUTicks: 100, RSSBytes: 1000},
		{PID: 11, PPID: 10, CPUTicks: 50, RSSBytes: 500},
		{PID: 12, PPID: 11, CPUTicks: 50, RSSBytes: 250},
	}}
	ps := NewProcessTreeSampler(table)
	pt := Target{RunnerID: "r2", RootPID: 10}
	s, _, _ = ps.Sample(ctx, pt, t0)
	if !s.Known || s.MemoryBytes != 1750 || s.CPUMillis != 0 {
		t.Fatalf("tree first: %+v", s)
	}
	for i := range table.procs { // 200 more ticks = 2 CPU s over 5 s
		if table.procs[i].PID >= 10 {
			table.procs[i].CPUTicks += 100 * 2 / 3 // 66 each, 198 total
		}
	}
	table.procs[3].CPUTicks += 2
	s, _, _ = ps.Sample(ctx, pt, t0.Add(5*time.Second))
	if s.CPUMillis != 400 {
		t.Fatalf("tree cpu millis = %d, want 400", s.CPUMillis)
	}
}

func TestUnknownWithReason(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		files map[string]string
		tg    Target
	}{
		"no path":     {nil, Target{}},
		"no cpu.stat": {map[string]string{}, Target{CgroupPath: "/x"}},
		"bad usage":   {map[string]string{"/x/cpu.stat": "foo 1\n"}, Target{CgroupPath: "/x"}},
		"no memory":   {map[string]string{"/x/cpu.stat": "usage_usec 1\n"}, Target{CgroupPath: "/x"}},
		"memory text": {map[string]string{"/x/cpu.stat": "usage_usec 1\n", "/x/memory.current": "max"}, Target{CgroupPath: "/x"}},
	}
	for name, c := range cases {
		s, reason, err := NewCgroupSampler(fakeFS(c.files)).Sample(ctx, c.tg, t0)
		if err != nil || s.Known || reason == "" {
			t.Errorf("%s: %+v %q %v", name, s, reason, err)
		}
	}
	ps := NewProcessTreeSampler(&fakeTable{err: errors.New("no /proc")})
	if s, r, _ := ps.Sample(ctx, Target{RootPID: 5}, t0); s.Known || r == "" {
		t.Errorf("table error: %+v %q", s, r)
	}
	ps = NewProcessTreeSampler(&fakeTable{})
	if s, r, _ := ps.Sample(ctx, Target{RootPID: 5}, t0); s.Known || r == "" {
		t.Errorf("missing root: %+v %q", s, r)
	}
	if s, r, _ := ps.Sample(ctx, Target{}, t0); s.Known || r == "" {
		t.Errorf("no pid: %+v %q", s, r)
	}
}

func TestParseStat(t *testing.T) {
	raw := []byte("42 (my (odd) name) S 7 1 1 0 -1 0 0 0 0 0 30 20 0 0 20 0 1 0 100 1000 5 18446744073709551615")
	p, ok := parseStat(raw, 42, 4096)
	if !ok || p.PPID != 7 || p.CPUTicks != 50 || p.RSSBytes != 5*4096 {
		t.Fatalf("%+v %v", p, ok)
	}
	if _, ok := parseStat([]byte("garbage"), 1, 4096); ok {
		t.Fatal("garbage parsed")
	}
}

type recSender struct {
	mu    sync.Mutex
	calls []call
}
type call struct {
	t       Target
	n       int
	final   bool
	unknown string
}

func (r *recSender) Send(_ context.Context, t Target, _ Source, s []Sample, final bool, unknown string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{t, len(s), final, unknown})
	return nil
}

func TestCollectorBatchesAndFinal(t *testing.T) {
	ctx := context.Background()
	fs := map[string]string{"/cg/cpu.stat": "usage_usec 0\n", "/cg/memory.current": "1"}
	snd := &recSender{}
	forgot := 0
	c := &Collector{Cgroup: NewCgroupSampler(fakeFS(fs)), Sender: snd, Forget: func(Target) { forgot++ }}
	tg := Target{RunnerID: "r", CgroupPath: "/cg"}
	c.Start(tg, t0)
	for i := 1; i <= 7; i++ { // ticks at 5..35 s
		c.Tick(ctx, t0.Add(time.Duration(i)*TreeInterval))
	}
	// batches at 15 s (3 samples) and 30 s (3 samples); 1 left
	if len(snd.calls) != 2 || snd.calls[0].n != 3 || snd.calls[1].n != 3 {
		t.Fatalf("calls %+v", snd.calls)
	}
	c.Finish(ctx, tg, t0.Add(37*time.Second), false)
	last := snd.calls[len(snd.calls)-1]
	if len(snd.calls) != 3 || !last.final || last.n != 2 || forgot != 1 {
		t.Fatalf("final %+v forgot %d", snd.calls, forgot)
	}
	c.Finish(ctx, tg, t0.Add(40*time.Second), false) // unknown job: nothing
	if len(snd.calls) != 3 {
		t.Fatal("finish of unknown job sent")
	}
}

func TestCollectorDisabledSendsNothing(t *testing.T) {
	ctx := context.Background()
	snd := &recSender{}
	c := &Collector{ProcessTree: NewProcessTreeSampler(&fakeTable{}), Sender: snd, Enabled: func() bool { return false }}
	tg := Target{RunnerID: "r", RootPID: 1}
	c.Start(tg, t0)
	c.Tick(ctx, t0.Add(20*time.Second))
	c.Finish(ctx, tg, t0.Add(25*time.Second), false)
	if len(snd.calls) != 0 {
		t.Fatalf("sent %+v", snd.calls)
	}
}

func TestCollectorUnknownReasonSent(t *testing.T) {
	ctx := context.Background()
	snd := &recSender{}
	c := &Collector{ProcessTree: NewProcessTreeSampler(&fakeTable{err: errors.New("x")}), Sender: snd}
	tg := Target{RunnerID: "r", RootPID: 1}
	c.Start(tg, t0)
	c.Finish(ctx, tg, t0.Add(5*time.Second), false)
	if len(snd.calls) != 1 || snd.calls[0].unknown == "" || !snd.calls[0].final {
		t.Fatalf("%+v", snd.calls)
	}
}

func TestRunStopsOnClose(t *testing.T) {
	ticks := make(chan time.Time, 2)
	ticks <- t0
	ticks <- t0.Add(Interval)
	close(ticks)
	snd := &recSender{}
	c := &Collector{ProcessTree: NewProcessTreeSampler(&fakeTable{}), Sender: snd}
	c.Start(Target{RunnerID: "r", RootPID: 1}, t0)
	c.Run(context.Background(), ticks) // returns because ticks closed
	_ = fmt.Sprint(snd)
}
