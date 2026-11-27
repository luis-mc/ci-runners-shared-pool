package container

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

// cpuPeriod is the period of the CPU quota a build gets while jobs run.
const cpuPeriod = 100000

// jobsRunning says whether a job runs in any pool container of this machine.
// Without a CPU budget the backend does not know its pools and says no.
func (b *Backend) jobsRunning(ctx context.Context) bool {
	if b.cpus == nil {
		return false
	}
	for _, pool := range b.cpus.Holders() {
		if busy, err := b.busy(ctx, pool); err != nil || busy {
			return true // fail closed: a doubtful answer keeps the build small
		}
	}
	return false
}

// buildArgs adds the real progress stream and the core limit: all cores with
// no job running, half while jobs run (RES19 decision D20).
func (b *Backend) buildArgs(ctx context.Context, args []string) []string {
	total := b.cfg.BuildCores
	if total < 1 {
		total = runtime.NumCPU()
	}
	out := []string{args[0], "--progress=plain"}
	if b.jobsRunning(ctx) {
		n := builds.Cores(total, true)
		out = append(out, "--cpu-period="+strconv.Itoa(cpuPeriod), "--cpu-quota="+strconv.Itoa(n*cpuPeriod))
		b.log("prepare image", fmt.Sprintf("jobs run: the build uses %d of %d cores", n, total))
	}
	out = append(out, args[1:]...)
	return out
}

