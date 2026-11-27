package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// SessionCookieName is the web session cookie.
const SessionCookieName = "crsp_session"

// SetupCookieName is the first-run cookie.
const SetupCookieName = "crsp_setup"

// HandoffTTL is how long the one-time code of a sign-in handoff lives (OPS16).
const HandoffTTL = time.Minute

// SignInStart is what the browser is sent to (R5.8).
type SignInStart struct {
	RedirectURL string
	// HandoffBind, when set, is a value for the HandoffCookieName cookie on
	// the address the browser used. It ties the later handoff to this browser.
	HandoffBind string
}

// HandoffCookieName is the cookie that ties a sign-in handoff to the browser
// that started it (OPS16). Host-only, HttpOnly, short lived.
const HandoffCookieName = "crsp_handoff"

// SignInOptions are the inputs of StartSignIn.
type SignInOptions struct {
	// Origin is the console origin the browser used, such as
	// http://127.0.0.1:8460. It picks the callback URL (R5.8).
	Origin string
	// ReturnTo is a same-origin path to land on after sign-in.
	ReturnTo string
	// SetupCookie, when it holds a redeemed setup code and there is no owner,
	// lets this sign-in claim ownership (R5.10).
	SetupCookie string
	// BindTray is a ticket from CreateBindTicket (owner decision for M1).
	BindTray string
	// SameMachine is true when the browser runs on the PC of the controller
	// (the request came from a loopback or an own address), so the loopback
	// callback works for it (OPS16).
	SameMachine bool
}

// SafeReturnTo keeps only a same-origin path (web-console R5.9).
func SafeReturnTo(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") ||
		strings.ContainsAny(p, "\r\n\\") {
		return "/"
	}
	if u, err := url.Parse(p); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return p
}

// redirectURI is the callback URL of an address the App has a callback URL
// for. Plain http works on loopback only (R5.8, R6.4).
func (s *Service) redirectURI(ctx context.Context, origin string) (string, error) {
	o, err := NormalizeConsoleURL(origin)
	if err != nil {
		return "", ErrInsecure
	}
	if s.isRegistered(ctx, o) {
		return o + CallbackPath, nil
	}
	return "", s.notRegistered(ctx, o)
}

// StartSignIn makes the state and the PKCE challenge, and returns GitHub's
// authorize URL (R5.8). Both are single use and live 10 minutes.
//
// The redirect_uri is always one GitHub knows (OPS16): the address the
// browser used when the App has a callback URL for it; else, for a browser on
// this PC, the registered loopback callback, and the browser is handed back to
// the address it started from with a one-time code (RedeemHandoff); else a
// dry sign-in asks GitHub whether the address was added since, and when it
// was not, the sign-in stops with a NotRegisteredError that carries the exact
// callback URL to add.
func (s *Service) StartSignIn(ctx context.Context, o SignInOptions) (SignInStart, error) {
	if _, err := s.store.GetApp(ctx); err != nil {
		return SignInStart{}, ErrNoApp
	}
	origin, err := NormalizeConsoleURL(o.Origin)
	if err != nil {
		return SignInStart{}, ErrInsecure
	}
	redirect, handoff, err := s.chooseRedirect(ctx, origin, o.SameMachine)
	if err != nil {
		return SignInStart{}, err
	}
	verifier := RandomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	data := map[string]string{"verifier": verifier, "redirect": redirect, "return_to": SafeReturnTo(o.ReturnTo)}
	var bindCookie string
	if handoff {
		bindCookie = RandomToken(24)
		data["handoff_origin"] = origin
		data["handoff_bind"] = Hash(bindCookie)
	}
	if s.CheckSetupCookie(ctx, o.SetupCookie) {
		data["claim_owner"] = "1"
	}
	if o.BindTray != "" {
		if _, ok := s.states.peek("bind", o.BindTray); ok {
			data["bind"] = o.BindTray
		}
	}
	state := s.states.put("signin", StateTTL, data)
	return SignInStart{RedirectURL: s.cfg.GitHub.AuthorizeURL(state, challenge, redirect), HandoffBind: bindCookie}, nil
}

// Sign-in failure codes. The sign-in page shows a plain message for each; the
// controller logs the code and the cause at WARN (OPS17).
const (
	SignInStateMissing   = "state_missing"   // no sign-in state: used, expired or never made
	SignInStateMismatch  = "state_mismatch"  // the state was made for another callback address
	SignInAddressRefused = "address_refused" // the address cannot sign in (plain http off loopback)
	SignInExchangeFailed = "exchange_failed" // GitHub did not accept the sign-in code
	SignInUserFailed     = "user_failed"     // GitHub did not tell who signed in
	SignInSessionFailed  = "session_failed"  // the controller could not make the session
)

// SignInError is a sign-in failure with a stable code and the cause behind it.
type SignInError struct {
	Code  string
	Cause error
}

func (e *SignInError) Error() string {
	if e.Cause == nil {
		return "sign-in failed: " + e.Code
	}
	return "sign-in failed: " + e.Code + ": " + e.Cause.Error()
}

