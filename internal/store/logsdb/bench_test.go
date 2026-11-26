package logsdb

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/logs"
)

// benchLines is the size of the benchmark database: 100,000 lines, not the
// 10 GiB of R6.11 (that needs about 50 million lines and a CI disk to match).
const benchLines = 100_000

// BenchmarkSearchFirstPage: the first page of a text search and filters over
// benchLines lines. R6.11 asks for under 1 second over 10 GiB; this runs on a
// smaller data size and reports the time per search.
func BenchmarkSearchFirstPage(b *testing.B) {
	ctx := context.Background()
	d, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	batch := make([]logs.Line, 0, 5000)
	for i := 0; i < benchLines; i++ {
		batch = append(batch, logs.Line{
			At: base.Add(time.Duration(i) * time.Second), Level: slog.Level(4 * (i % 4)), Machine: fmt.Sprintf("m%d", i%5),
			Runner: fmt.Sprintf("r%d", i%50), Job: fmt.Sprint(i % 1000), Component: "agent",
			Message: fmt.Sprintf("step %d of runner setup finished, volume %d almost full, token%d", i, i%97, i%13),
		})
		if len(batch) == cap(batch) {
			if err := d.Insert(ctx, batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	warn := slog.LevelWarn
	queries := map[string]logs.Query{
		"text":         {Text: "volume almost full"},
		"rare word":    {Text: "token7 setup"},
		"text+filters": {Text: "finished", Machine: "m3", MinLevel: &warn, From: base, To: base.Add(24 * time.Hour)},
		"filters only": {Runner: "r7", Job: "7"},
		"newest, no q": {},
	}
	for name, q := range queries {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				p, err := d.Search(ctx, q)
				if err != nil || len(p.Lines) == 0 {
					b.Fatalf("lines %d err %v", len(p.Lines), err)
				}
			}
		})
	}
}
