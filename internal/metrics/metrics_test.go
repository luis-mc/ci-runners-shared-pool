package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
)

func sample() Snapshot {
	return Snapshot{
		RunnersByState:     map[string]int{"idle": 3, "busy": 2},
		QueueWaitSeconds:   []float64{2, 10, 400},
		JobDurationSeconds: []float64{50, 700},
		FallbackTotal:      4, CacheHits: 3, CacheMisses: 1,
		Machines: []MachineUptime{{Name: `b"x`, OS: "linux", Online: true, UptimeSeconds: 90}, {Name: "a", OS: "windows"}},
	}
}

func TestPrometheusText(t *testing.T) {
	var b bytes.Buffer
	WritePrometheus(&b, sample())
	out := b.String()
	for _, want := range []string{
		`crsp_runners{state="busy"} 2`,
		`crsp_queue_wait_seconds_bucket{le="5"} 1`,
		`crsp_queue_wait_seconds_bucket{le="+Inf"} 3`,
		`crsp_queue_wait_seconds_count 3`,
		`crsp_job_duration_seconds_sum 750`,
		`crsp_fallback_jobs_total 4`,
		`crsp_cache_hit_ratio 0.75`,
		`crsp_machine_up{machine="a",os="windows"} 0`,
		`crsp_machine_uptime_seconds{machine="b\"x",os="linux"} 90`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestCacheHitRateZeroLookups(t *testing.T) {
	if (Snapshot{}).CacheHitRate() != 0 {
		t.Fatal("no lookups must give 0")
	}
}

func TestHandlerToken(t *testing.T) {
	h := Handler(SourceFunc(func(context.Context) (Snapshot, error) { return sample(), nil }), "secret-token-123456")
	for _, c := range []struct {
		auth string
		code int
	}{{"", 401}, {"Bearer nope", 401}, {"Bearer secret-token-123456", 200}} {
		r := httptest.NewRequest("GET", "/metrics", nil)
		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Errorf("auth %q: %d, want %d", c.auth, w.Code, c.code)
		}
	}
	open := Handler(SourceFunc(func(context.Context) (Snapshot, error) { return sample(), nil }), "")
	w := httptest.NewRecorder()
	open.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestOTLPSameMetricSet(t *testing.T) {
	now := time.Unix(1000, 0)
	body, err := EncodeOTLP(sample(), "v1", now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ResourceMetrics []struct {
			ScopeMetrics []struct {
				Metrics []struct {
					Name      string `json:"name"`
					Histogram *struct {
						DataPoints []struct {
							BucketCounts []string `json:"bucketCounts"`
						} `json:"dataPoints"`
					} `json:"histogram"`
				} `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range doc.ResourceMetrics[0].ScopeMetrics[0].Metrics {
		got[m.Name] = true
		if m.Name == NameQueueWait {
			bc := m.Histogram.DataPoints[0].BucketCounts
			if len(bc) != len(Buckets)+1 {
				t.Errorf("bucket counts %d", len(bc))
			}
		}
	}
	var prom bytes.Buffer
	WritePrometheus(&prom, sample())
	for _, n := range []string{NameRunners, NameQueueWait, NameJobDuration, NameFallback, NameCacheHitRate, NameCacheLookups, NameMachineUptime, NameMachineUp} {
		if !got[n] {
			t.Errorf("OTLP lacks %s", n)
		}
		if !strings.Contains(prom.String(), "# TYPE "+n+" ") {
			t.Errorf("Prometheus lacks %s", n)
		}
	}
}

func TestPushOnce(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotType string
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath, gotType = r.URL.Path, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer ts.Close()
	u, err := OTLPURL(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := &Pusher{Source: SourceFunc(func(context.Context) (Snapshot, error) { return sample(), nil }), Endpoint: u, Version: "v1"}
	if err := p.PushOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v1/metrics" || gotType != "application/json" || !json.Valid(gotBody) {
		t.Fatalf("path %q type %q", gotPath, gotType)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	p.Endpoint = bad.URL
	if err := p.PushOnce(context.Background(), time.Now()); err == nil {
		t.Fatal("a 500 must fail")
	}
}

func TestConfig(t *testing.T) {
	c := Default()
	if c.Enabled || c.PushEnabled || c.Expose || c.ListenAddr() != "127.0.0.1:9464" {
		t.Fatalf("not off and local by default: %+v", c)
	}
	c.Expose = true
	if c.ListenAddr() != "0.0.0.0:9464" {
		t.Fatal(c.ListenAddr())
	}
	if len(Default().Validate()) != 0 {
		t.Fatal("default must be valid")
	}
	bad := Config{Port: 80, PushSeconds: 1, PushEnabled: true, PushEndpoint: "ftp://x"}
	if len(bad.Validate()) != 3 {
		t.Fatalf("problems: %v", bad.Validate())
	}
	for in, want := range map[string]string{"http://c:4318": "http://c:4318/v1/metrics", "https://c/x": "https://c/x"} {
		if got, err := OTLPURL(in); err != nil || got != want {
			t.Errorf("%s -> %s %v", in, got, err)
		}
	}
	if _, err := OTLPURL("http://u:p@c"); err == nil {
		t.Fatal("password in url must fail")
	}
}

func TestScrapeConfig(t *testing.T) {
	c := Default()
	c.RequireToken = true
	s := ScrapeConfig(c, "pc.lan")
	if !strings.Contains(s, "localhost:9464") || !strings.Contains(s, "YOUR-TOKEN") {
		t.Fatal(s)
	}
	c.Expose = true
	if !strings.Contains(ScrapeConfig(c, "pc.lan"), "pc.lan:9464") {
		t.Fatal("exposed page names the host")
	}
}

type memStore struct{ c Config }

func (m *memStore) Load(context.Context) (Config, error)                  { return m.c, nil }
func (m *memStore) Save(_ context.Context, _ audit.Actor, c Config) error { m.c = c; return nil }

type memToken struct{ t string }

func (m *memToken) Get(context.Context) (string, error)   { return m.t, nil }
func (m *memToken) Put(_ context.Context, t string) error { m.t = t; return nil }

func TestServiceLifecycle(t *testing.T) {
	src := SourceFunc(func(context.Context) (Snapshot, error) { return sample(), nil })
	svc := &Service{Store: &memStore{c: Default()}, Token: &memToken{}, Source: src}
	defer svc.Close()
	ctx := context.Background()
	if err := svc.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.State(ctx); st.Running {
		t.Fatal("must be off by default")
	}
	c := Default()
	c.Enabled, c.RequireToken, c.Port = true, true, 19464
	if _, err := svc.Update(ctx, audit.Actor{Login: "owner"}, c); err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
	tok, err := svc.MakeToken(ctx)
	if err != nil || len(tok) < MinTokenLength {
		t.Fatalf("token %q %v", tok, err)
	}
	c.Port = 0 // free port is not allowed: validate refuses
	if _, err := svc.Update(ctx, audit.Actor{Login: "owner"}, c); err == nil {
		t.Fatal("port 0 must be refused")
	}
	c.Port = 19464
	st, err := svc.Update(ctx, audit.Actor{Login: "owner"}, c)
	if err != nil {
		t.Skipf("port busy on this machine: %v", err)
	}
	if !st.Running || !strings.HasPrefix(st.Addr, "127.0.0.1:") || !st.TokenSet {
		t.Fatalf("state %+v", st)
	}
	r, _ := http.NewRequest("GET", "http://"+st.Addr+"/metrics", nil)
	if resp, err := http.DefaultClient.Do(r); err != nil || resp.StatusCode != 401 {
		t.Fatalf("no token must be 401: %v", err)
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(r)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("with token: %v", err)
	}
	resp.Body.Close()
	c.Enabled = false
	if st, _ := svc.Update(ctx, audit.Actor{Login: "owner"}, c); st.Running {
		t.Fatal("must stop")
	}
}

func TestPoolSource(t *testing.T) {
	t0 := time.Unix(10000, 0)
	now := t0
	online := true
	p := &PoolSource{Started: t0, Now: func() time.Time { return now }, Feeds: Feeds{
		RunnerStates: func(context.Context) (map[string]int, error) { return map[string]int{"idle": 1}, nil },
		FinishedJobs: func(_ context.Context, since time.Time) ([]JobFact, error) {
			if !since.Equal(t0) {
				t.Errorf("since %v", since)
			}
			return []JobFact{
				{ID: 1, Queued: t0, Started: t0.Add(5 * time.Second), Completed: t0.Add(65 * time.Second), Cache: "warm"},
				{ID: 2, Hosted: true, Cache: "cold"},
			}, nil
		},
		Machines: func(context.Context) ([]MachineFact, error) {
			return []MachineFact{{Name: "m", OS: "linux", Online: online}}, nil
		},
	}}
	s, _ := p.Snapshot(context.Background())
	if len(s.QueueWaitSeconds) != 1 || s.QueueWaitSeconds[0] != 5 || s.JobDurationSeconds[0] != 60 {
		t.Fatalf("%+v", s)
	}
	if s.FallbackTotal != 1 || s.CacheHitRate() != 0.5 || s.Machines[0].UptimeSeconds != 0 {
		t.Fatalf("%+v", s)
	}
	now = t0.Add(30 * time.Second)
	if s, _ = p.Snapshot(context.Background()); s.Machines[0].UptimeSeconds != 30 {
		t.Fatalf("uptime %v", s.Machines[0].UptimeSeconds)
	}
	online = false
	if s, _ = p.Snapshot(context.Background()); s.Machines[0].UptimeSeconds != 0 || s.Machines[0].Online {
		t.Fatal("offline machine has no uptime")
	}
	if _, err := (&PoolSource{}).Snapshot(context.Background()); err != nil {
		t.Fatal("nil feeds must not fail")
	}
}
