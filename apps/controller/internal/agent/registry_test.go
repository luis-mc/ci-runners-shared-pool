package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

type sentLog struct {
	mu   sync.Mutex
	msgs []*agentv1.SessionRequest
	err  error
	got  chan *agentv1.SessionRequest
}

func newSentLog() *sentLog { return &sentLog{got: make(chan *agentv1.SessionRequest, 16)} }

func (s *sentLog) send(m *agentv1.SessionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.msgs = append(s.msgs, m)
	s.got <- m
	return nil
}

func waitMsg(t *testing.T, s *sentLog) *agentv1.SessionRequest {
	t.Helper()
	select {
	case m := <-s.got:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent")
		return nil
	}
}

// R5.19: an agent asks only when the HelloAck says version 3 or more.
func TestHelloAckHasRegistry(t *testing.T) {
	for v, want := range map[agentv1.ProtocolVersion]bool{
		agentv1.ProtocolVersion_PROTOCOL_VERSION_1: false,
		agentv1.ProtocolVersion_PROTOCOL_VERSION_2: false,
		agentv1.ProtocolVersion_PROTOCOL_VERSION_3: true,
	} {
		if got := HelloAckHasRegistry(&agentv1.HelloAck{ProtocolVersion: v}); got != want {
			t.Errorf("version %v: got %v", v, got)
		}
	}
	c := NewRegistryClient()
	if c.Available() {
		t.Fatal("available before any ack")
	}
	s := c.attach(newSentLog().send, false)
	if c.Available() {
		t.Fatal("available after an old ack")
	}
	c.detach(s)
	s = c.attach(newSentLog().send, true)
	c.detach(s)
	if !c.Available() {
		t.Fatal("a controller that said version 3 stays known while the link is down")
	}
}

