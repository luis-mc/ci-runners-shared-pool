package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CRSP205: an old update's failure (result.json of an update to 0.1.13) must not
// become the outcome of a later restart. The status names the attempt of the
// restart, the plan carries it to the helper, and the old failure belongs to
// another attempt id.
func TestRestartHasItsOwnAttemptApartFromAnOldFailedUpdate(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	dir := filepath.Join(h.dir, "updates")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The record of an update to 1.3.0 that did not become healthy, days ago.
	if err := WriteResult(dir, Result{Kind: "update", From: "1.2.0", To: "1.3.0", Error: errNotHealthy.Error(), At: time.Now().Add(-72 * time.Hour), Attempt: "a-old"}); err != nil {
		t.Fatal(err)
	}
	m := New(h.m.o) // the controller starts and reads the old record
	st := m.Status(ctx)
	if st.State != StateFailed || st.ErrorAttempt != "a-old" {
		t.Fatalf("the old failure is known with its own attempt id: %+v", st)
	}

	before := m.o.Now()
	st, err := m.Apply(ctx, KindRestart, true, "octo")
	if err != nil {
		t.Fatal(err)
	}
	if st.Error != "" || st.ErrorAttempt != "" || st.State == StateFailed {
		t.Fatalf("a restart starts clean: %+v", st)
	}
	if st.Attempt == nil || st.Attempt.Kind != KindRestart || st.Attempt.ID == "" || st.Attempt.ID == "a-old" || st.Attempt.StartedAt.Before(before) {
		t.Fatalf("the restart has its own attempt: %+v", st.Attempt)
	}
	h.m = m
	if err := m.OnIdle(ctx, KindRestart); err != nil {
		t.Fatal(err)
	}
	if got := h.spawns[len(h.spawns)-1].Attempt; got != st.Attempt.ID {
		t.Fatalf("the helper plan carries attempt %q, want %q", got, st.Attempt.ID)
	}

	// The helper ends well and the service starts again as a new process.
	if err := WriteResult(dir, Result{OK: true, Kind: "restart", From: "1.4.0", To: "1.4.0", At: time.Now(), Attempt: st.Attempt.ID}); err != nil {
		t.Fatal(err)
	}
	later := New(h.m.o)
	later.started = later.started.Add(time.Minute)
	ls := later.Status(ctx)
	if ls.State == StateFailed || ls.Error != "" {
		t.Fatalf("after a good restart the old failure is gone: %+v", ls)
	}
	if ls.Attempt == nil || ls.Attempt.ID != st.Attempt.ID {
		t.Fatalf("the new process still names the restart's attempt: %+v", ls.Attempt)
	}
	if !ls.ServiceStarted.After(st.Attempt.StartedAt) {
		t.Fatalf("service start %v is not later than the yes %v", ls.ServiceStarted, st.Attempt.StartedAt)
	}
}

// A restart that did not come back healthy is a failure of that attempt, named
// by its id, so the page can tell it from an older one.
func TestFailedRestartCarriesItsAttempt(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	st, err := h.m.Apply(ctx, KindRestart, true, "octo")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.OnIdle(ctx, KindRestart); err != nil {
		t.Fatal(err)
	}
	p := h.spawns[0]
	p.Dir = filepath.Join(h.dir, "updates")
	env := Env{
		Exec:   func(context.Context, string, ...string) (int, error) { return 0, nil },
		Health: func(context.Context, string) (string, error) { return "", errNotHealthy },
		Sleep:  func(time.Duration) {},
	}
	clock := time.Now()
	env.Now = func() time.Time { clock = clock.Add(30 * time.Second); return clock }
	if err := RunPlan(ctx, p, env); err == nil {
		t.Fatal("an unhealthy restart must fail")
	}
	res, _ := ReadResult(p.Dir)
	if res.OK || res.Attempt != st.Attempt.ID || res.Kind != "restart" {
		t.Fatalf("result = %+v, want a failed restart of attempt %s", res, st.Attempt.ID)
	}
	sg, _ := ReadStage(p.Dir)
	if sg.Attempt != st.Attempt.ID || sg.AttemptKind != "restart" {
		t.Fatalf("stage = %+v", sg)
	}
}
