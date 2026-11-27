package insights

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// R6.3: one pass over 20,000 jobs a week. The analyser here is a plain pass over the facts, the cost the
// engine and the job source add; each real analyser has its own bench.
type hourly struct{ facts []JobFact }

func (h hourly) JobsBetween(context.Context, Window, int64) ([]JobFact, error) { return h.facts, nil }

type tally struct{ src JobSource }

func (tally) Name() string  { return "tally" }
func (tally) Kinds() []Kind { return []Kind{KindWaitHold} }
func (a tally) Analyse(ctx context.Context, w Window) ([]Finding, error) {
	facts, _ := a.src.JobsBetween(ctx, w, 0)
	wait := map[string]time.Duration{}
	for _, f := range facts {
		wait[f.JobIdent] += f.StartedAt.Sub(f.CreatedAt)
	}
	var out []Finding
	for k, d := range wait {
		if Minutes(d) > 1000 {
			out = append(out, Finding{Kind: KindWaitHold, Subject: k, MinutesSavedWeek: Minutes(d)})
		}
	}
	return out, nil
}

func BenchmarkPass20kJobs(b *testing.B) {
	facts := make([]JobFact, 20000)
	for i := range facts {
		c := t0.Add(-time.Duration(i) * 30 * time.Second)
		facts[i] = JobFact{ID: int64(i), JobIdent: fmt.Sprintf("job-%d", i%200), CreatedAt: c, StartedAt: c.Add(2 * time.Minute)}
	}
	e := NewEngine([]Analyser{tally{hourly{facts}}}, newMemStore(), nil, quiet())
	b.ReportAllocs()
	for b.Loop() {
		if err := e.RunOnce(context.Background(), t0); err != nil {
			b.Fatal(err)
		}
	}
}
