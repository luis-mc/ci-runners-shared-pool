package machines

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StuckAfter is how long an agent may stay on an older version after the
// controller asked it to update before it needs recovery (operations R5.44).
const StuckAfter = time.Hour

// Reasons a machine needs recovery.
const (
	ReasonOldProtocol = "old_protocol" // too old to speak the current agent link
	ReasonStuck       = "stuck"        // asked to update more than an hour ago, still old
)

// Recovery is a machine that cannot be updated through the normal link.
type Recovery struct {
	State  string // always "needs_recovery"
	Reason string
	Has    string // the version the agent runs
	Needs  string // the version the controller runs
	Since  time.Time
	// Armed is true after the owner pressed the action: the next recovery
	// call of the agent gets the signed update.
	Armed bool
}

// ErrNoRecovery is a machine that does not need recovery.
var ErrNoRecovery = fmt.Errorf("%w: this machine does not need recovery", ErrConflict)

// ErrTooOld is the refusal of a normal call from an agent below the minimum
// protocol; the agent keeps only the recovery call (R5.44).
var ErrTooOld = errors.New("this agent is too old for the agent link; it needs recovery")

type recState struct {
	refusedAt time.Time // first refused Session of an old-protocol agent
	version   string
	askedAt   time.Time // first ask for askedFor
	askedFor  string
	armed     bool
}

func parseTriple(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	p := strings.Split(v, ".")
	var n [3]int
	if len(p) != 3 {
		return n, false
	}
	for i, s := range p {
		x, err := strconv.Atoi(s)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// olderStable says cur is a stable version below latest (both must be stable,
// as in operations R5.39: a development build is never pulled).
func olderStable(cur, latest string) bool {
	c, ok1 := parseTriple(cur)
	l, ok2 := parseTriple(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if c[i] != l[i] {
			return c[i] < l[i]
		}
	}
	return false
}

func (s *Service) controllerVersion() string { return s.hooks.ControllerVersion }

// noteAsked records the first update ask of an agent that is still old.
func (s *Service) noteAsked(id, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.recov[id]
	if st.askedAt.IsZero() || st.askedFor != version {
		st.askedAt, st.askedFor = s.now(), version // a newer release starts the hour again
		s.recov[id] = st
	}
}

// noteRefused records a Session refused for an old protocol.
func (s *Service) noteRefused(id, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.recov[id]
	if st.refusedAt.IsZero() {
		st.refusedAt = s.now()
	}
	st.version = version
	s.recov[id] = st
}

// minProtocol is the lowest protocol version a Session accepts; 0 accepts all.
func (s *Service) tooOld(protocol int32) bool {
	return s.o.MinProtocol > 0 && protocol < s.o.MinProtocol
}

// RecoveryOf says whether the machine needs recovery. A machine that now runs
// the controller's version, or a Session that was accepted again, clears it.
func (s *Service) RecoveryOf(m Machine) (Recovery, bool) {
	need := s.controllerVersion()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.recov[m.ID]
	if !ok {
		return Recovery{}, false
	}
	has := m.AgentVersion
	if st.version != "" && st.refusedAt.After(st.askedAt) {
		has = st.version
	}
	if need != "" && has == need {
		delete(s.recov, m.ID)
		return Recovery{}, false
	}
	switch {
	case !st.refusedAt.IsZero():
		return Recovery{State: "needs_recovery", Reason: ReasonOldProtocol, Has: has, Needs: need, Since: st.refusedAt, Armed: st.armed}, true
	case !st.askedAt.IsZero() && olderStable(has, need) && s.now().Sub(st.askedAt) >= StuckAfter:
		return Recovery{State: "needs_recovery", Reason: ReasonStuck, Has: has, Needs: need, Since: st.askedAt.Add(StuckAfter), Armed: st.armed}, true
	}
	return Recovery{}, false
}

// clearRefused forgets an old-protocol refusal after an accepted Session.
func (s *Service) clearRefused(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.recov[id]; ok && !st.refusedAt.IsZero() {
		st.refusedAt, st.version = time.Time{}, ""
		s.recov[id] = st
	}
}

// ArmRecovery is the one action: the next recovery call of this machine gets
// the signed update. It audits the owner's choice.
func (s *Service) ArmRecovery(ctx context.Context, actor, id string) error {
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return err
	}
	if _, ok := s.RecoveryOf(m); !ok {
		return ErrNoRecovery
	}
	s.mu.Lock()
	st := s.recov[id]
	st.armed = true
	s.recov[id] = st
	s.mu.Unlock()
	_ = s.o.Repo.Audit(ctx, actor, "machine.recovery.armed", id, "")
	s.notifyMachine(id)
	return nil
}

// RecoveryArmed is the recovery handler's check: this machine needs recovery
// and the owner armed it.
func (s *Service) RecoveryArmed(ctx context.Context, id string) bool {
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return false
	}
	r, ok := s.RecoveryOf(m)
	return ok && r.Armed
}

// RecoveryServed audits a signed target that went out and disarms the machine.
func (s *Service) RecoveryServed(ctx context.Context, id, version string) {
	s.mu.Lock()
	st := s.recov[id]
	st.armed = false
	s.recov[id] = st
	s.mu.Unlock()
	_ = s.o.Repo.Audit(ctx, "agent", "machine.recovery.served", id, version)
}
