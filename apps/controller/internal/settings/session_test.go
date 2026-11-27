package settings

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionWaitDefaultsSavesAndRefuses(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	if got, err := s.GetSessionWait(ctx); err != nil || got != 6*time.Minute {
		t.Fatalf("%v %v", got, err)
	}
	if err := s.UpdateSessionWait(ctx, owner, 5); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSessionWait(ctx); got != 5*time.Minute {
		t.Fatalf("%v", got)
	}
	var re *RangeError
	if err := s.UpdateSessionWait(ctx, owner, 31); !errors.As(err, &re) {
		t.Fatalf("%v", err)
	}
	if got, _ := s.GetSessionWait(ctx); got != 5*time.Minute {
		t.Fatalf("refused change applied: %v", got)
	}
}
