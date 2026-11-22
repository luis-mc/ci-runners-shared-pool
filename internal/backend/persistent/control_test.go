package persistent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
)

// fakeHost is the service manager of one OS: a listener and a worker that the
// start and stop steps switch, and a record of what was asked.
type fakeHost struct {
	listener, worker bool
	calls            []string
	stopLeavesOn     bool // the listener ignores the stop (a hung exit)
	startMaintenance bool
}

func (f *fakeHost) control() *Control {
	return &Control{
		Folder: func(_ context.Context, ref string) (string, bool, error) {
			if ref == "gone" {
				return "", false, nil
			}
			return "/runners/" + ref, true, nil
		},
		Listeners: func(context.Context, string) ([]int, error) {
			if f.listener {
				return []int{10}, nil
			}
			return nil, nil
		},
		Workers: func(context.Context, string) ([]int, error) {
			if f.worker {
				return []int{11}, nil
			}
			return nil, nil
		},
		StartService: func(_ context.Context, ref string, maint bool) error {
			f.calls = append(f.calls, "start "+ref)
			f.listener, f.startMaintenance = true, maint
			return nil
		},
		StopService: func(_ context.Context, ref string) error {
			f.calls = append(f.calls, "stop "+ref)
			if !f.stopLeavesOn {
				f.listener = false
			}
			return nil
		},
		Sleep: func(context.Context, time.Duration) {}, Poll: time.Millisecond, Wait: 3 * time.Millisecond,
	}
}

func TestListenerStateOfflineOnlineBusy(t *testing.T) {
	f := &fakeHost{}
	c := f.control()
	want := func(w backend.ListenerState) {
		t.Helper()
		if got, err := c.ListenerState(context.Background(), "r1"); err != nil || got != w {
			t.Fatalf("state %q err %v, want %q", got, err, w)
		}
	}
	want(backend.ListenerOffline)
	f.listener = true
	want(backend.ListenerOnline)
	f.worker = true
	want(backend.ListenerBusy)
	if _, err := c.ListenerState(context.Background(), "gone"); err == nil {
		t.Error("an unknown runner was not refused")
	}
}

func TestStartListenerIsIdempotentAndPassesMaintenance(t *testing.T) {
	f := &fakeHost{}
	c := f.control()
	if err := c.StartListener(context.Background(), "r1", true); err != nil || !f.startMaintenance {
		t.Fatalf("start: %v maint=%v", err, f.startMaintenance)
	}
	if err := c.StartListener(context.Background(), "r1", false); err != nil || len(f.calls) != 1 {
		t.Errorf("a start beside a live listener started again: %v %v", err, f.calls)
	}
}

func TestStopWaitsForCleanExit(t *testing.T) {
	f := &fakeHost{listener: true}
	if err := f.control().StopListener(context.Background(), "r1", false); err != nil {
		t.Fatal(err)
	}
	if f.listener || len(f.calls) != 1 {
		t.Errorf("listener %v calls %v", f.listener, f.calls)
	}
}

func TestStopNeverKillsAHungListener(t *testing.T) {
	f := &fakeHost{listener: true, stopLeavesOn: true}
	err := f.control().StopListener(context.Background(), "r1", false)
	if !errors.Is(err, ErrStopTimeout) || !f.listener {
		t.Fatalf("err %v listener %v", err, f.listener)
	}
}

func TestStopRefusedWhileAJobIsAssigned(t *testing.T) {
	f := &fakeHost{listener: true}
	c := f.control()
	if err := c.StopListener(context.Background(), "r1", true); !errors.Is(err, ErrJobAssigned) {
		t.Errorf("controller says assigned: %v", err)
	}
	f.worker = true
	if err := c.StopListener(context.Background(), "r1", false); !errors.Is(err, ErrJobAssigned) {
		t.Errorf("a worker runs: %v", err)
	}
	if len(f.calls) != 0 || !f.listener {
		t.Errorf("a refused stop still acted: %v", f.calls)
	}
}
