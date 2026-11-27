package container

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

// streamCmd is the fake runner that also streams: a build call prints a
// scripted BuildKit log line by line before the fake engine answers it.
type streamCmd struct {
	cmdAdapter
	lines   []string
	gate    chan struct{} // when set, a build waits here after its lines
	started chan struct{}
}

func (s *streamCmd) RunStream(ctx context.Context, c sys.Command, on func(string)) (sys.Result, error) {
	if len(c.Args) > 0 && c.Args[0] == "build" {
		if s.started != nil {
			s.started <- struct{}{}
		}
		for _, l := range s.lines {
			on(l)
		}
		if s.gate != nil {
			<-s.gate
		}
	}
	return s.Run(ctx, c)
}

type sinkCall struct {
	Meta     builds.Meta
	Progress []builds.Progress
	Out      builds.Outcome
	Ended    bool
}

type fakeSink struct {
	mu      sync.Mutex
	typical float64
	calls   []*sinkCall
}

func (f *fakeSink) Begin(_ context.Context, m builds.Meta) (builds.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, &sinkCall{Meta: m})
	return builds.Handle{ID: "bld_" + string(rune('0'+len(f.calls))), TypicalSeconds: f.typical}, nil
}
func (f *fakeSink) Progress(_ context.Context, id string, p builds.Progress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls[int(id[len(id)-1]-'0')-1]
	c.Progress = append(c.Progress, p)
}
func (f *fakeSink) End(_ context.Context, id string, o builds.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls[int(id[len(id)-1]-'0')-1]
	c.Out, c.Ended = o, true
}

var scripted = []string{
	"#1 [internal] load build definition from Dockerfile",
	"#1 DONE 0.0s",
	"#2 [stage-1 1/3] FROM ubuntu:24.04",
	"#2 sha256:0123456789abcdef 10.0MB / 40.0MB 1.0s",
	"#2 DONE 3.0s",
	"#3 [stage-1 2/3] RUN apt-get update",
	"#3 DONE 9.0s",
	"#4 [stage-1 3/3] RUN install-layers",
	"#4 DONE 20.0s",
}

func streamRig(t *testing.T, sc *streamCmd, sink builds.Sink, mod ...func(*Config)) *rig {
	t.Helper()
	r := newRig(t, append([]func(*Config){func(c *Config) { c.Builds = sink; c.MachineID = "m1" }}, mod...)...)
	sc.cmdAdapter = cmdAdapter{r.f}
	d := r.b.d
	d.Cmd = sc
	r.b.d = d
	return r
}

// R5.68, R5.69: the build streams its steps into the log and the record, with the outcome.
func TestBuildStreamsStepsAndRecords(t *testing.T) {
	sink := &fakeSink{typical: 100}
	r := streamRig(t, &streamCmd{lines: scripted}, sink)
	ctx := builds.WithMeta(context.Background(), builds.Meta{Profile: "node-web", Reason: "not_in_registry"})
	if _, err := r.b.PrepareImage(ctx, backend.ImageSpec{OS: "linux", Arch: "x64", Layers: []string{"node"}}); err != nil {
		t.Fatal(err)
	}
	c := sink.calls[0]
	if c.Meta.MachineID != "m1" || c.Meta.Arch != "amd64" || c.Meta.Profile != "node-web" || c.Meta.Reason != "not_in_registry" || c.Meta.Key == "" {
		t.Fatalf("meta: %+v", c.Meta)
	}
	if !c.Ended || c.Out.State != builds.StateDone || c.Out.SizeBytes != 900<<20 {
		t.Fatalf("outcome: %+v", c.Out)
	}
	last := c.Progress[len(c.Progress)-1]
	if last.Step != 3 || last.Total != 3 || last.State != builds.StepDone {
		t.Fatalf("last progress: %+v", last)
	}
	log := strings.Join(r.logs, "\n")
	for _, want := range []string{"build: step 1 of 3: FROM ubuntu:24.04", "layer 0123456789ab 10.0 MB of 40.0 MB", "step 3 of 3: RUN install-layers (done)", "left"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if !strings.Contains(r.f.text(), "docker build --progress=plain -t ") {
		t.Fatalf("the build must ask for the plain progress:\n%s", r.f.text())
	}
}

// R5.69: a failed build keeps the step, the class and a redacted tail.
func TestFailedBuildIsRecordedWithClass(t *testing.T) {
	sink := &fakeSink{}
	lines := append(append([]string{}, scripted[:5]...),
		"#3 [stage-1 2/3] RUN apt-get update",
		"#3 0.5 token "+jitSecret,
		"#3 1.0 write /var/lib/x: no space left on device",
		"#3 ERROR: process did not complete successfully: exit code: 100")
	r := streamRig(t, &streamCmd{lines: lines}, sink)
	r.f.failBuild = "no space left on device"
	r.b.learn(jitSecret) // the backend knows the secrets it was given
	_, err := r.b.PrepareImage(context.Background(), backend.ImageSpec{OS: "linux", Layers: []string{"node"}})
	if err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("error: %v", err)
	}
	o := sink.calls[0].Out
	if o.State != builds.StateFailed || o.FailureClass != builds.ClassDisk || o.FailureStep != "RUN apt-get update" || o.FailureWhy == "" {
		t.Fatalf("outcome: %+v", o)
	}
	if o.LogTail == "" || strings.Contains(o.LogTail, jitSecret) {
		t.Fatalf("tail: %q", o.LogTail)
	}
}

