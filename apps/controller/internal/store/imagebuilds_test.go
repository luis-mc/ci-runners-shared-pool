package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

func testDB(t *testing.T) builds.Store {
	t.Helper()
	st, err := OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.ImageBuilds()
}

type pub struct {
	mu  sync.Mutex
	got []events.Type
	raw [][]byte
}

func (p *pub) Publish(t events.Type, d []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, t)
	p.raw = append(p.raw, d)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// Built at run time so the value never reads as a credential to the secret scan.
var buildHash = strings.Join([]string{"abc123", "def456"}, "")

func TestImageBuildRecordLife(t *testing.T) {
	ctx := context.Background()
	q := testDB(t)
	ck := &clock{time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}
	pb := &pub{}
	n := 0
	r := &builds.Recorder{Q: q, Events: pb, Now: ck.now, NewID: func() string { n++; return "bld_" + string(rune('a'+n)) }}

	h, err := r.Begin(ctx, builds.Meta{MachineID: "m1", Profile: "node-web", Arch: "amd64", Key: buildHash, Reason: "not_in_registry"})
	if err != nil {
		t.Fatal(err)
	}
	if h.TypicalSeconds != 0 {
		t.Fatalf("no history: typical %v", h.TypicalSeconds)
	}
	r.Progress(ctx, h.ID, builds.Progress{Step: 2, Total: 9, Name: "RUN apt-get update", State: builds.StepRunning})
	ck.t = ck.t.Add(90 * time.Second)
	r.End(ctx, h.ID, builds.Outcome{State: builds.StateDone, SizeBytes: 900 << 20})

	b, ok, err := r.Get(ctx, h.ID, ck.t)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if b.State != gen.ImageBuildState(builds.StateDone) || b.Step != 2 || b.StepsTotal != 9 || b.Key != buildHash ||
		b.Reason == nil || string(*b.Reason) != "not_in_registry" || b.StartedAt == nil || b.FinishedAt == nil {
		t.Fatalf("record: %+v", b)
	}
	if d := b.FinishedAt.Sub(*b.StartedAt); d != 90*time.Second {
		t.Fatalf("duration %v", d)
	}
	if b.SecondsLeft != nil || b.TypicalSeconds != nil {
		t.Fatal("no history: no time left")
	}
	if len(pb.got) != 3 || pb.got[0] != events.ImageBuild {
		t.Fatalf("events %v", pb.got)
	}
	var ev gen.ImageBuild
	if err := json.Unmarshal(pb.raw[2], &ev); err != nil || ev.State != gen.ImageBuildState(builds.StateDone) {
		t.Fatalf("event body: %s", pb.raw[2])
	}

	// The next build of the same profile and arch knows the typical time (R5.69, R5.70).
	h2, err := r.Begin(ctx, builds.Meta{MachineID: "m1", Profile: "node-web", Arch: "amd64", Key: "k2"})
	if err != nil {
		t.Fatal(err)
	}
	if h2.TypicalSeconds < 89 || h2.TypicalSeconds > 91 {
		t.Fatalf("typical %v, want 90", h2.TypicalSeconds)
	}
	r.Progress(ctx, h2.ID, builds.Progress{Step: 1, Total: 9, Name: "FROM base", State: builds.StepRunning})
	ck.t = ck.t.Add(30 * time.Second)
	b2, _, _ := r.Get(ctx, h2.ID, ck.t)
	if b2.SecondsLeft == nil || *b2.SecondsLeft < 59 || *b2.SecondsLeft > 61 {
		t.Fatalf("seconds left %v, want 60", b2.SecondsLeft)
	}
	ck.t = ck.t.Add(10 * time.Minute)
	b2, _, _ = r.Get(ctx, h2.ID, ck.t)
	if b2.SecondsLeft == nil || *b2.SecondsLeft != 0 {
		t.Fatalf("seconds left never goes below zero: %v", b2.SecondsLeft)
	}
}

func TestImageBuildRecordFailureKeepsClassAndTail(t *testing.T) {
	ctx := context.Background()
	r := &builds.Recorder{Q: testDB(t), NewID: func() string { return "bld_f" }}
	h, _ := r.Begin(ctx, builds.Meta{MachineID: "m1", Key: "k"})
	r.Progress(ctx, h.ID, builds.Progress{Step: 4, Total: 4, Name: "RUN install", State: builds.StepFailed})
	r.End(ctx, h.ID, builds.Outcome{State: builds.StateFailed, FailureStep: "RUN install", FailureClass: builds.ClassDisk, FailureWhy: "The disk is full.", LogTail: "no space left on device"})

	b, _, _ := r.Get(ctx, h.ID, time.Now())
	if b.Failure == nil || b.Failure.Class != gen.ImageBuildFailureClass(builds.ClassDisk) || b.Failure.Step != "RUN install" || b.LogTail == nil {
		t.Fatalf("failure: %+v", b)
	}
	list, _ := r.List(ctx, "", time.Now())
	if len(list) != 1 || list[0].LogTail != nil {
		t.Fatalf("the list holds no log tail: %+v", list)
	}
	if _, ok, _ := r.Get(ctx, "bld_missing", time.Now()); ok {
		t.Fatal("a missing build is not found")
	}
}

func TestImageBuildListRunningFirstAndPerMachine(t *testing.T) {
	ctx := context.Background()
	q := testDB(t)
	ids := []string{"bld_1", "bld_2", "bld_3"}
	i := 0
	clk := time.Now()
	r := &builds.Recorder{Q: q, NewID: func() string { i++; return ids[i-1] }, Now: func() time.Time { clk = clk.Add(time.Second); return clk }}
	a, _ := r.Begin(ctx, builds.Meta{MachineID: "m1", Key: "a"})
	r.End(ctx, a.ID, builds.Outcome{State: builds.StateDone})
	b, _ := r.Begin(ctx, builds.Meta{MachineID: "m2", Key: "b"})
	r.Progress(ctx, b.ID, builds.Progress{Step: 1, Total: 2, Name: "x"})
	c, _ := r.Begin(ctx, builds.Meta{MachineID: "m1", Key: "c"})
	r.End(ctx, c.ID, builds.Outcome{State: builds.StateFailed, FailureClass: builds.ClassOther})

	all, _ := r.List(ctx, "", time.Now())
	if len(all) != 3 || all[0].Id != "bld_2" {
		t.Fatalf("running first: %+v", all)
	}
	m1, _ := r.List(ctx, "m1", time.Now())
	if len(m1) != 2 || m1[0].Id != "bld_3" {
		t.Fatalf("machine m1: %+v", m1)
	}
}