func (e *SignInError) Unwrap() error { return e.Cause }

func signInFail(code string, cause error) error { return &SignInError{Code: code, Cause: cause} }

// SignedIn is the result of a good callback.
type SignedIn struct {
	Principal Principal
	// Cookie is the session cookie value; show it once.
	Cookie   string
	ReturnTo string
	// Owner is true when this sign-in made the owner.
	Owner bool
	// Handoff is set when the sign-in came back on a registered loopback
	// address but started on another one: the browser goes on to that address
	// with the code, and no cookie is set here (OPS16).
	Handoff *Handoff
}

// Handoff sends the browser back to the address it started the sign-in on.
type Handoff struct {
	Origin string
	Code   string
	State  string
}

// CompleteSignIn finishes the callback: state and PKCE, the GitHub user, the
// allow list, the owner claim, the session (R5.8 to R5.13). origin is the
// address the browser used; an HTTP callback on a non-loopback address is
// refused (R6.4).
func (s *Service) CompleteSignIn(ctx context.Context, code, state, origin string) (SignedIn, error) {
	redirect, err := s.redirectURI(ctx, origin)
	if err != nil {
		s.audit(ctx, "anonymous", "auth.signin_failed", "session", "", "address refused", "failed")
		var nr *NotRegisteredError
		if errors.As(err, &nr) {
			return SignedIn{}, err
		}
		return SignedIn{}, signInFail(SignInAddressRefused, err)
	}
	data, ok := s.states.take("signin", state)
	if !ok {
		s.audit(ctx, "anonymous", "auth.signin_failed", "session", "", "bad or old state", "failed")
		return SignedIn{}, signInFail(SignInStateMissing, ErrBadState)
	}
	if data["redirect"] != redirect {
		s.audit(ctx, "anonymous", "auth.signin_failed", "session", "", "state for another address", "failed")
		return SignedIn{}, signInFail(SignInStateMismatch, ErrBadState)
	}
	tok, err := s.cfg.GitHub.ExchangeCode(ctx, code, data["verifier"], redirect)
	if err != nil {
		s.audit(ctx, "anonymous", "auth.signin_failed", "session", "", "code refused", "failed")
		return SignedIn{}, signInFail(SignInExchangeFailed, err)
	}
	gu, err := s.cfg.GitHub.CurrentUser(ctx, tok)
	if err != nil {
		return SignedIn{}, signInFail(SignInUserFailed, err)
	}
	si, err := s.finishSignIn(ctx, gu, data)
	if err != nil {
		var na *NotAllowedError
		if errors.As(err, &na) {
			return si, err
		}
		return si, signInFail(SignInSessionFailed, err)
	}
	s.holdToken(gu.ID, tok)
	if ho := data["handoff_origin"]; ho != "" {
		// The session cookie belongs to the address the sign-in started on,
		// not to this loopback address: keep it behind a one-time code.
		code := s.states.put("handoff", HandoffTTL, map[string]string{"cookie": si.Cookie, "origin": ho,
			"state": state, "bind": data["handoff_bind"], "return_to": si.ReturnTo})
		si.Handoff = &Handoff{Origin: ho, Code: code, State: state}
		si.Cookie = ""
	}
	return si, nil
}

// RedeemHandoff trades the one-time code for the session cookie on the
// address the sign-in started on (OPS16). The code is single use and lives
// HandoffTTL; it must come with the sign-in state it was made for, on the
// address it was made for, from the browser that started the sign-in (the
// bind cookie). Any mismatch burns the code and ends the session it held.
func (s *Service) RedeemHandoff(ctx context.Context, code, state, origin, bindCookie string) (SignedIn, error) {
	data, ok := s.states.take("handoff", code)
	if !ok {
		s.audit(ctx, "anonymous", "auth.handoff_failed", "session", "", "bad, used or old code", "failed")
		return SignedIn{}, ErrBadCode
	}
	o, err := NormalizeConsoleURL(origin)
	if err != nil || o != data["origin"] || !equal(state, data["state"]) ||
		bindCookie == "" || !equal(Hash(bindCookie), data["bind"]) {
		_ = s.store.EndSession(ctx, Hash(data["cookie"]))
		s.audit(ctx, "anonymous", "auth.handoff_failed", "session", "", "wrong address, state or browser", "failed")
		return SignedIn{}, ErrBadCode
	}
	return SignedIn{Cookie: data["cookie"], ReturnTo: data["return_to"]}, nil
}