// RES19 D20: one build per machine at a time; a second asker for the same key does not build again.
func TestOneBuildAtATimeAndSecondFindsTheImage(t *testing.T) {
	sc := &streamCmd{lines: scripted, gate: make(chan struct{}), started: make(chan struct{}, 4)}
	sink := &fakeSink{}
	r := streamRig(t, sc, sink)
	spec := backend.ImageSpec{OS: "linux", Layers: []string{"node"}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.b.PrepareImage(context.Background(), spec); err != nil {
				t.Error(err)
			}
		}()
	}
	<-sc.started // the first build runs; the second waits at the gate
	if !r.b.buildGate.Busy() {
		t.Fatal("the gate must be held while a build runs")
	}
	// The first asker inspected twice (before and after the gate). The third
	// "image inspect" is the second asker's local check, the last step before it
	// reaches the gate; wait for it so the image is still missing for that check.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for {
		r.f.mu.Lock()
		n := r.f.count("image inspect")
		r.f.mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("the second asker never checked for the image (%d inspects)", n)
		}
	}
	close(sc.gate)
	wg.Wait()
	r.f.mu.Lock()
	n := r.f.count("build")
	r.f.mu.Unlock()
	if n != 1 || len(sink.calls) != 1 {
		t.Fatalf("%d builds, %d records; the second asker must find the first's image", n, len(sink.calls))
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "was built while this one waited") {
		t.Fatal("the waiter should say so")
	}
}

// RES19 D20: half the cores while a job runs, all of them when none runs.
func TestBuildUsesHalfTheCoresWhileJobsRun(t *testing.T) {
	r := streamRig(t, &streamCmd{lines: scripted}, nil, func(c *Config) { c.CPUBudget = 4; c.BuildCores = 8 })
	r.start("job-1", "demo", r.image("node"))

	r.f.workers = 0
	if _, err := r.b.PrepareImage(context.Background(), backend.ImageSpec{OS: "linux", Layers: []string{"go"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lastBuild(r.f), "--cpu-quota") {
		t.Fatalf("no job runs: no limit:\n%s", lastBuild(r.f))
	}

	r.f.workers = 1
	if _, err := r.b.PrepareImage(context.Background(), backend.ImageSpec{OS: "linux", Layers: []string{"rust"}}); err != nil {
		t.Fatal(err)
	}
	if got := lastBuild(r.f); !strings.Contains(got, "--cpu-quota=400000") || !strings.Contains(got, "--cpu-period=100000") {
		t.Fatalf("a job runs: half of 8 cores:\n%s", got)
	}
}

func lastBuild(f *fakeDocker) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := ""
	for _, c := range f.calls {
		if strings.HasPrefix(c, "docker build") {
			out = c
		}
	}
	return out
}

// A runner without streaming still gets its output read after the end.
func TestBuildWithoutStreamingRunnerStillParses(t *testing.T) {
	sink := &fakeSink{}
	r := newRig(t, func(c *Config) { c.Builds = sink })
	if _, err := r.b.PrepareImage(context.Background(), backend.ImageSpec{OS: "linux", Layers: []string{"node"}}); err != nil {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 || sink.calls[0].Out.State != builds.StateDone {
		t.Fatalf("%+v", sink.calls)
	}
}
