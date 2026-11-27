package logs

import (
	"context"
	"testing"
	"time"
)

type pruneSpy struct {
	cutoff time.Time
	max    int64
}

func (p *pruneSpy) Insert(context.Context, []Line) error        { return nil }
func (p *pruneSpy) Search(context.Context, Query) (Page, error) { return Page{}, nil }
func (p *pruneSpy) Prune(_ context.Context, c time.Time, m int64) (int64, error) {
	p.cutoff, p.max = c, m
	return 0, nil
}

// TestDefaultsFloorsCeilings holds the R6.8 table for logs.
func TestDefaultsFloorsCeilings(t *testing.T) {
	if RetentionDays != (Bound{14, 1, 90}) || RetentionMiBPerMachine != (Bound{512, 64, 10240}) || AgentBufferMiB != (Bound{64, 8, 1024}) {
		t.Fatalf("limits changed: %+v %+v %+v", RetentionDays, RetentionMiBPerMachine, AgentBufferMiB)
	}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	spy := &pruneSpy{}
	if _, err := Prune(context.Background(), spy, now, 14, 512); err != nil {
		t.Fatal(err)
	}
	if !spy.cutoff.Equal(now.AddDate(0, 0, -14)) || spy.max != 512<<20 {
		t.Fatalf("cutoff %v max %d", spy.cutoff, spy.max)
	}
	for _, c := range [][2]int{{0, 512}, {91, 512}, {14, 63}, {14, 10241}} {
		if _, err := Prune(context.Background(), spy, now, c[0], c[1]); err == nil {
			t.Errorf("days %d, MiB %d: want a refusal that names the range", c[0], c[1])
		}
	}
}
