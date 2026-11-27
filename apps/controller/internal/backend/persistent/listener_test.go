package persistent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// runners R5.46: the listener is supervised, never a second one.
func TestParsePIDs(t *testing.T) {
	got := ParsePIDs("12\n\nabc\n-3\n 407 \r\n")
	if len(got) != 2 || got[0] != 12 || got[1] != 407 {
		t.Fatalf("pids = %v", got)
	}
}

func TestWaitNoListenerReturnsWhenTheListenerEnds(t *testing.T) {
	calls := 0
	list := func(context.Context) ([]int, error) {
		calls++
		if calls < 3 {
			return []int{900}, nil
		}
		return nil, nil
	}
	slept := 0
	if err := WaitNoListener(t.Context(), list, time.Minute, time.Second, func(context.Context, time.Duration) { slept++ }); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || slept != 2 {
		t.Fatalf("calls %d slept %d", calls, slept)
	}
}

func TestWaitNoListenerRefusesWhileAListenerLives(t *testing.T) {
	list := func(context.Context) ([]int, error) { return []int{900}, nil }
	err := WaitNoListener(t.Context(), list, 3*time.Second, time.Second, func(context.Context, time.Duration) {})
	if !errors.Is(err, ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitNoListenerDoesNotGuess(t *testing.T) {
	list := func(context.Context) ([]int, error) { return nil, errors.New("no process list") }
	if err := WaitNoListener(t.Context(), list, time.Second, time.Second, nil); err == nil {
		t.Fatal("an unreadable process list must stop the start")
	}
}

// runners R5.59: terminate, then kill, then confirm; never a real process.
func TestEndListenersEscalatesTermThenKill(t *testing.T) {
	alive := []int{5}
	var sent []bool
	e := Ender{
		List: func(context.Context) ([]int, error) { return alive, nil },
		Signal: func(_ context.Context, _ []int, force bool) error {
			sent = append(sent, force)
			if force {
				alive = nil
			}
			return nil
		},
		Sleep: func(context.Context, time.Duration) {},
	}
	if err := EndListeners(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] || !sent[1] {
		t.Fatalf("signals = %v", sent)
	}
}

func TestEndListenersReportsALeftoverListener(t *testing.T) {
	e := Ender{
		List:   func(context.Context) ([]int, error) { return []int{5}, nil },
		Signal: func(context.Context, []int, bool) error { return nil },
		Sleep:  func(context.Context, time.Duration) {},
	}
	if err := EndListeners(t.Context(), e); !errors.Is(err, ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckNoListener(t *testing.T) {
	if err := CheckNoListener(t.Context(), func(context.Context) ([]int, error) { return []int{9}, nil }); !errors.Is(err, ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
	if err := CheckNoListener(t.Context(), func(context.Context) ([]int, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
}
