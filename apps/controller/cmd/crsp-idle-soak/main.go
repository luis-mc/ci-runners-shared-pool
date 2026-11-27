// Command crsp-idle-soak measures the cost of one idle agent (machines R6.5):
// under 1% of one core and under 50 MB of heap. It starts the controller link
// and one agent at the production intervals in this process, waits for the
// agent to come online, then measures CPU and heap over a window. It exits
// non-zero when either limit is passed. It is a soak measurement, run by
// .github/workflows/soak.yml, not a unit test.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

const (
	maxCPUPercent = 1.0
	maxHeapBytes  = 50 << 20
)

func main() {
	window := flag.Duration("window", 60*time.Second, "measurement window")
	flag.Parse()
	if err := run(*window); err != nil {
		fmt.Fprintln(os.Stderr, "crsp-idle-soak:", err)
		os.Exit(1)
	}
}

func run(window time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := os.MkdirTemp("", "crsp-idle-soak-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ca, err := tlsca.LoadOrCreate(ctx, tlsca.Options{Dir: dir, Secrets: secretstest.New()})
	if err != nil {
		return err
	}
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "127.0.0.1", Fingerprint: ca.Fingerprint()})
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := machines.NewLinkServer(ln.Addr().String(), ca, svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, ca.ServerTLS())) }()
	defer func() { _ = srv.Close() }()

	code, err := svc.CreateJoinCode(ctx, "owner", 0)
	if err != nil {
		return err
	}
	// Production intervals: every interval left zero takes its default.
	cfg := agent.Config{
		Controller: ln.Addr().String(), Pin: ca.Fingerprint(), JoinCode: code.Code,
		Secrets: secretstest.New(), DataDir: dir, Version: "soak",
	}
	errc := make(chan error, 1)
	go func() { errc <- agent.Run(ctx, cfg) }()

	if err := waitOnline(ctx, svc, 30*time.Second); err != nil {
		return err
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	cpu0, wall0 := cpuTime(), time.Now()
	select {
	case <-time.After(window):
	case err := <-errc:
		return fmt.Errorf("agent stopped during the window: %v", err)
	}
	cpu, wall := cpuTime()-cpu0, time.Since(wall0)
	runtime.GC()
	runtime.ReadMemStats(&after)

	pct := float64(cpu) / float64(wall) * 100
	fmt.Printf("window %s, cpu %.3f%% of one core (limit %.0f%%), heap %d MB (limit %d MB)\n",
		wall.Round(time.Second), pct, maxCPUPercent, after.HeapAlloc>>20, maxHeapBytes>>20)
	cancel()
	err = <-errc
	if err != nil && ctx.Err() == nil {
		return err
	}
	if pct >= maxCPUPercent {
		return fmt.Errorf("idle cpu %.2f%% of one core, want under %.0f%% (agent and controller share this process)", pct, maxCPUPercent)
	}
	if after.HeapAlloc > maxHeapBytes {
		return fmt.Errorf("idle heap %d MB, want under %d MB", after.HeapAlloc>>20, maxHeapBytes>>20)
	}
	return nil
}

func waitOnline(ctx context.Context, svc *machines.Service, limit time.Duration) error {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(limit)
	for {
		if ms, _ := svc.List(ctx); len(ms) == 1 && ms[0].Machine.Online {
			return nil
		}
		select {
		case <-tick.C:
		case <-deadline:
			return fmt.Errorf("agent did not come online in %s", limit)
		}
	}
}
