package logsdb

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/logs"
)

func seed(t *testing.T, d *DB) time.Time {
	t.Helper()
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	lines := []logs.Line{
		{At: base, Level: slog.LevelInfo, Machine: "m1", Runner: "r1", Job: "j1", Component: "setup", Message: "prepare image ubuntu"},
		{At: base.Add(time.Minute), Level: slog.LevelWarn, Machine: "m1", Runner: "r1", Job: "j1", Component: "setup", Message: "register runner failed once, retrying"},
		{At: base.Add(2 * time.Minute), Level: slog.LevelError, Machine: "m2", Runner: "r2", Job: "j2", Component: "agent", Message: "disk almost full on volume"},
		{At: base.Add(3 * time.Minute), Level: slog.LevelDebug, Machine: "m2", Component: "agent", Message: `quote " and AND OR NEAR(`},
	}
	if err := d.Insert(context.Background(), lines); err != nil {
		t.Fatal(err)
	}
	return base
}

func msgs(p logs.Page) []string {
	var out []string
	for _, l := range p.Lines {
		out = append(out, l.Message)
	}
	return out
}

// TestFullTextAndFilters: text and each filter narrow the result, newest first, and a result keeps its runner and job (R5.21).
func TestFullTextAndFilters(t *testing.T) {
	ctx := context.Background()
	d, _ := open(t)
	base := seed(t, d)
	warn, errl := slog.LevelWarn, slog.LevelError
	cases := []struct {
		name string
		q    logs.Query
		want int
	}{
		{"all", logs.Query{}, 4},
		{"text", logs.Query{Text: "disk full"}, 1},
		{"two words need both", logs.Query{Text: "disk image"}, 0},
		{"machine", logs.Query{Machine: "m1"}, 2},
		{"runner", logs.Query{Runner: "r2"}, 1},
		{"job", logs.Query{Job: "j1"}, 2},
		{"level warn and up", logs.Query{MinLevel: &warn}, 2},
		{"level error", logs.Query{MinLevel: &errl}, 1},
		{"time range", logs.Query{From: base.Add(time.Minute), To: base.Add(2 * time.Minute)}, 2},
		{"text and machine", logs.Query{Text: "runner", Machine: "m1"}, 1},
		{"fts syntax is plain text", logs.Query{Text: `quote " AND OR NEAR(`}, 1},
	}
	for _, c := range cases {
		p, err := d.Search(ctx, c.q)
		if err != nil || len(p.Lines) != c.want {
			t.Errorf("%s: %d lines %v, err %v, want %d", c.name, len(p.Lines), msgs(p), err, c.want)
		}
	}
	p, _ := d.Search(ctx, logs.Query{Job: "j1"})
	if p.Lines[0].Message[:8] != "register" || p.Lines[0].Runner != "r1" || p.Lines[0].Job != "j1" {
		t.Fatalf("newest first with ids: %+v", p.Lines[0])
	}
	// Paging by cursor.
	first, _ := d.Search(ctx, logs.Query{Limit: 3})
	if len(first.Lines) != 3 || first.NextBefore == 0 {
		t.Fatalf("first page %d, next %d", len(first.Lines), first.NextBefore)
	}
	second, _ := d.Search(ctx, logs.Query{Limit: 3, BeforeID: first.NextBefore})
	if len(second.Lines) != 1 || second.NextBefore != 0 {
		t.Fatalf("second page %d, next %d", len(second.Lines), second.NextBefore)
	}
}
