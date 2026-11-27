package auth

import (
	"context"
	"errors"
	"time"
)

// SetupCodeTTL is how long a printed setup code stays good.
const SetupCodeTTL = 24 * time.Hour

// SetupCookieTTL is how long a redeemed setup cookie works.
const SetupCookieTTL = 2 * time.Hour

// SetupState is the first-run state (R5.1).
type SetupState struct {
	HasApp   bool
	HasOwner bool
	App      AppRecord
}

// State reads the first-run state.
func (s *Service) State(ctx context.Context) (SetupState, error) {
	var st SetupState
	if a, err := s.store.GetApp(ctx); err == nil {
		st.HasApp, st.App = true, a
	} else if !errors.Is(err, ErrNotFound) {
		return st, err
	}
	if _, err := s.store.GetOwner(ctx); err == nil {
		st.HasOwner = true
	} else if !errors.Is(err, ErrNotFound) {
		return st, err
	}
	return st, nil
}

// NeedsSetup is true for a controller with no App and no owner: it serves
// only the wizard and health (R5.1).
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	st, err := s.State(ctx)
	if err != nil {
		return false, err
	}
	return !st.HasApp && !st.HasOwner, nil
}

// NewSetupCode makes the one-time code the controller prints at start and
// hands to the tray. It returns "" when an owner exists: the code is void
// then (R5.1).
func (s *Service) NewSetupCode(ctx context.Context) (string, error) {
	st, err := s.State(ctx)
	if err != nil {
		return "", err
	}
	if st.HasOwner {
		return "", nil
	}
	code := newCode()
	if err := s.store.InsertSetupCode(ctx, Hash(normalizeCode(code)), s.cfg.Now().Add(SetupCodeTTL)); err != nil {
		return "", err
	}
	return code, nil
}

// RedeemSetupCode trades a setup code, once, for a setup cookie value. It is
// refused when an owner exists.
func (s *Service) RedeemSetupCode(ctx context.Context, code string) (string, error) {
	st, err := s.State(ctx)
	if err != nil {
		return "", err
	}
	if st.HasOwner {
		return "", ErrSetupDone
	}
	ok, err := s.store.UseSetupCode(ctx, Hash(normalizeCode(code)), s.cfg.Now())
	if err != nil {
		return "", err
	}
	if !ok {
		s.audit(ctx, "setup", "setup.code_refused", "setup", "", "wrong, used or old code", "failed")
		return "", ErrBadCode
	}
	cookie := RandomToken(32)
	s.mu.Lock()
	s.setupCookies[Hash(cookie)] = s.cfg.Now().Add(SetupCookieTTL)
	s.mu.Unlock()
	return cookie, nil
}

// CheckSetupCookie reports whether a cookie value was made by a redeemed code
// and is still good, with no owner yet.
func (s *Service) CheckSetupCookie(ctx context.Context, cookie string) bool {
	if cookie == "" {
		return false
	}
	s.mu.Lock()
	exp, ok := s.setupCookies[Hash(cookie)]
	s.mu.Unlock()
	if !ok || !s.cfg.Now().Before(exp) {
		return false
	}
	st, err := s.State(ctx)
	return err == nil && !st.HasOwner
}
