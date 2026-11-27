package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

// The agent side of the image registry calls (registry R5.19, R5.20, protocol
// version 3). The container backend asks through container.Controller; this
// client sends the request on the session that is open and gives it the
// answer with the same request id. With no session every call fails with
// container.ErrNoController, and the backend waits and asks again (owner
// decision: a job waits for the controller).

// registryCallBound bounds one question: longer than the controller's own
// bound for an answer, so a slow answer is not given up on first.
const registryCallBound = 45 * time.Second

// RegistryClient is the long-lived client of the agent. It outlives sessions;
// each session attaches itself on open and detaches on close.
type RegistryClient struct {
	mu        sync.Mutex
	cur       *registrySession
	supported bool // the controller's HelloAck said version 3 or more
	ensure    map[string]chan *agentv1.EnsureImageAnswer
	token     map[string]chan *agentv1.RegistryToken
}

type registrySession struct {
	send func(*agentv1.SessionRequest) error
	gone chan struct{}
}

var _ container.Controller = (*RegistryClient)(nil)

// NewRegistryClient makes a client with no session.
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{ensure: map[string]chan *agentv1.EnsureImageAnswer{}, token: map[string]chan *agentv1.RegistryToken{}}
}

// HelloAckHasRegistry says whether the controller's HelloAck allows the
// registry calls (R5.19).
func HelloAckHasRegistry(ack *agentv1.HelloAck) bool {
	return ack.GetProtocolVersion() >= agentv1.ProtocolVersion_PROTOCOL_VERSION_3
}

// attach opens the calls on a session. supported is what its HelloAck said;
// once true it stays true across a dropped link, so a job that started while
// the controller is away waits for it and does not build on its own.
func (c *RegistryClient) attach(send func(*agentv1.SessionRequest) error, supported bool) *registrySession {
	s := &registrySession{send: send, gone: make(chan struct{})}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = s
	if supported {
		c.supported = true
	}
	return s
}

// detach ends the session's calls; the ones waiting fail at once.
func (c *RegistryClient) detach(s *registrySession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == s {
		c.cur = nil
	}
	close(s.gone)
}

// Available implements container.Controller.
func (c *RegistryClient) Available() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.supported
}

// EnsureImage implements container.Controller.
func (c *RegistryClient) EnsureImage(ctx context.Context, req *agentv1.EnsureImage) (*agentv1.EnsureImageAnswer, error) {
	ch := make(chan *agentv1.EnsureImageAnswer, 1)
	s, err := c.begin(func() { c.ensure[req.GetRequestId()] = ch })
	if err != nil {
		return nil, err
	}
	defer c.end(func() { delete(c.ensure, req.GetRequestId()) })
	if err := s.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_EnsureImage{EnsureImage: req}}); err != nil {
		return nil, container.ErrNoController
	}
	select {
	case a := <-ch:
		return a, nil
	case <-s.gone:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(registryCallBound):
	}
	return nil, container.ErrNoController
}

// RegistryToken implements container.Controller.
func (c *RegistryClient) RegistryToken(ctx context.Context, req *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
	ch := make(chan *agentv1.RegistryToken, 1)
	s, err := c.begin(func() { c.token[req.GetRequestId()] = ch })
	if err != nil {
		return nil, err
	}
	defer c.end(func() { delete(c.token, req.GetRequestId()) })
	if err := s.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RegistryTokenRequest{RegistryTokenRequest: req}}); err != nil {
		return nil, container.ErrNoController
	}
	select {
	case t := <-ch:
		return t, nil
	case <-s.gone:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(registryCallBound):
	}
	return nil, container.ErrNoController
}

// PushReport implements container.Controller. A report with no session is
// dropped; the next one of the same push says the state again.
func (c *RegistryClient) PushReport(rep *agentv1.PushReport) {
	if s := c.session(); s != nil {
		_ = s.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_PushReport{PushReport: rep}})
	}
}

// PullReport implements container.Controller.
func (c *RegistryClient) PullReport(rep *agentv1.PullReport) {
	if s := c.session(); s != nil {
		_ = s.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_PullReport{PullReport: rep}})
	}
}

func (c *RegistryClient) session() *registrySession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *RegistryClient) begin(register func()) (*registrySession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil {
		return nil, container.ErrNoController
	}
	register()
	return c.cur, nil
}

func (c *RegistryClient) end(unregister func()) {
	c.mu.Lock()
	unregister()
	c.mu.Unlock()
}

// onAnswer gives an answer to the call that waits for it; an answer nobody
// waits for (it timed out) is dropped.
func (c *RegistryClient) onAnswer(a *agentv1.EnsureImageAnswer) {
	c.mu.Lock()
	ch := c.ensure[a.GetRequestId()]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- a:
		default:
		}
	}
}

func (c *RegistryClient) onToken(t *agentv1.RegistryToken) {
	c.mu.Lock()
	ch := c.token[t.GetRequestId()]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- t:
		default:
		}
	}
}

// The build records (IMG1, R5.68, R5.69): the agent tells the controller about
// each image build as it goes. The reports are best effort: with no session,
// or a controller that does not say it has the registry, the build goes on and
// is recorded nowhere.
var _ builds.Sink = (*RegistryClient)(nil)

// buildSession is the session to report to, or nil when none can take it.
func (c *RegistryClient) buildSession() *registrySession {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.supported {
		return nil
	}
	return c.cur
}

func (c *RegistryClient) buildReport(rep *agentv1.BuildReport) error {
	s := c.buildSession()
	if s == nil {
		return container.ErrNoController
	}
	return s.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_BuildReport{BuildReport: rep}})
}

// Begin implements builds.Sink. The id is the agent's own; the controller maps
// it to its record.
func (c *RegistryClient) Begin(_ context.Context, m builds.Meta) (builds.Handle, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return builds.Handle{}, err
	}
	id := "abld_" + hex.EncodeToString(b[:])
	err := c.buildReport(&agentv1.BuildReport{
		BuildId: id, Phase: agentv1.BuildReport_PHASE_BEGIN,
		Profile: m.Profile, Arch: m.Arch, Key: m.Key, Reason: m.Reason,
	})
	if err != nil {
		return builds.Handle{}, err
	}
	return builds.Handle{ID: id}, nil
}

// Progress implements builds.Sink.
func (c *RegistryClient) Progress(_ context.Context, id string, p builds.Progress) {
	_ = c.buildReport(&agentv1.BuildReport{
		BuildId: id, Phase: agentv1.BuildReport_PHASE_PROGRESS,
		Step: int32(p.Step), Total: int32(p.Total), StepName: p.Name,
	})
}

// End implements builds.Sink.
func (c *RegistryClient) End(_ context.Context, id string, o builds.Outcome) {
	_ = c.buildReport(&agentv1.BuildReport{
		BuildId: id, Phase: agentv1.BuildReport_PHASE_END,
		State: o.State, SizeBytes: o.SizeBytes, FailureStep: o.FailureStep,
		FailureClass: o.FailureClass, FailureWhy: o.FailureWhy, LogTail: o.LogTail,
	})
}
