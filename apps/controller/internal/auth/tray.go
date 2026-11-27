package auth

import (
	"context"
	"errors"
	"net"
	"time"
)

// LinkTTL is how long a tray link lives (tray R6.1).
const LinkTTL = 60 * time.Second

// Link purposes.
const (
	LinkSignIn   = "sign_in"
	LinkBindTray = "bind_tray"
)

// IssueLink makes a one-time link code for a user: 128 random bits, valid 60
// seconds, kept only as a hash. The caller is the tray's local channel, which
// has already proved the OS user (tray R5.7).
func (s *Service) IssueLink(ctx context.Context, userID int64, purpose, target string) (string, error) {
	if purpose != LinkSignIn && purpose != LinkBindTray {
		return "", Invalid("unknown link purpose")
	}
	if _, err := s.store.GetUser(ctx, userID); err != nil {
		return "", err
	}
	code := RandomToken(16)
	err := s.store.InsertLinkCode(ctx, LinkCode{Hash: Hash(code), UserID: userID, Purpose: purpose,
		TargetPath: SafeReturnTo(target), ExpiresAt: s.cfg.Now().Add(LinkTTL)})
	if err != nil {
		return "", err
	}
	u, _ := s.store.GetUser(ctx, userID)
	s.audit(ctx, "tray", "auth.link_issued", "session", u.Login, purpose, "done")
	return code, nil
}

// IssueLinkForOSUser makes a sign-in link for the GitHub user bound to an OS
// user of a machine. ErrNotFound means no binding yet: the tray starts the
// bind sign-in instead.
func (s *Service) IssueLinkForOSUser(ctx context.Context, machineID, osUserID, target string) (string, error) {
	b, err := s.store.GetTrayBinding(ctx, machineID, osUserID)
	if err != nil {
		return "", err
	}
	return s.IssueLink(ctx, b.UserID, LinkSignIn, target)
}

// CreateBindTicket makes the ticket the tray hands to /auth/github/login
// (bind_tray) so the GitHub sign-in binds that GitHub user to the OS user.
func (s *Service) CreateBindTicket(machineID, osUserID string) string {
	s.audit(context.Background(), "tray", "auth.bind_started", "session", "", "an OS user has no binding yet", "done")
	return s.states.put("bind", StateTTL, map[string]string{"machine": machineID, "os_user": osUserID})
}

// RedeemLink signs in with a tray link. Loopback only: remoteAddr must be a
// loopback address (owner decision for M1). The code is marked used in the
// same step that finds it, so it cannot be used twice. The answer is a
// session; the caller redirects to the target path, which holds no code.
func (s *Service) RedeemLink(ctx context.Context, code, remoteAddr string) (SignedIn, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if !IsLoopbackHost(host) {
		s.audit(ctx, "anonymous", "auth.link_failed", "session", "", "not from loopback", "failed")
		return SignedIn{}, ErrLoopbackOnly
	}
	lc, err := s.store.UseLinkCode(ctx, Hash(code), s.cfg.Now())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.audit(ctx, "anonymous", "auth.link_failed", "session", "", "wrong, used or old code", "failed")
			return SignedIn{}, ErrBadCode
		}
		return SignedIn{}, err
	}
	u, err := s.store.GetUser(ctx, lc.UserID)
	if err != nil {
		return SignedIn{}, ErrBadCode
	}
	cookie, p, err := s.newSession(ctx, u, "tray")
	if err != nil {
		return SignedIn{}, err
	}
	s.auditUser(ctx, p, "auth.signin", "session", u.Login, "tray link")
	return SignedIn{Principal: p, Cookie: cookie, ReturnTo: lc.TargetPath}, nil
}
