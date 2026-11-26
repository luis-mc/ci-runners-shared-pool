package history

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// R5.9. A duration is never negative; an end before its start is unknown.
func TestNeverNegative(t *testing.T) {
	if s := Between(at(10), at(5)); s.Known || s.MS != 0 {
		t.Fatalf("end before start: %+v", s)
	}
	if s := Between(at(5), at(5)); !s.Known || s.MS != 0 {
		t.Fatalf("equal times are a known zero: %+v", s)
	}
}

// R5.9. A missing or zero time (0001-01-01) is unknown, never 0.
func TestZeroTimeIsUnknown(t *testing.T) {
	placeholder, err := time.Parse(time.RFC3339, "0001-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Span{
		"zero start":        Between(time.Time{}, at(5)),
		"zero end":          Between(at(5), time.Time{}),
		"placeholder start": Between(placeholder, at(5)),
		"placeholder end":   Between(at(5), placeholder),
		"both zero":         Between(time.Time{}, time.Time{}),
	} {
		if s.Known {
			t.Errorf("%s: want unknown, got %+v", name, s)
		}
	}
	if _, ok := Unknown.Duration(); ok {
		t.Error("Unknown must report not known")
	}
}

// R5.9. A re-run counts from its own attempt start, not the first attempt.
func TestRerunUsesAttemptStart(t *testing.T) {
	created, rerunStart := at(0), at(3600)
	if got := AttemptStart(2, created, rerunStart); !got.Equal(rerunStart) {
		t.Fatalf("re-run start %v, want %v", got, rerunStart)
	}
	if got := AttemptStart(1, created, time.Time{}); !got.Equal(created) {
		t.Fatalf("first attempt falls back to created_at: %v", got)
	}
	if got := AttemptStart(2, created, time.Time{}); !Missing(got) {
		t.Fatalf("a re-run without its own start is unknown: %v", got)
	}
	d, ok := Between(AttemptStart(2, created, rerunStart), at(3660)).Duration()
	if !ok || d != time.Minute {
		t.Fatalf("re-run duration %v %v, want 1m", d, ok)
	}
}
