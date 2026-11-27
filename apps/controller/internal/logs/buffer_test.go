package logs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func line(i int) Line {
	return Line{At: time.UnixMilli(int64(1000 + i)).UTC(), Machine: "m", Component: "agent", Message: fmt.Sprintf("line %03d %s", i, strings.Repeat("x", 40))}
}

// TestBufferKeepsOrderAndCountsDropped: lines come back in order, a full buffer
// drops the oldest first, and one line says how many went (R5.23).
func TestBufferKeepsOrderAndCountsDropped(t *testing.T) {
	dir := t.TempDir()
	b, err := openBuffer(dir, 4000) // about 8 segments of 500 bytes
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := b.Append(line(i)); err != nil {
			t.Fatal(err)
		}
	}
	lines, dropped, ack, err := b.Take(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if dropped == 0 || !strings.HasPrefix(lines[0].Message, fmt.Sprintf("dropped %d log lines", dropped)) {
		t.Fatalf("first line %q, dropped %d: want one dropped-count line first", lines[0].Message, dropped)
	}
	rest := lines[1:]
	if int64(len(rest))+dropped != 100 {
		t.Fatalf("kept %d + dropped %d != 100", len(rest), dropped)
	}
	first := int(dropped) // the oldest `dropped` lines are the ones lost
	for i, l := range rest {
		if want := line(first + i).Message; l.Message != want {
			t.Fatalf("line %d is %q, want %q (order or oldest-first broken)", i, l.Message, want)
		}
	}
	// A line written after Take survives the ack; the sent ones and the count are gone.
	if err := b.Append(line(500)); err != nil {
		t.Fatal(err)
	}
	if err := ack(); err != nil {
		t.Fatal(err)
	}
	again, d2, _, err := b.Take(time.Now())
	if err != nil || d2 != 0 || len(again) != 1 || again[0].Message != line(500).Message {
		t.Fatalf("after ack: %d lines, dropped %d, err %v", len(again), d2, err)
	}
}

// TestBufferSurvivesRestart: lines and the dropped count are on disk.
func TestBufferSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	b, _ := openBuffer(dir, 2000)
	for i := 0; i < 60; i++ {
		_ = b.Append(line(i))
	}
	want := b.Dropped()
	b2, err := openBuffer(dir, 2000)
	if err != nil || b2.Dropped() != want || want == 0 {
		t.Fatalf("dropped %d after reopen, want %d (err %v)", b2.Dropped(), want, err)
	}
	if _, err := OpenBuffer(dir, 7); err == nil {
		t.Fatal("a cap under the 8 MiB floor must be refused")
	}
}
