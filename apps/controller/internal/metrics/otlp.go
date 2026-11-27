package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// OTLP/HTTP with JSON encoding (opentelemetry-proto, ExportMetricsServiceRequest).
// It needs no library: the body is plain JSON, posted to <collector>/v1/metrics.

type kv struct {
	Key   string `json:"key"`
	Value struct {
		S string `json:"stringValue"`
	} `json:"value"`
}

func attr(k, v string) kv {
	a := kv{Key: k}
	a.Value.S = v
	return a
}

type numPoint struct {
	Attributes []kv    `json:"attributes,omitempty"`
	Start      string  `json:"startTimeUnixNano,omitempty"`
	Time       string  `json:"timeUnixNano"`
	AsDouble   float64 `json:"asDouble"`
}

type histPoint struct {
	Start          string    `json:"startTimeUnixNano"`
	Time           string    `json:"timeUnixNano"`
	Count          string    `json:"count"`
	Sum            float64   `json:"sum"`
	BucketCounts   []string  `json:"bucketCounts"`
	ExplicitBounds []float64 `json:"explicitBounds"`
}

type metric struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Unit        string `json:"unit,omitempty"`
	Gauge       *struct {
		DataPoints []numPoint `json:"dataPoints"`
	} `json:"gauge,omitempty"`
	Sum *struct {
		DataPoints             []numPoint `json:"dataPoints"`
		AggregationTemporality int        `json:"aggregationTemporality"`
		IsMonotonic            bool       `json:"isMonotonic"`
	} `json:"sum,omitempty"`
	Histogram *struct {
		DataPoints             []histPoint `json:"dataPoints"`
		AggregationTemporality int         `json:"aggregationTemporality"`
	} `json:"histogram,omitempty"`
}

func gauge(name, desc, unit string, pts ...numPoint) metric {
	m := metric{Name: name, Description: desc, Unit: unit}
	m.Gauge = &struct {
		DataPoints []numPoint `json:"dataPoints"`
	}{pts}
	return m
}

func counter(name, desc string, pts ...numPoint) metric {
	m := metric{Name: name, Description: desc}
	m.Sum = &struct {
		DataPoints             []numPoint `json:"dataPoints"`
		AggregationTemporality int        `json:"aggregationTemporality"`
		IsMonotonic            bool       `json:"isMonotonic"`
	}{pts, 2, true} // 2 = cumulative
	return m
}

func hist(name, desc string, h Hist, start, now string) metric {
	// OTLP bucket counts are per bucket, with one more for +Inf; ours are cumulative.
	counts := make([]string, 0, len(h.Counts)+1)
	var prev uint64
	for _, c := range h.Counts {
		counts = append(counts, strconv.FormatUint(c-prev, 10))
		prev = c
	}
	counts = append(counts, strconv.FormatUint(h.Count-prev, 10))
	m := metric{Name: name, Description: desc, Unit: "s"}
	m.Histogram = &struct {
		DataPoints             []histPoint `json:"dataPoints"`
		AggregationTemporality int         `json:"aggregationTemporality"`
	}{[]histPoint{{Start: start, Time: now, Count: strconv.FormatUint(h.Count, 10), Sum: h.Sum, BucketCounts: counts, ExplicitBounds: h.Bounds}}, 1} // 1 = delta
	return m
}

// EncodeOTLP builds the OTLP/HTTP JSON body of s. It carries the same metrics,
// names and values as WritePrometheus. start is when this window began; now is
// when it ends.
func EncodeOTLP(s Snapshot, version string, start, now time.Time) ([]byte, error) {
	ts := strconv.FormatInt(now.UnixNano(), 10)
	st := strconv.FormatInt(start.UnixNano(), 10)
	pt := func(v float64, a ...kv) numPoint { return numPoint{Attributes: a, Time: ts, AsDouble: v} }
	cpt := func(v float64, a ...kv) numPoint { p := pt(v, a...); p.Start = st; return p }

	var ms []metric
	states := make([]string, 0, len(s.RunnersByState))
	for k := range s.RunnersByState {
		states = append(states, k)
	}
	sort.Strings(states)
	var rp []numPoint
	for _, k := range states {
		rp = append(rp, pt(float64(s.RunnersByState[k]), attr("state", k)))
	}
	ms = append(ms, gauge(NameRunners, "Runners by state.", "{runner}", rp...))
	ms = append(ms,
		hist(NameQueueWait, "Seconds recent jobs waited in the queue.", NewHist(s.QueueWaitSeconds), st, ts),
		hist(NameJobDuration, "Seconds recent jobs ran.", NewHist(s.JobDurationSeconds), st, ts),
		counter(NameFallback, "Jobs sent to the hosted fallback since the controller started.", cpt(float64(s.FallbackTotal))),
		counter(NameCacheLookups, "Cache lookups since the controller started, by result.",
			cpt(float64(s.CacheHits), attr("result", "hit")), cpt(float64(s.CacheMisses), attr("result", "miss"))),
		gauge(NameCacheHitRate, "Cache hits over lookups, 0 to 1.", "1", pt(s.CacheHitRate())),
	)
	machines := append([]MachineUptime(nil), s.Machines...)
	sort.Slice(machines, func(i, j int) bool { return machines[i].Name < machines[j].Name })
	var up, online []numPoint
	for _, m := range machines {
		a := []kv{attr("machine", m.Name), attr("os", m.OS)}
		up = append(up, pt(m.UptimeSeconds, a...))
		v := 0.0
		if m.Online {
			v = 1
		}
		online = append(online, pt(v, a...))
	}
	ms = append(ms,
		gauge(NameMachineUptime, "Seconds the machine has been up.", "s", up...),
		gauge(NameMachineUp, "1 when the machine is online.", "1", online...),
	)
	body := map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": []kv{attr("service.name", DefaultServiceName), attr("service.version", version)}},
		"scopeMetrics": []any{map[string]any{
			"scope":   map[string]any{"name": "crsp/metrics"},
			"metrics": ms,
		}},
	}}}
	return json.Marshal(body)
}

// Pusher sends the snapshot to an OTLP collector on an interval.
type Pusher struct {
	Source   Source
	Endpoint string // already the full URL, from OTLPURL
	Interval time.Duration
	Version  string
	Client   *http.Client
	Now      func() time.Time
	// OnError is called with each failed push; the loop keeps going.
	OnError func(error)
	// OnPush is called after each good push.
	OnPush func(time.Time)
}

// PushOnce reads the source and posts one export.
func (p *Pusher) PushOnce(ctx context.Context, start time.Time) error {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	snap, err := p.Source.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read the pool numbers: %w", err)
	}
	body, err := EncodeOTLP(snap, p.Version, start, now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("push to the collector: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the collector answered %d", resp.StatusCode)
	}
	return nil
}

// Run pushes every Interval until ctx ends.
func (p *Pusher) Run(ctx context.Context) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	start := now()
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.PushOnce(ctx, start); err != nil {
				if p.OnError != nil {
					p.OnError(err)
				}
			} else if p.OnPush != nil {
				p.OnPush(now())
			}
			start = now()
		}
	}
}
