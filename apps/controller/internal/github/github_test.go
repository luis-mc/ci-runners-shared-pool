package github

import (
	"errors"
	"testing"
	"time"
)

func TestRateLimitErrorIsErrRateLimited(t *testing.T) {
	err := error(&RateLimitError{ResetAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("RateLimitError must match ErrRateLimited")
	}
	if got := err.Error(); got != "github rate limited until 2026-11-14T10:00:00Z" {
		t.Fatalf("message = %q", got)
	}
}
