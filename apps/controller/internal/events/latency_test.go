package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// R6.1: a change reaches an open console within 2 seconds at the 95th percentile. The test runs the real path
// from Publish through the hub and the SSE writer to several open HTTP clients on this machine, so it holds the
// controller's share of the budget; the local network adds the rest. Each event carries the time it was
// published and each client measures the gap when it reads the event.
func TestP95UnderTwoSeconds(t *testing.T) {
	const clients, changes = 4, 100
	h := NewHub(Options{Snapshotter: snap{}, MaxStreams: 16})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := h.Subscribe(r.Context(), 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		Serve(w, r, s, time.Second)
	}))
	defer srv.Close()

	type got struct{ gap time.Duration }
	seen := make(chan got, clients*changes)
	ready := make(chan struct{}, clients)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < clients; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		go func() {
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			first := true
			for sc.Scan() {
				line := sc.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				if first { // the snapshot
					first = false
					ready <- struct{}{}
					continue
				}
				var ev struct {
					Job struct {
						Sent int64 `json:"sent"`
					} `json:"job"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) != nil {
					continue
				}
				seen <- got{time.Since(time.Unix(0, ev.Job.Sent))}
			}
		}()
	}
	for i := 0; i < clients; i++ {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("a client got no snapshot")
		}
	}

	var gaps []time.Duration
	for n := 0; n < changes; n++ {
		h.Publish(JobStarted, []byte(fmt.Sprintf(`{"sent":%d}`, time.Now().UnixNano())))
		for i := 0; i < clients; i++ {
			select {
			case g := <-seen:
				gaps = append(gaps, g.gap)
			case <-time.After(5 * time.Second):
				t.Fatalf("change %d did not reach every client", n)
			}
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	p95 := gaps[len(gaps)*95/100]
	if len(gaps) != clients*changes || p95 >= 2*time.Second {
		t.Fatalf("%d gaps, p95 %v, want %d gaps under 2s", len(gaps), p95, clients*changes)
	}
}
