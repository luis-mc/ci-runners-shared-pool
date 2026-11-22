//go:build windows

package uninstall

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestStartDetachedRetriesWithoutBreakaway(t *testing.T) {
	var flags []uint32
	start := func(_ context.Context, f uint32, _ string, _ ...string) error {
		flags = append(flags, f)
		if f&createBreakawayJob != 0 {
			return fmt.Errorf("fork/exec: %w", errorAccessDenied)
		}
		return nil
	}
	if err := startDetachedWith(context.Background(), start, "msiexec.exe"); err != nil {
		t.Fatal(err)
	}
	if len(flags) != 2 || flags[1] != detachedProcess|createNewProcessGrp {
		t.Fatalf("flags = %x", flags)
	}
}

func TestStartDetachedOtherErrorIsNotRetried(t *testing.T) {
	n := 0
	start := func(context.Context, uint32, string, ...string) error { n++; return errors.New("boom") }
	if err := startDetachedWith(context.Background(), start, "x"); err == nil || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}