func (s *Service) finishSignIn(ctx context.Context, gu github.User, data map[string]string) (SignedIn, error) {
	owner := false
	u, err := s.store.GetUser(ctx, gu.ID)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		if data["claim_owner"] != "1" {
			s.audit(ctx, gu.Login, "auth.signin_failed", "session", gu.Login, "not on the list", "failed")
			return SignedIn{}, &NotAllowedError{Login: gu.Login}
		}
		// The first sign-in with a valid setup code becomes the owner. The
		// unique owner rule makes two racing claims safe: one loses.
		nu := User{GitHubID: gu.ID, Login: gu.Login, Role: RoleOwner, AddedBy: "setup", AddedAt: s.cfg.Now()}
		if err := s.store.InsertUser(ctx, nu); err != nil {
			if errors.Is(err, ErrConflict) {
				s.audit(ctx, gu.Login, "auth.signin_failed", "session", gu.Login, "owner already exists", "failed")
				return SignedIn{}, &NotAllowedError{Login: gu.Login}
			}
			return SignedIn{}, err
		}
		u, owner = nu, true
		s.audit(ctx, gu.Login, "auth.owner_claimed", "user", gu.Login, "", "done")
	default:
		return SignedIn{}, err
	}
	// The login is shown and refreshed at each sign-in; identity is the id (R5.9).
	if err := s.store.RecordSignIn(ctx, u.GitHubID, gu.Login, s.cfg.Now()); err != nil {
		return SignedIn{}, err
	}
	u.Login = gu.Login

	cookie, p, err := s.newSession(ctx, u, "web")
	if err != nil {
		return SignedIn{}, err
	}
	if t := data["bind"]; t != "" {
		if b, ok := s.states.take("bind", t); ok {
			_ = s.store.UpsertTrayBinding(ctx, TrayBinding{MachineID: b["machine"], OSUserID: b["os_user"], UserID: u.GitHubID})
			s.auditUser(ctx, p, "auth.tray_bound", "user", u.Login, "")
		}
	}
	s.auditUser(ctx, p, "auth.signin", "session", u.Login, "")
	return SignedIn{Principal: p, Cookie: cookie, ReturnTo: data["return_to"], Owner: owner}, nil
}

// csrfFor derives the CSRF token of a session from its id, so GET /me can
// show it again while the database keeps only a hash.
func csrfFor(sessionID string) string {
	mac := hmac.New(sha256.New, []byte(sessionID))
	mac.Write([]byte("crsp-csrf"))
	return hex.EncodeToString(mac.Sum(nil))
}

// newSession stores a session for u. Sessions never expire by time; they end
// at sign-out, when the user is removed and when their role changes (owner
// decision for M1, which replaces the idle and 7-day limits of R5.13).
func (s *Service) newSession(ctx context.Context, u User, source string) (string, Principal, error) {
	id := RandomToken(32)
	csrf := csrfFor(id)
	if err := s.store.InsertSession(ctx, Session{IDHash: Hash(id), CSRFHash: Hash(csrf), UserID: u.GitHubID, Source: source}); err != nil {
		return "", Principal{}, err
	}
	return id, Principal{UserID: u.GitHubID, Login: u.Login, Role: u.Role, Source: source, SessionHash: Hash(id), CSRF: csrf}, nil
}

// Authenticate turns a session cookie value into a Principal.
func (s *Service) Authenticate(ctx context.Context, cookie string) (Principal, error) {
	if cookie == "" {
		return Principal{}, ErrUnauthenticated
	}
	sess, u, err := s.store.GetSession(ctx, Hash(cookie))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	_ = s.store.TouchSession(ctx, sess.IDHash, s.cfg.Now())
	return Principal{UserID: u.GitHubID, Login: u.Login, Role: u.Role, Source: sess.Source,
		SessionHash: sess.IDHash, CSRF: csrfFor(cookie)}, nil
}

// CheckCSRF compares the X-CSRF-Token header with the session's token.
func CheckCSRF(p Principal, header string) bool {
	return p.SessionHash != "" && header != "" && equal(p.CSRF, header)
}

// SignOut ends the session of p (R5.13).
func (s *Service) SignOut(ctx context.Context, p Principal) error {
	if p.SessionHash == "" {
		return nil
	}
	if err := s.store.EndSession(ctx, p.SessionHash); err != nil {
		return err
	}
	s.dropToken(p.UserID)
	s.auditUser(ctx, p, "auth.signout", "session", p.Login, "")
	return nil
}

// CookieSecure reports whether a cookie set on r carries the Secure flag: on
// every address except 127.0.0.1 and ::1 (web-console R6.1, github-integration
// R5.13). The console is TLS only off loopback, so r.TLS is set there; the
// host check keeps a proxy or a test server from handing out a plain cookie
// for a non-loopback name. On loopback the console is plain HTTP, and a
// Secure cookie would never come back.
func CookieSecure(r *http.Request) bool {
	if r == nil {
		return true
	}
	return r.TLS != nil || !IsLoopbackHost(r.Host)
}

// SessionCookie builds the session cookie (web-console R6.1): HttpOnly,
// SameSite=Lax and host-only (no Domain), Secure when secure is set (see
// CookieSecure). The cookie has no time limit short of the browser's own cap,
// and the session no time limit at all (D39), so a browser restart does not
// sign anyone out.
func SessionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{Name: SessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: 400 * 24 * 3600}
}

// ClearCookie ends the cookie in the browser.
func ClearCookie(name string, secure bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1}
}
