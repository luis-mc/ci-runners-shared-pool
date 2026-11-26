package machines

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1/agentv1connect"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tlsca"
)

// Header names of the agent link (agent.proto): after Join every call carries
// the machine key and the machine id.
const (
	HeaderAuthorization = "Authorization"
	HeaderMachineID     = "Crsp-Machine-Id"
	// ControllerVersionFallback is sent in HelloAck when none is set.
	ControllerVersionFallback = "dev"
)

// Hooks let other features join the link: scheduling answers gate messages and
// reconciles slots on reconnect (R5.38), runners keep their state.
type Hooks struct {
	// OnHello runs after a session opened and the machine's facts were stored.
	OnHello func(ctx context.Context, machineID string, hello *agentv1.Hello)
	// OnMessage sees every message the machine layer does not handle itself. It
	// answers through reply; it returns true when it handled the message.
	OnMessage func(ctx context.Context, machineID string, msg *agentv1.SessionRequest, reply func(*agentv1.SessionResponse)) bool
	// ControllerVersion is sent in HelloAck.
	ControllerVersion string
	// Registry says the controller answers the image registry calls (REG1,
	// REG4 wire it). HelloAck then carries protocol version 3, and an agent
	// asks only on that (registry R5.19); without it the agent builds as before.
	Registry bool
	// OnJobSamples takes the JobResourceSamples batches of agents (ci-run-insights R5.19). When set, and the
	// registry is on, HelloAck carries protocol version 4 and agents start sending them.
	OnJobSamples func(ctx context.Context, machineID string, m *agentv1.JobResourceSamples)
	// OnLogBatch takes the runner setup and agent log lines of agents (observability R5.20, R5.23). It
	// never answers; with no hook a batch is dropped.
	OnLogBatch func(ctx context.Context, machineID string, m *agentv1.LogBatch)
}

// RecoveryPath is the frozen recovery call (internal/recovery.Path).
const RecoveryPath = "/recovery/v1/poll"

// SetRecoveryHandler mounts the recovery call on the link server; call it
// before NewLinkServer.
func (s *Service) SetRecoveryHandler(h http.Handler) { s.recoveryHandler = h }

// SetHooks sets the hooks; call it before the link serves.
func (s *Service) SetHooks(h Hooks) { s.hooks = h }

// Link is the AgentService implementation.
type Link struct{ svc *Service }

var _ agentv1connect.AgentServiceHandler = (*Link)(nil)

// LinkHandler returns the connect-go handler path and handler of AgentService.
func (s *Service) LinkHandler() (string, http.Handler) {
	return agentv1connect.NewAgentServiceHandler(&Link{svc: s})
}

func bearer(h http.Header) string {
	v := h.Get(HeaderAuthorization)
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

// Join implements AgentService (machines R5.3 to R5.7).
func (l *Link) Join(ctx context.Context, req *connect.Request[agentv1.JoinRequest]) (*connect.Response[agentv1.JoinResponse], error) {
	msg := req.Msg
	in := JoinInput{Facts: FactsFromProto(msg.GetFacts()), AgentVersion: msg.GetAgentVersion(), RemoteAddr: req.Peer().Addr}
	if in.Facts.HostName == "" && in.Facts.OS == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the join request has no facts"))
	}
	switch c := msg.GetCredential().(type) {
	case *agentv1.JoinRequest_JoinCode:
		in.Code = c.JoinCode
	case *agentv1.JoinRequest_LocalCredential:
		in.Code, in.Local = c.LocalCredential, true
	}
	res, err := l.svc.Join(ctx, in)
	if err != nil {
		var je *JoinError
		if errors.As(err, &je) {
			code := connect.CodePermissionDenied
			if strings.HasPrefix(je.Reason, "too many") {
				code = connect.CodeResourceExhausted
			}
			return nil, connect.NewError(code, errors.New(je.Reason))
		}
		if errors.Is(err, ErrInvalid) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the controller cannot join machines right now"))
	}
	// The machine's first facts carry the backends too.
	if err := l.svc.HandleFacts(ctx, res.MachineID, msg.GetAgentVersion(), msg.GetFacts()); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the controller cannot join machines right now"))
	}
	return connect.NewResponse(&agentv1.JoinResponse{
		MachineId: res.MachineID, MachineCredential: res.Key, CertSha256: res.Fingerprint,
		Settings: ProtoSettings(res.Settings, res.Machine.State),
	}), nil
}

