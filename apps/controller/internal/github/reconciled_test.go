package github

import (
	"context"
	"errors"
	"testing"
	"time"
)

type srcFake struct{ ch chan Job }

func (s srcFake) Jobs(context.Context) (<-chan Job, error) { return s.ch, nil }

type reconHarness struct {
	pf    *pollFake
	src   chan Job
	ticks chan time.Time
	out   <-chan Job
	asked time.Duration
	found chan Job
	errs  chan error
}

func newRecon(t *testing.T) *reconHarness {
	t.Helper()
	h := &reconHarness{pf: &pollFake{}, src: make(chan Job, 8), ticks: make(chan time.Time),
		found: make(chan Job, 8), errs: make(chan error, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &Reconciled{Source: srcFake{h.src}, Client: h.pf,
		Targets: func(context.Context) ([]PollTarget, error) { return []PollTarget{{10, "a", "b"}}, nil },
		Tick:    func(d time.Duration) (<-chan time.Time, func()) { h.asked = d; return h.ticks, func() {} },
		OnFound: func(j Job) { h.found <- j }, OnError: func(err error) { h.errs <- err }}
	out, err := r.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.out = out
	return h
}

func TestReconciledFindsAJobTheSourceMissed(t *testing.T) { // R5.38
	h := newRecon(t)
	h.pf.set(Job{ID: 1, Repository: "a/b", Status: "queued"})
	h.ticks <- time.Now()
	if j := recv(t, h.out); j.ID != 1 || j.Status != "queued" {
		t.Fatalf("a missed webhook is found at the tick: %+v", j)
	}
	if h.asked != 5*time.Minute {
		t.Fatalf("every 5 minutes: %v", h.asked)
	}
}

func TestReconciledQuietWhenTheViewIsRight(t *testing.T) { // R5.38
	h := newRecon(t)
	h.src <- Job{ID: 1, Repository: "a/b", Status: "queued"}
	if j := recv(t, h.out); j.ID != 1 {
		t.Fatal("the source's job passes through")
	}
	h.pf.set(Job{ID: 1, Repository: "a/b", Status: "queued"})
	h.ticks <- time.Now()
	h.ticks <- time.Now() // the second send returns only after the first round ended
	select {
	case j := <-h.found:
		t.Fatalf("nothing is missing: %+v", j)
	default:
	}
}

func TestReconciledClosesAJobThatIsGone(t *testing.T) { // R5.38
	h := newRecon(t)
	h.src <- Job{ID: 1, Repository: "a/b", Status: "in_progress"}
	recv(t, h.out)
	h.ticks <- time.Now()
	if j := recv(t, h.out); j.ID != 1 || j.Status != "completed" {
		t.Fatalf("a job GitHub no longer lists is completed: %+v", j)
	}
}

func TestReconciledSeesAStatusChange(t *testing.T) { // R5.38
	h := newRecon(t)
	h.src <- Job{ID: 1, Repository: "a/b", Status: "queued"}
	recv(t, h.out)
	h.pf.set(Job{ID: 1, Repository: "a/b", Status: "in_progress", RunnerName: "r1"})
	h.ticks <- time.Now()
	if j := recv(t, h.out); j.Status != "in_progress" || j.RunnerName != "r1" {
		t.Fatalf("%+v", j)
	}
}

func TestReconciledListFailureClosesNothing(t *testing.T) { // R5.38
	h := newRecon(t)
	h.src <- Job{ID: 1, Repository: "a/b", Status: "in_progress"}
	recv(t, h.out)
	h.pf.mu.Lock()
	h.pf.err = errors.New("boom")
	h.pf.mu.Unlock()
	h.ticks <- time.Now()
	select {
	case err := <-h.errs:
		if err == nil {
			t.Fatal("an error is reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the failed round is reported")
	}
	select {
	case j := <-h.out:
		t.Fatalf("a failed listing must not close a job: %+v", j)
	default:
	}
}

func TestReconciledNeedsParts(t *testing.T) {
	if _, err := (&Reconciled{}).Jobs(context.Background()); err == nil {
		t.Fatal("must refuse")
	}
}
