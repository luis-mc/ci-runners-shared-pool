package machines_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1/agentv1connect"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	T    *testing.T
	Svc  *machines.Service
	Repo *machines.MemRepo
	CA   *tlsca.Authority
	Clk  *clock
	Addr string
	Note *notes
}

type notes struct {
	mu    sync.Mutex
	state []string
	load  []string
	// overdue holds one entry per DrainOverdue call: "<machine> <kind> <waited> <jobs>".
	overdue []string
	// stuck, when set, gets one line per DrainStuck call.
	stuck func(string)
}

// DrainStuck records the alert for a holder far past its usual duration (R5.56).
func (n *notes) DrainStuck(id, kind string, h machines.Holder, running time.Duration) {
	if n.stuck != nil {
		n.stuck(fmt.Sprintf("%s %s %s %s", id, kind, h.Job.Job, running))
	}
}

// DrainOverdue records the warning for a drain that passed its timeout (R5.33).
func (n *notes) DrainOverdue(id, kind string, waited time.Duration, jobs []machines.JobRef) {
	n.mu.Lock()
	n.overdue = append(n.overdue, fmt.Sprintf("%s %s %s %d", id, kind, waited, len(jobs)))
	n.mu.Unlock()
}

func (n *notes) MachineChanged(id string) { n.mu.Lock(); n.state = append(n.state, id); n.mu.Unlock() }
func (n *notes) LoadChanged(id string)    { n.mu.Lock(); n.load = append(n.load, id); n.mu.Unlock() }

func newEnv(t *testing.T, mod ...func(*machines.Options)) *env {
	t.Helper()
	ca, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New()})
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	repo := machines.NewMemRepo()
	repo.Now = clk.Now
	n := &notes{}
	o := machines.Options{Repo: repo, Now: clk.Now, ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: ca.Fingerprint(), Notify: n}
	for _, m := range mod {
		m(&o)
	}
	svc := machines.New(o)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := machines.NewLinkServer(ln.Addr().String(), ca, svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, ca.ServerTLS())) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &env{T: t, Svc: svc, Repo: repo, CA: ca, Clk: clk, Addr: ln.Addr().String(), Note: n}
}

func (e *env) client() agentv1connect.AgentServiceClient {
	hc := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS(e.CA.Fingerprint())}}
	return agentv1connect.NewAgentServiceClient(hc, "https://"+e.Addr, connect.WithGRPC())
}

func testFacts(host string) *agentv1.Facts {
	return &agentv1.Facts{
		HostName: host, Os: "linux", OsName: "Ubuntu", OsVersion: "24.04", Arch: "x64", CpuModel: "Test CPU",
		Cores: 4, Threads: 8, MemoryBytes: 32 << 30, DiskTotalBytes: 500 << 30, DiskFreeBytes: 200 << 30,
		Backends: []*agentv1.BackendSupport{
			{Name: "persistent", Present: true, Usable: true},
			{Name: "docker", Present: true, Usable: true, Version: "27.0"},
			{Name: "kvm", Present: false, Usable: false, Reason: "no /dev/kvm"},
		},
	}
}

// joined makes a code and joins a fake agent with it.
func (e *env) joined(host string) (*agentv1.JoinResponse, string) {
	e.T.Helper()
	code, err := e.Svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		e.T.Fatal(err)
	}
	resp, err := e.client().Join(context.Background(), connect.NewRequest(&agentv1.JoinRequest{
		Credential: &agentv1.JoinRequest_JoinCode{JoinCode: code.Code}, Facts: testFacts(host), AgentVersion: "test",
	}))
	if err != nil {
		e.T.Fatalf("join: %v", err)
	}
	return resp.Msg, code.Code
}

// fakeAgent is an open Session.
type fakeAgent struct {
	T      *testing.T
	Stream *connect.BidiStreamForClient[agentv1.SessionRequest, agentv1.SessionResponse]
	Ack    *agentv1.HelloAck
	in     chan *agentv1.SessionResponse
	errc   chan error
	cancel context.CancelFunc
}

