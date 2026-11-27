package store

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// The grouped table and the stats read every finished job of 30 to 90 days, which is the floor of their cost (audit
// R3-2, about 150 ms on 150,000 jobs). Their answers change with the jobs table and with the clock, so they are kept
// for a short time and thrown away when a job or run is written; one read serves every open tab.
const (
	aggTTL = 10 * time.Second
	aggMax = 64
)

type aggEntry struct {
	gen int64
	at  time.Time
	v   any
}

type aggCache struct {
	mu sync.Mutex
	m  map[string]aggEntry
	sf singleflight.Group
}

// JobsVersion changes whenever the store wrote a job or a run (and now and then for other writes in a transaction): a
// reader that holds an answer made at the same version knows no job or run moved since. It starts at zero.
func (s *Store) JobsVersion() int64 { return s.jobsGen.Load() }

// touchJobs records that the jobs or runs tables were written.
func (s *Store) touchJobs() { s.jobsGen.Add(1) }

func (c *aggCache) get(key string, gen int64) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || e.gen != gen || time.Since(e.at) > aggTTL {
		return nil, false
	}
	return e.v, true
}

func (c *aggCache) put(key string, gen int64, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]aggEntry{}
	}
	if len(c.m) >= aggMax {
		for k, e := range c.m {
			if time.Since(e.at) > aggTTL {
				delete(c.m, k)
			}
		}
		for len(c.m) >= aggMax {
			for k := range c.m { // still full of fresh entries: drop one, any one
				delete(c.m, k)
				break
			}
		}
	}
	c.m[key] = aggEntry{gen: gen, at: time.Now(), v: v}
}

// cached answers key from the cache when an answer made at the current jobs version is fresh, else runs load once for
// all callers at the same time and keeps its answer. The version is read before load runs, so a write that lands during
// the read leaves the answer one version behind and it is not served again.
func cached[T any](ctx context.Context, s *Store, key string, load func(context.Context) (T, error)) (T, error) {
	gen := s.JobsVersion()
	if v, ok := s.agg.get(key, gen); ok {
		return v.(T), nil
	}
	res, err, _ := s.agg.sf.Do(fmt.Sprintf("%s@%d", key, gen), func() (any, error) {
		// The read must not die with the first caller's request.
		v, err := load(context.WithoutCancel(ctx))
		if err == nil {
			s.agg.put(key, gen, v)
		}
		return v, err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return res.(T), nil
}

// Groups is the grouped table, from the cache when it is fresh.
func (s *JobsStore) Groups(ctx context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	rows, err := cached(ctx, s.Store, "groups|"+string(by), func(ctx context.Context) ([]jobsdata.GroupRow, error) {
		return s.groups(ctx, by)
	})
	return cloneGroups(rows), err
}

// Stats is the chart data, from the cache when it is fresh.
func (s *JobsStore) Stats(ctx context.Context, days int, f jobsdata.JobFilter) (jobsdata.Stats, error) {
	st, err := cached(ctx, s.Store, fmt.Sprintf("stats|%d|%+v", days, f), func(ctx context.Context) (jobsdata.Stats, error) {
		return s.stats(ctx, days, f)
	})
	st.Days, st.Hours, st.ForecastPoints = slices.Clone(st.Days), slices.Clone(st.Hours), slices.Clone(st.ForecastPoints)
	return st, err
}

func cloneGroups(rows []jobsdata.GroupRow) []jobsdata.GroupRow {
	out := slices.Clone(rows)
	for i := range out {
		out[i].Trend = slices.Clone(out[i].Trend)
		if out[i].Light != nil {
			l := *out[i].Light
			out[i].Light = &l
		}
	}
	return out
}
