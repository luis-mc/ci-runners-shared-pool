package auth

import (
	"context"
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// GitHub is what auth needs from the GitHub adapter.
type GitHub interface {
	github.OAuth
	github.AppClient
	// CheckUserToken asks GitHub whether a user token was made for this App
	// (R5.14).
	CheckUserToken(ctx context.Context, tok github.UserToken) (github.User, error)
	// CheckRedirectURI is a dry sign-in: does GitHub know this callback URL
	// for the App (OPS16)?
	CheckRedirectURI(ctx context.Context, redirectURI string) (bool, error)
}

// Config builds a Service.
type Config struct {
	GitHub  GitHub
	Secrets secrets.Store
	// WebBase is GitHub's web address (default https://github.com).
	WebBase string
	// ConsoleURLs lists the console origins the App has callback URLs for
	// when none is stored yet (an App made before the list was kept: its
	// callback is the loopback address).
	ConsoleURLs func() []string
	// LoadRegistered and SaveRegistered keep the console origins GitHub knows
	// as callback URLs of the App. GitHub has no API to read or change them,
	// so the controller keeps the list it made the manifest with, plus every
	// address a dry sign-in confirmed (OPS16).
	LoadRegistered func(ctx context.Context) ([]string, error)
	SaveRegistered func(ctx context.Context, origins []string) error
	// LoadWebhookAddress and SaveWebhookAddress keep the public address the
	// owner gave for webhooks (OPS25). Nil keeps none.
	LoadWebhookAddress func(ctx context.Context) (string, error)
	SaveWebhookAddress func(ctx context.Context, address string) error
	// HookHealth gives the newest webhook delivery (OPS25); nil says none.
	HookHealth WebhookHealth
	// SelfTestHTTP is the client of the webhook self-test; nil uses
	// NewSelfTestClient. A test passes its own.
	SelfTestHTTP *http.Client
	// OnApp is called after the App is made, so the GitHub client can use it.
	OnApp func(AppRecord)
	Now   func() time.Time
}

// Service is sign-in, users, sessions, tokens and setup.
type Service struct {
	cfg    Config
	store  Store
	states *states

	keyMu             sync.Mutex // one key rotation at a time (R5.7)
	mu                sync.Mutex
	usedManifestCodes map[string]bool
	setupCookies      map[string]time.Time // hash -> expiry
	userTokens        map[int64]github.UserToken
	consoles          []string
	touched           map[string]time.Time // session hash -> when its last-seen time was written (touchEvery)
}

// touchEvery is how often a session's last-seen time is written: a signed-in page makes a dozen calls and each used to
// be a write on the single writer (CRSP290, audit F8). Nothing reads the time to a finer grain than minutes.
const touchEvery = time.Minute

// New returns a Service.
func New(store Store, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.WebBase == "" {
		cfg.WebBase = addresses.DefaultGitHubURL
	}
	if cfg.ConsoleURLs == nil {
		cfg.ConsoleURLs = func() []string { return nil }
	}
	return &Service{cfg: cfg, store: store, states: newStates(cfg.Now),
		usedManifestCodes: map[string]bool{}, setupCookies: map[string]time.Time{}, userTokens: map[int64]github.UserToken{}}
}

func secretKey(appID int64, name string) secrets.Key { return github.SecretKey(appID, name) }

var tokenLike = regexp.MustCompile(`(ghs_|ghu_|ghr_|gho_|ghp_|github_pat_)[A-Za-z0-9_]+|crsp_cli_[A-Za-z0-9_-]+|crsp_acc_[A-Za-z0-9_-]+|-----BEGIN [A-Z ]+-----[\s\S]*?(-----END [A-Z ]+-----|$)`)

// scrub removes anything that looks like a token or key from text that is
// about to go in the audit log (R5.16).
func scrub(s string) string { return tokenLike.ReplaceAllString(s, "[redacted]") }

// audit writes an audit event. A failure to write is returned to no one but
// is not hidden: the caller's action has already happened, and the next
// health check sees the audit table.
func (s *Service) audit(ctx context.Context, actor, action, targetType, subject, detail, result string) {
	_ = s.store.Audit(ctx, AuditEvent{Actor: scrub(actor), Action: action, TargetType: targetType,
		Subject: scrub(subject), Detail: scrub(detail), Result: result, Source: "controller"})
}

func (s *Service) auditUser(ctx context.Context, p Principal, action, targetType, subject, detail string) {
	_ = s.store.Audit(ctx, AuditEvent{Actor: p.Login, ActorID: strconv.FormatInt(p.UserID, 10), Source: p.Source,
		Action: action, TargetType: targetType, Subject: scrub(subject), Detail: scrub(detail), Result: "done"})
}

// Audit lets another package (repos) write an audit row as a principal.
func (s *Service) Audit(ctx context.Context, p Principal, action, targetType, subject, detail string) {
	s.auditUser(ctx, p, action, targetType, subject, detail)
}

// ErrGitHubSessionExpired means the GitHub user token that listing and the
// admin check need is gone or old: the person signs in with GitHub again. The
// token is held in memory only and never written anywhere (R6.2).
var ErrGitHubSessionExpired = errors.New("sign in with GitHub again")

// GitHubToken returns the user's GitHub token from this process's memory.
func (s *Service) GitHubToken(userID int64) (github.UserToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.userTokens[userID]
	if !ok || (!t.ExpiresAt.IsZero() && !s.cfg.Now().Before(t.ExpiresAt)) {
		return github.UserToken{}, ErrGitHubSessionExpired
	}
	return t, nil
}

// GitHubSignIn is the state of a user's GitHub sign-in.
type GitHubSignIn struct {
	Active bool
	// Expires is when the held token ends (zero when GitHub gave no limit).
	Expires time.Time
}

// GitHubSignInState says whether the user's memory-only GitHub token is held
// and still valid, and until when. It never returns the token.
func (s *Service) GitHubSignInState(userID int64) GitHubSignIn {
	t, err := s.GitHubToken(userID)
	if err != nil {
		return GitHubSignIn{}
	}
	return GitHubSignIn{Active: true, Expires: t.ExpiresAt}
}

func (s *Service) holdToken(userID int64, t github.UserToken) {
	s.mu.Lock()
	s.userTokens[userID] = t
	s.mu.Unlock()
}

func (s *Service) dropToken(userID int64) {
	s.mu.Lock()
	delete(s.userTokens, userID)
	s.mu.Unlock()
}

// SetConsoleURLs records the console addresses the App was made for, so
// sign-in accepts exactly those (R5.4, R5.8). The controller's config gives the
// same list at start.
func (s *Service) SetConsoleURLs(urls []string) {
	s.mu.Lock()
	s.consoles = append([]string(nil), urls...)
	s.mu.Unlock()
}

// consoleURLs is the list of console origins the App has callback URLs for:
// the stored list, else the one of this run's manifest, else the configured one.
func (s *Service) consoleURLs(ctx context.Context) []string {
	if s.cfg.LoadRegistered != nil {
		if l, err := s.cfg.LoadRegistered(ctx); err == nil && len(l) > 0 {
			return l
		}
	}
	s.mu.Lock()
	c := s.consoles
	s.mu.Unlock()
	if len(c) > 0 {
		return c
	}
	return s.cfg.ConsoleURLs()
}
