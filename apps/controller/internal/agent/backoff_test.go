package agent_test

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
)

// R5.36: exponential backoff: 1 second, doubled each time, capped at 60
// seconds, with up to 20% random jitter. A successful session resets it.
func TestBackoffDoublesCapsAndResets(t *testing.T) {
	b := &agent.Backoff{Rand: func() float64 { return 0 }}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("step %d = %v, want %v", i, got, w*time.Second)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after reset = %v, want 1s", got)
	}
}

func TestBackoffJitterUpTo20Percent(t *testing.T) {
	hi := &agent.Backoff{Rand: func() float64 { return 0.999999 }}
	for _, base := range []time.Duration{1, 2, 4, 8, 16, 32, 60} {
		got := hi.Next()
		max := time.Duration(float64(base*time.Second) * 1.2)
		if got < base*time.Second || got > max {
			t.Fatalf("base %v: %v outside [base, base+20%%]", base, got)
		}
	}
	// Real randomness stays in range too.
	r := &agent.Backoff{}
	for i := 0; i < 200; i++ {
		r.Reset()
		if d := r.Next(); d < time.Second || d > 1200*time.Millisecond {
			t.Fatalf("jittered first delay = %v", d)
		}
	}
}