func (e *env) session(machineID, key string, hello *agentv1.Hello) (*fakeAgent, error) {
	e.T.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st := e.client().Session(ctx)
	st.RequestHeader().Set("Authorization", "Bearer "+key)
	st.RequestHeader().Set("Crsp-Machine-Id", machineID)
	go func() { <-ctx.Done(); _ = st.CloseRequest(); _ = st.CloseResponse() }()
	if hello == nil {
		hello = &agentv1.Hello{}
	}
	hello.MachineId = machineID
	if hello.Facts == nil {
		hello.Facts = testFacts("box")
	}
	if err := st.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Hello{Hello: hello}}); err != nil {
		// On io.EOF the real error is on the receive side.
		if _, rerr := st.Receive(); rerr != nil {
			err = rerr
		}
		cancel()
		return nil, err
	}
	first, err := st.Receive()
	if err != nil {
		cancel()
		return nil, err
	}
	fa := &fakeAgent{T: e.T, Stream: st, Ack: first.GetHelloAck(), in: make(chan *agentv1.SessionResponse, 64), errc: make(chan error, 1), cancel: cancel}
	go func() {
		var m *agentv1.SessionResponse
		var err error
		for m, err = st.Receive(); err == nil; m, err = st.Receive() {
			fa.in <- m
		}
		fa.errc <- err
	}()
	e.T.Cleanup(cancel)
	return fa, nil
}

func (f *fakeAgent) Send(m *agentv1.SessionRequest) {
	f.T.Helper()
	if err := f.Stream.Send(m); err != nil {
		f.T.Fatalf("send: %v", err)
	}
}

// Next waits for the next message matching pred.
func (f *fakeAgent) Next(pred func(*agentv1.SessionResponse) bool) *agentv1.SessionResponse {
	f.T.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-f.in:
			if pred(m) {
				return m
			}
		case err := <-f.errc:
			f.T.Fatalf("stream ended while waiting: %v", err)
		case <-deadline:
			f.T.Fatal("timed out waiting for a message")
		}
	}
}

func (f *fakeAgent) Close() { f.cancel() }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitUntil(t, 5*time.Second, what, cond)
}

// waitUntil is waitFor with its own limit, for work that touches the real
// machine (detection runs docker, lspci and the like) and is slow when cold.
func waitUntil(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	if cond() {
		return
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for {
		select {
		case <-tick.C:
			if cond() {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func heartbeat() *agentv1.SessionRequest {
	return &agentv1.SessionRequest{Message: &agentv1.SessionRequest_Heartbeat{Heartbeat: &agentv1.Heartbeat{}}}
}

func isDrain(m *agentv1.SessionResponse) bool    { return m.GetDrain() != nil }
func isResume(m *agentv1.SessionResponse) bool   { return m.GetResume() != nil }
func isSettings(m *agentv1.SessionResponse) bool { return m.GetSettings() != nil }

func drainStatus(cmd string, idle bool, jobs ...*agentv1.JobRef) *agentv1.SessionRequest {
	return &agentv1.SessionRequest{Message: &agentv1.SessionRequest_DrainStatus{DrainStatus: &agentv1.DrainStatus{CommandId: cmd, Idle: idle, WaitingFor: jobs}}}
}

func ptr[T any](v T) *T { return &v }

func machinesHooks(onHello func(*agentv1.Hello)) machines.Hooks {
	return machines.Hooks{OnHello: func(_ context.Context, _ string, h *agentv1.Hello) { onHello(h) }}
}

func machinesUpdateSlots(n int) machines.SettingsUpdate { return machines.SettingsUpdate{JobSlots: &n} }

// restartedEnv is the controller after a restart: a new service and link server
// on the same repository and certificate authority, at a new address.
func restartedEnv(t *testing.T, e *env) *env {
	t.Helper()
	svc := machines.New(machines.Options{Repo: e.Repo, Now: e.Clk.Now, ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: e.CA.Fingerprint(), Notify: e.Note})
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := machines.NewLinkServer(ln.Addr().String(), e.CA, svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, e.CA.ServerTLS())) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &env{T: t, Svc: svc, Repo: e.Repo, CA: e.CA, Clk: e.Clk, Addr: ln.Addr().String(), Note: e.Note}
}