// R5.14: with no session a call fails with ErrNoController: the backend waits.
func TestRegistryClientNoSession(t *testing.T) {
	c := NewRegistryClient()
	if _, err := c.EnsureImage(context.Background(), &agentv1.EnsureImage{RequestId: "r1"}); !errors.Is(err, container.ErrNoController) {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := c.RegistryToken(context.Background(), &agentv1.RegistryTokenRequest{RequestId: "r2"}); !errors.Is(err, container.ErrNoController) {
		t.Fatalf("token: %v", err)
	}
	c.PullReport(&agentv1.PullReport{}) // dropped, no panic
	c.PushReport(&agentv1.PushReport{})
}

// R5.20: each answer carries the request id of its question.
func TestRegistryClientRoundTrip(t *testing.T) {
	c := NewRegistryClient()
	log := newSentLog()
	c.attach(log.send, true)
	type res struct {
		a   *agentv1.EnsureImageAnswer
		err error
	}
	out := make(chan res, 1)
	go func() {
		a, err := c.EnsureImage(context.Background(), &agentv1.EnsureImage{RequestId: "e1", Key: "abcdefabcdef", Arch: "amd64"})
		out <- res{a, err}
	}()
	m := waitMsg(t, log)
	if m.GetEnsureImage().GetRequestId() != "e1" || m.GetEnsureImage().GetKey() != "abcdefabcdef" {
		t.Fatalf("sent %v", m)
	}
	// An answer to another question is not ours.
	c.onAnswer(&agentv1.EnsureImageAnswer{RequestId: "other"})
	c.onAnswer(&agentv1.EnsureImageAnswer{RequestId: "e1", Outcome: agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND})
	r := <-out
	if r.err != nil || r.a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND {
		t.Fatalf("got %v %v", r.a, r.err)
	}

	tout := make(chan *agentv1.RegistryToken, 1)
	go func() {
		tk, _ := c.RegistryToken(context.Background(), &agentv1.RegistryTokenRequest{RequestId: "t1", Action: agentv1.RegistryTokenRequest_ACTION_PULL})
		tout <- tk
	}()
	if m := waitMsg(t, log); m.GetRegistryTokenRequest().GetRequestId() != "t1" {
		t.Fatalf("sent %v", m)
	}
	c.onToken(&agentv1.RegistryToken{RequestId: "t1", Token: "x"})
	if tk := <-tout; tk.GetToken() != "x" {
		t.Fatalf("token %v", tk)
	}

	c.PullReport(&agentv1.PullReport{PullId: "p"})
	c.PushReport(&agentv1.PushReport{PushId: "q"})
	if waitMsg(t, log).GetPullReport().GetPullId() != "p" || waitMsg(t, log).GetPushReport().GetPushId() != "q" {
		t.Fatal("reports not sent")
	}
}

// A link that drops while a question waits ends it at once, so the backend
// waits and asks again on the next session.
func TestRegistryClientDetachEndsPendingCalls(t *testing.T) {
	c := NewRegistryClient()
	log := newSentLog()
	s := c.attach(log.send, true)
	errc := make(chan error, 1)
	go func() {
		_, err := c.EnsureImage(context.Background(), &agentv1.EnsureImage{RequestId: "e1"})
		errc <- err
	}()
	waitMsg(t, log)
	c.detach(s)
	if err := <-errc; !errors.Is(err, container.ErrNoController) {
		t.Fatalf("got %v", err)
	}
}

func TestRegistryClientSendFailure(t *testing.T) {
	c := NewRegistryClient()
	log := newSentLog()
	log.err = errors.New("stream closed")
	c.attach(log.send, true)
	if _, err := c.EnsureImage(context.Background(), &agentv1.EnsureImage{RequestId: "e1"}); !errors.Is(err, container.ErrNoController) {
		t.Fatalf("got %v", err)
	}
}

// The agent says registry in its MachineReport (R5.19).
func TestMachineReportSaysRegistry(t *testing.T) {
	rep := buildMachineReportWith(context.Background(), OSSys{}, &agentv1.Facts{}, nil, nil, nil, time.Unix(1, 0))
	if !rep.GetRegistry() {
		t.Fatal("the machine report does not say registry")
	}
}

// IMG1: the agent reports each image build to the controller over the session;
// with no session, or a controller without the registry, nothing is sent and
// the build goes on.
func TestRegistryClientBuildReports(t *testing.T) {
	c := NewRegistryClient()
	ctx := context.Background()
	if _, err := c.Begin(ctx, builds.Meta{Key: "37fef5dbb41b"}); err == nil {
		t.Fatal("no session: the build is not recorded")
	}
	log := newSentLog()
	c.attach(log.send, false)
	if _, err := c.Begin(ctx, builds.Meta{Key: "37fef5dbb41b"}); err == nil {
		t.Fatal("a controller without the registry gets no build reports")
	}
	log = newSentLog()
	c.attach(log.send, true)
	h, err := c.Begin(ctx, builds.Meta{Profile: "node-web", Arch: "amd64", Key: "37fef5dbb41b", Reason: "not_in_registry"})
	if err != nil || h.ID == "" {
		t.Fatalf("begin %+v %v", h, err)
	}
	c.Progress(ctx, h.ID, builds.Progress{Step: 2, Total: 9, Name: "RUN apt-get update"})
	c.End(ctx, h.ID, builds.Outcome{State: builds.StateFailed, FailureClass: builds.ClassDisk, FailureStep: "RUN x", FailureWhy: "no space", LogTail: "tail"})

	b := waitMsg(t, log).GetBuildReport()
	if b.GetPhase() != agentv1.BuildReport_PHASE_BEGIN || b.GetBuildId() != h.ID || b.GetProfile() != "node-web" || b.GetReason() != "not_in_registry" {
		t.Fatalf("begin %+v", b)
	}
	p := waitMsg(t, log).GetBuildReport()
	if p.GetPhase() != agentv1.BuildReport_PHASE_PROGRESS || p.GetStep() != 2 || p.GetTotal() != 9 || p.GetStepName() != "RUN apt-get update" {
		t.Fatalf("progress %+v", p)
	}
	e := waitMsg(t, log).GetBuildReport()
	if e.GetPhase() != agentv1.BuildReport_PHASE_END || e.GetState() != builds.StateFailed || e.GetFailureClass() != builds.ClassDisk || e.GetLogTail() != "tail" {
		t.Fatalf("end %+v", e)
	}
}
