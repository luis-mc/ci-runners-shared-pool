package agent_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// ctl is a real controller link over TLS 1.3 on a random port.
type ctl struct {
	Svc  *machines.Service
	Repo *machines.MemRepo
	CA   *tlsca.Authority
	Addr string
	Now  *fakeClock
	srv  *http.Server
	mu   sync.Mutex
}

// Stop closes the link (the controller goes away); Start serves it again on the
// same address.
func (c *ctl) Stop() { c.mu.Lock(); _ = c.srv.Close(); c.mu.Unlock() }

func (c *ctl) Start(t *testing.T) {
	t.Helper()
	var ln net.Listener
	var err error
	// The old listener may still hold the port for a moment: retry on a ticker,
	// for at most one second.
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	for {
		if ln, err = (&net.ListenConfig{}).Listen(t.Context(), "tcp", c.Addr); err == nil {
			break
		}
		select {
		case <-retry.C:
			continue
		case <-ctx.Done():
		}
		break
	}
	if err != nil {
		t.Fatal(err)
	}
	srv := machines.NewLinkServer(c.Addr, c.CA, c.Svc)
	c.mu.Lock()
	c.srv = srv
	c.mu.Unlock()
	go func() { _ = srv.Serve(tls.NewListener(ln, c.CA.ServerTLS())) }()
	t.Cleanup(func() { _ = srv.Close() })
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func startController(t *testing.T) *ctl {
	t.Helper()
	ca, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New()})
	if err != nil {
		t.Fatal(err)
	}
	repo := machines.NewMemRepo()
	clk := &fakeClock{t: time.Now()}
	repo.Now = clk.Now
	svc := machines.New(machines.Options{Repo: repo, Now: clk.Now, ControllerAddress: "127.0.0.1", Fingerprint: ca.Fingerprint()})
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := machines.NewLinkServer(ln.Addr().String(), ca, svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, ca.ServerTLS())) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &ctl{Svc: svc, Repo: repo, CA: ca, Addr: ln.Addr().String(), Now: clk, srv: srv}
}

// fastConfig is an agent config with short intervals and a fake probe.
func fastConfig(t *testing.T, c *ctl, code string) agent.Config {
	t.Helper()
	sec := secretstest.New()
	p := &fakeSys{fakeProbeBase: linuxBox()}
	return agent.Config{
		Controller: c.Addr, Pin: c.CA.Fingerprint(), JoinCode: code, Secrets: sec, DataDir: t.TempDir(), Version: "test",
		Sys: p, Sampler: load.NewSampler(p),
		HeartbeatEvery: 50 * time.Millisecond, LoadEvery: 50 * time.Millisecond, RedetectEvery: time.Hour,
		SampleEvery: 20 * time.Millisecond, DrainPollEvery: 20 * time.Millisecond,
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	// 15 s: the Windows CI runners are slow under load; a pass returns at once.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(15 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-tick.C:
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func runAgent(t *testing.T, cfg agent.Config) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- agent.Run(ctx, cfg) }()
	var once sync.Once
	var result error
	stop = func() error {
		once.Do(func() { cancel(); result = <-errc })
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}