// Session implements AgentService (machines R5.24, R5.35 to R5.38).
func (l *Link) Session(ctx context.Context, stream *connect.BidiStream[agentv1.SessionRequest, agentv1.SessionResponse]) error {
	svc := l.svc
	m, err := svc.Authenticate(ctx, bearer(stream.RequestHeader()), stream.RequestHeader().Get(HeaderMachineID))
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			// R5.39: the agent stops retrying on this code.
			return connect.NewError(connect.CodeUnauthenticated, errors.New("this machine was removed from the pool"))
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("the controller cannot check keys right now"))
	}
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the first message must be Hello"))
	}
	if svc.tooOld(int32(hello.GetProtocolVersion())) {
		// Normal calls are refused; the recovery call stays open (R5.44).
		svc.noteRefused(m.ID, hello.GetAgentVersion())
		_ = svc.o.Repo.Audit(ctx, "agent", "machine.agent_too_old", m.ID, hello.GetAgentVersion())
		svc.notifyMachine(m.ID)
		return connect.NewError(connect.CodeFailedPrecondition, ErrTooOld)
	}
	svc.clearRefused(m.ID)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ss := svc.sessions.open(m.ID, cancel)
	defer svc.sessions.close(m.ID, ss)

	if err := svc.MarkSeen(ctx, m.ID); err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("the controller cannot reach its database"))
	}
	if hello.GetFacts() != nil {
		_ = svc.HandleFacts(ctx, m.ID, hello.GetAgentVersion(), hello.GetFacts())
	}
	if hello.GetLoad() != nil {
		_ = svc.HandleLoad(ctx, m.ID, hello.GetLoad())
	}
	var jobs []JobRef
	for _, j := range hello.GetHolders().GetRunning() {
		jobs = append(jobs, jobFromProto(j))
	}
	var runners []string
	for _, r := range hello.GetRunners().GetRunners() {
		runners = append(runners, r.GetName())
	}
	svc.SetRunning(m.ID, jobs, runners)
	svc.mu.Lock()
	delete(svc.updates, m.ID) // a new session: the agent restarted or reconnected, so an old report is stale
	svc.mu.Unlock()
	if svc.hooks.OnHello != nil {
		svc.hooks.OnHello(ctx, m.ID, hello)
	}
	svc.finishRestart(ctx, m.ID) // a restart that waited for this agent is over

	cur, _ := svc.o.Repo.GetSettings(ctx, m.ID)
	mach, _ := svc.o.Repo.GetMachine(ctx, m.ID)
	ver := svc.hooks.ControllerVersion
	if ver == "" {
		ver = ControllerVersionFallback
	}
	ackVersion := agentv1.ProtocolVersion_PROTOCOL_VERSION_1
	if svc.hooks.Registry {
		ackVersion = agentv1.ProtocolVersion_PROTOCOL_VERSION_3
		if svc.hooks.OnJobSamples != nil {
			ackVersion = agentv1.ProtocolVersion_PROTOCOL_VERSION_4
		}
	}
	ack := &agentv1.SessionResponse{Message: &agentv1.SessionResponse_HelloAck{HelloAck: &agentv1.HelloAck{
		ProtocolVersion: ackVersion, ControllerVersion: ver,
		Settings: ProtoSettings(cur, mach.State), HeartbeatSeconds: int32(HeartbeatEvery / time.Second),
	}}}
	if err := stream.Send(ack); err != nil {
		return err
	}
	// A change that was waiting when the link dropped drains again (R5.31).
	if c, open, _ := svc.o.Repo.OpenChange(ctx, m.ID); open {
		mins := int(time.Until(c.Deadline).Minutes())
		if mins < 1 {
			mins = 1
		}
		svc.sendDrain(c, mins)
	}

	// One writer: stream.Send is never called from two goroutines.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ss.out:
				if err := stream.Send(msg); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()

	reply := func(r *agentv1.SessionResponse) { _ = svc.sessions.send(m.ID, r) }
	// Receive blocks on the request body and does not see ctx; read in its own
	// goroutine so a kick (key revoked, newer session) ends the handler at once.
	type recv struct {
		msg *agentv1.SessionRequest
		err error
	}
	in := make(chan recv)
	go func() {
		for {
			req, err := stream.Receive()
			select {
			case in <- recv{req, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-in:
			if r.err != nil {
				return r.err
			}
			_ = svc.MarkSeen(ctx, m.ID)
			svc.dispatch(ctx, m.ID, hello.GetAgentVersion(), r.msg, reply)
		}
	}
}

func (s *Service) dispatch(ctx context.Context, id, agentVersion string, req *agentv1.SessionRequest, reply func(*agentv1.SessionResponse)) {
	switch msg := req.GetMessage().(type) {
	case *agentv1.SessionRequest_Heartbeat:
		reply(&agentv1.SessionResponse{Message: &agentv1.SessionResponse_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAtUnixMs: s.now().UnixMilli()}}})
	case *agentv1.SessionRequest_LoadReport:
		_ = s.HandleLoad(ctx, id, msg.LoadReport)
	case *agentv1.SessionRequest_AcceptingReport:
		s.HandleAccepting(ctx, id, msg.AcceptingReport)
	case *agentv1.SessionRequest_MachineReport:
		s.HandleMachineReport(id, msg.MachineReport)
		if s.hooks.OnMessage != nil {
			s.hooks.OnMessage(ctx, id, req, reply)
		}
	case *agentv1.SessionRequest_Facts:
		_ = s.HandleFacts(ctx, id, agentVersion, msg.Facts)
	case *agentv1.SessionRequest_UpdateReport:
		s.handleUpdateReport(ctx, id, msg.UpdateReport)
	case *agentv1.SessionRequest_DrainStatus:
		_ = s.HandleDrainStatus(ctx, id, msg.DrainStatus)
	case *agentv1.SessionRequest_HolderReport:
		var jobs []JobRef
		for _, j := range msg.HolderReport.GetRunning() {
			jobs = append(jobs, jobFromProto(j))
		}
		s.SetRunning(id, jobs, nil)
		if s.hooks.OnMessage != nil {
			s.hooks.OnMessage(ctx, id, req, reply)
		}
	case *agentv1.SessionRequest_RunnerStates:
		var names []string
		for _, r := range msg.RunnerStates.GetRunners() {
			names = append(names, r.GetName())
		}
		s.mu.Lock()
		s.runners[id] = names
		s.mu.Unlock()
		if s.hooks.OnMessage != nil {
			s.hooks.OnMessage(ctx, id, req, reply)
		}
	case *agentv1.SessionRequest_JobResourceSamples:
		if s.hooks.OnJobSamples != nil {
			s.hooks.OnJobSamples(ctx, id, msg.JobResourceSamples)
		}
	case *agentv1.SessionRequest_LogBatch:
		if s.hooks.OnLogBatch != nil {
			s.hooks.OnLogBatch(ctx, id, msg.LogBatch)
		}
	case *agentv1.SessionRequest_CommandResult:
		if !msg.CommandResult.GetOk() {
			_ = s.o.Repo.Audit(ctx, "agent", "machine.command.failed", id, msg.CommandResult.GetError())
		}
		if s.hooks.OnMessage != nil {
			s.hooks.OnMessage(ctx, id, req, reply)
		}
	default:
		if s.hooks.OnMessage != nil {
			s.hooks.OnMessage(ctx, id, req, reply)
		}
	}
}

// NewLinkServer is the :8461 server: TLS 1.3 only, with the controller
// certificate, serving AgentService (R6.1). The caller starts and stops it.
func NewLinkServer(addr string, ca *tlsca.Authority, svc *Service) *http.Server {
	mux := http.NewServeMux()
	path, h := svc.LinkHandler()
	mux.Handle(path, h)
	if rh := svc.recoveryHandler; rh != nil {
		mux.Handle(RecoveryPath, rh)
	}
	return &http.Server{
		Addr: addr, Handler: mux, TLSConfig: ca.ServerTLS(),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
	}
}