// normArch gives the arch the records use.
func normArch(a string) string {
	switch strings.ToLower(a) {
	case "", "native":
		return runtime.GOARCH
	case "x64", "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return a
}

// streamBuild runs `docker build` and reads its progress while it runs: each
// step becomes a log line and a build record update (R5.68, R5.69). It returns
// the size of the new image in MB. The error text is what `step` gave before.
func (b *Backend) streamBuild(ctx context.Context, spec backend.ImageSpec, key string, args []string) (int64, error) {
	meta := builds.MetaFrom(ctx)
	meta.MachineID, meta.Key, meta.Arch = b.cfg.MachineID, key, normArch(spec.Arch)
	var h builds.Handle
	sink := b.buildSink()
	if sink != nil {
		got, err := sink.Begin(ctx, meta)
		if err != nil {
			b.log("prepare image", "the build is not recorded: "+err.Error())
		} else {
			h = got
		}
	}
	start := time.Now()
	parser, tail := builds.NewParser(), builds.NewTail(b.redact)
	var lastSent builds.Progress
	var lastAt time.Time
	report := func(p builds.Progress) {
		changed := p.Step != lastSent.Step || p.Name != lastSent.Name || p.State != lastSent.State || p.Total != lastSent.Total
		if !changed && time.Since(lastAt) < reportEvery {
			return
		}
		lastSent, lastAt = p, time.Now()
		p.Elapsed = time.Since(start).Seconds()
		b.log("build", describe(p, h.TypicalSeconds))
		if sink != nil && h.ID != "" {
			sink.Progress(ctx, h.ID, p)
		}
	}
	onLine := func(line string) {
		tail.Add(line)
		if p, ok := parser.Feed(line); ok {
			report(p)
		}
	}

	c, err := b.eng.Command(b.buildArgs(ctx, args)...)
	var res sys.Result
	if err == nil {
		if sc, ok := b.d.Cmd.(sys.StreamCommands); ok {
			res, err = sc.RunStream(ctx, c, onLine)
		} else {
			res, err = b.d.Cmd.Run(ctx, c)
			for _, l := range strings.Split(res.Stderr+"\n"+res.Stdout, "\n") {
				onLine(l)
			}
		}
	}
	finish := func(o builds.Outcome) {
		if sink != nil && h.ID != "" {
			// The context may be the cancelled one; the record is still written.
			sink.End(context.WithoutCancel(ctx), h.ID, o)
		}
	}
	fail := func(state string, cause error) (int64, error) {
		o := builds.Outcome{State: state, LogTail: tail.String()}
		cur := parser.Current()
		o.FailureStep = cur.Name
		if state == builds.StateFailed {
			o.FailureClass, o.FailureWhy = builds.Classify(o.LogTail)
		}
		finish(o)
		return 0, cause
	}
	switch {
	case err != nil && errors.Is(ctx.Err(), context.Canceled):
		return fail(builds.StateCancelled, fmt.Errorf("build: %s", b.redact(err.Error())))
	case err != nil:
		return fail(builds.StateFailed, fmt.Errorf("build: %s", b.redact(err.Error())))
	case res.ExitCode != 0:
		return fail(builds.StateFailed, fmt.Errorf("build failed (exit %d): %s", res.ExitCode, b.redact(strings.TrimSpace(res.Stderr))))
	}
	tag := ImageTag(key)
	mb, ok, err := b.imageSize(ctx, tag)
	if err != nil || !ok {
		return fail(builds.StateFailed, fmt.Errorf("the image %s was built but cannot be read: %v", key, err))
	}
	finish(builds.Outcome{State: builds.StateDone, SizeBytes: mb << 20, LogTail: tail.String()})
	return mb, nil
}

// describe is the line a person reads while a build runs: step n of m, its
// name, the layer and bytes when one is being fetched, the time so far and the
// time left from earlier builds (R5.68).
func describe(p builds.Progress, typical float64) string {
	s := fmt.Sprintf("step %d of %d", p.Step, p.Total)
	if p.Name != "" {
		s += ": " + p.Name
	}
	switch p.State {
	case builds.StepCached:
		s += " (cached)"
	case builds.StepDone:
		s += " (done)"
	case builds.StepFailed:
		s += " (failed)"
	}
	if p.Layer != "" && p.BytesTotal > 0 {
		s += fmt.Sprintf(", layer %s %s of %s", p.Layer, mbText(p.BytesDone), mbText(p.BytesTotal))
	}
	s += ", " + secText(p.Elapsed) + " so far"
	if rest := typical - p.Elapsed; typical > 0 && rest > 0 {
		s += ", about " + secText(rest) + " left"
	}
	return s
}

func mbText(n int64) string { return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " MB" }

func secText(s float64) string {
	d := int(s + 0.5)
	if d < 60 {
		return strconv.Itoa(d) + " s"
	}
	return strconv.Itoa(d/60) + " min " + strconv.Itoa(d%60) + " s"
}

// BuildLink is the build sink of the agent process, set by the agent when it
// runs: the build records live on the controller. A backend whose Config has
// no Builds uses it.
var BuildLink = &BuildSlot{}

// BuildSlot forwards to the builds.Sink set into it; empty it records nothing.
type BuildSlot struct {
	mu sync.RWMutex
	s  builds.Sink
}

// Set puts the sink into the slot.
func (s *BuildSlot) Set(k builds.Sink) { s.mu.Lock(); s.s = k; s.mu.Unlock() }

func (s *BuildSlot) get() builds.Sink { s.mu.RLock(); defer s.mu.RUnlock(); return s.s }

var _ builds.Sink = (*BuildSlot)(nil)

// Begin implements builds.Sink.
func (s *BuildSlot) Begin(ctx context.Context, m builds.Meta) (builds.Handle, error) {
	if k := s.get(); k != nil {
		return k.Begin(ctx, m)
	}
	return builds.Handle{}, nil
}

// Progress implements builds.Sink.
func (s *BuildSlot) Progress(ctx context.Context, id string, p builds.Progress) {
	if k := s.get(); k != nil {
		k.Progress(ctx, id, p)
	}
}

// End implements builds.Sink.
func (s *BuildSlot) End(ctx context.Context, id string, o builds.Outcome) {
	if k := s.get(); k != nil {
		k.End(ctx, id, o)
	}
}

// buildSink is where this backend's build records go: its own sink, else the
// agent's link to the controller, else none.
func (b *Backend) buildSink() builds.Sink {
	if b.cfg.Builds != nil {
		return b.cfg.Builds
	}
	return BuildLink
}
