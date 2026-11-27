package machines

import (
	"sync"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// session is one open Session stream of a machine.
type session struct {
	out    chan *agentv1.SessionResponse
	cancel func()
}

// Sessions holds the open streams, one per machine. A command to a machine
// without a stream is ErrOffline; the controller never connects in (R5.9).
type Sessions struct {
	mu sync.Mutex
	m  map[string]*session
}

func newSessions() *Sessions { return &Sessions{m: map[string]*session{}} }

// open registers a stream, closing an older one of the same machine.
func (s *Sessions) open(machineID string, cancel func()) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.m[machineID]; old != nil {
		old.cancel()
	}
	ss := &session{out: make(chan *agentv1.SessionResponse, 64), cancel: cancel}
	s.m[machineID] = ss
	return ss
}

// close drops ss when it is still the current stream of the machine.
func (s *Sessions) close(machineID string, ss *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[machineID] == ss {
		delete(s.m, machineID)
	}
}

// send queues a message for the machine's stream.
func (s *Sessions) send(machineID string, msg *agentv1.SessionResponse) error {
	s.mu.Lock()
	ss := s.m[machineID]
	s.mu.Unlock()
	if ss == nil {
		return ErrOffline
	}
	select {
	case ss.out <- msg:
		return nil
	default:
		return ErrOffline // the agent does not keep up; treat as offline
	}
}

// Connected reports whether the machine has an open stream.
func (s *Sessions) Connected(machineID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[machineID] != nil
}

// Send sends a message to a machine's open stream, for other features
// (runners, scheduling, doctor) that command an agent.
func (s *Service) Send(machineID string, msg *agentv1.SessionResponse) error {
	return s.sessions.send(machineID, msg)
}

// Connected reports whether the machine has an open stream.
func (s *Service) Connected(machineID string) bool { return s.sessions.Connected(machineID) }

// kickAfterFlush closes the stream after the queued messages had a moment to
// go out (the MachineRemoved notice).
func (s *Sessions) kickAfterFlush(machineID string) {
	s.mu.Lock()
	ss := s.m[machineID]
	s.mu.Unlock()
	if ss == nil {
		return
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.mu.Lock()
		if s.m[machineID] == ss {
			delete(s.m, machineID)
		}
		s.mu.Unlock()
		ss.cancel()
	}()
}
