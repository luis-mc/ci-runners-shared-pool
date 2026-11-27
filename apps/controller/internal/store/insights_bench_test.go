package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// R6.7: a page of 50 out of 10,000 stored findings answers in under 300 ms.
func BenchmarkFindingsPage(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	found := make([]insights.Finding, 10000)
	for i := range found {
		found[i] = insights.Finding{Kind: insights.KindWaitHold, Scope: insights.ScopeRepo, RepositoryID: int64(i % 40), Subject: fmt.Sprint(i),
			Area: insights.AreaWaits, MinutesSavedWeek: i % 500}
	}
	fs := s.InsightFindings()
	if _, err := fs.Sync(ctx, []insights.Kind{insights.KindWaitHold}, found, tBase); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		items, _, _, err := fs.List(ctx, insights.Filter{Limit: 50, Cursor: "5000"})
		if err != nil || len(items) != 50 {
			b.Fatal(len(items), err)
		}
	}
}
